package catalog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/httpclient"
)

func TestNewClientUsesFixedManagedTransportContract(t *testing.T) {
	manager := httpclient.NewHTTPClientManager()
	client := NewClient(manager, nil)
	if client.endpoint != modelsDevEndpoint {
		t.Fatalf("production endpoint = %q, want %q", client.endpoint, modelsDevEndpoint)
	}
	managedClient, err := client.httpClientForSync()
	if err != nil {
		t.Fatal(err)
	}
	if managedClient.Timeout != 30*time.Second {
		t.Fatalf("request timeout = %s, want 30s", managedClient.Timeout)
	}
	transport, ok := managedClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", managedClient.Transport)
	}
	if transport.ResponseHeaderTimeout != 30*time.Second || transport.DisableCompression {
		t.Fatalf("transport header timeout/compression = %s/%t", transport.ResponseHeaderTimeout, transport.DisableCompression)
	}
	if transport.Proxy == nil {
		t.Fatal("production catalog client does not preserve environment proxy support")
	}
}

func TestClientUsesLatestGlobalProxyPolicyForEachSync(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"openai":{"id":"openai","name":"OpenAI","models":{}}}`)
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(raw)
	}))
	defer target.Close()
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		proxyCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(raw)
	}))
	defer proxy.Close()

	effective := outboundproxy.Effective{
		Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: proxy.URL},
		Source: outboundproxy.SourceGlobal,
	}
	var providerCalls atomic.Int32
	client := NewClient(httpclient.NewHTTPClientManager(), func() outboundproxy.Effective {
		providerCalls.Add(1)
		return effective
	})
	client.endpoint = target.URL
	if _, err := client.Sync(t.Context(), Metadata{}); err != nil {
		t.Fatal(err)
	}
	if proxyCalls.Load() != 1 || targetCalls.Load() != 0 {
		t.Fatalf("custom proxy/target calls = %d/%d, want 1/0", proxyCalls.Load(), targetCalls.Load())
	}

	effective = outboundproxy.Effective{
		Config: outboundproxy.Config{Mode: outboundproxy.ModeDirect},
		Source: outboundproxy.SourceGlobal,
	}
	if _, err := client.Sync(t.Context(), Metadata{}); err != nil {
		t.Fatal(err)
	}
	if proxyCalls.Load() != 1 || targetCalls.Load() != 1 || providerCalls.Load() != 2 {
		t.Fatalf(
			"direct proxy/target/provider calls = %d/%d/%d, want 1/1/2",
			proxyCalls.Load(),
			targetCalls.Load(),
			providerCalls.Load(),
		)
	}
}

func TestClientSyncSendsConditionalHeadersAndReturnsValidated200(t *testing.T) {
	raw := []byte(`{
		"openai":{"id":"openai","name":"OpenAI","models":{}},
		"volcengine":{"id":"volcengine","name":"Models.dev Volcengine","models":{
			"doubao-seed-2-0-pro-260215":{"id":"doubao-seed-2-0-pro-260215","name":"Upstream collision","cost":{"input":99,"cache_write":7}},
			"models-dev-only":{"id":"models-dev-only","name":"Models.dev only","cost":{"input":1}}
		}}
	}`)
	body := &trackedReadCloser{Reader: strings.NewReader(string(raw))}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://catalog.test/api.json" {
			t.Fatalf("request = %s %s", request.Method, request.URL)
		}
		if got := request.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q", got)
		}
		if got := request.Header.Get("If-None-Match"); got != `"old"` {
			t.Fatalf("If-None-Match = %q", got)
		}
		if got := request.Header.Get("If-Modified-Since"); got != "Mon, 03 Aug 2026 00:00:00 GMT" {
			t.Fatalf("If-Modified-Since = %q", got)
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Etag": {`"new"`}, "Last-Modified": {"Mon, 03 Aug 2026 01:00:00 GMT"}},
			Body:          body,
			ContentLength: int64(len(raw)),
			Request:       request,
		}, nil
	})
	client := newClientForTest(
		&http.Client{Transport: transport},
		"https://catalog.test/api.json",
		func() time.Time { return time.UnixMilli(1_754_180_400_123) },
	)
	previous := Metadata{ETag: `"old"`, LastModified: "Mon, 03 Aug 2026 00:00:00 GMT", SuccessfulFetchAtMillis: 17}

	result, err := client.Sync(context.Background(), previous)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.NotModified || result.Snapshot == nil || result.Snapshot.Providers["openai"].Name != "OpenAI" {
		t.Fatalf("Sync() result = %#v", result)
	}
	volcengine := result.Snapshot.Providers["volcengine"]
	if volcengine.Name != "Volcengine Ark" {
		t.Fatalf("merged Volcengine provider = %#v", volcengine)
	}
	if _, ok := volcengine.Models["models-dev-only"]; !ok {
		t.Fatal("Models.dev-only Volcengine model was not retained")
	}
	official := volcengine.Models["doubao-seed-2-0-pro-260215"]
	if official.Name != "Doubao Seed 2.0 Pro" || official.Cost == nil {
		t.Fatalf("official Volcengine model = %#v", official)
	}
	assertPrice(t, "official input", official.Cost.Prices.Input, 474_614_665, true)
	assertPrice(t, "official cache read", official.Cost.Prices.CacheRead, 94_922_933, true)
	assertPrice(t, "official cache write", official.Cost.Prices.CacheWrite, 0, false)
	if len(official.Cost.ContextTiers) != 2 ||
		official.Cost.ContextTiers[0].InputThresholdTokens != 32_001 ||
		official.Cost.ContextTiers[1].InputThresholdTokens != 128_001 {
		t.Fatalf("official context tiers = %#v", official.Cost.ContextTiers)
	}
	if string(result.RawJSON) != string(raw) {
		t.Fatalf("raw JSON = %s, want %s", result.RawJSON, raw)
	}
	wantMetadata := Metadata{
		ETag:                    `"new"`,
		LastModified:            "Mon, 03 Aug 2026 01:00:00 GMT",
		CheckedAtMillis:         1_754_180_400_123,
		SuccessfulFetchAtMillis: 1_754_180_400_123,
	}
	if result.Metadata != wantMetadata {
		t.Fatalf("metadata = %#v, want %#v", result.Metadata, wantMetadata)
	}
	if !body.closed {
		t.Fatal("response body was not closed")
	}
}

func TestClientSyncTreats304AsSuccessfulCheckAndPreservesValidators(t *testing.T) {
	body := &trackedReadCloser{Reader: strings.NewReader("ignored")}
	client := newClientForTest(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotModified, Header: http.Header{"Etag": {`"must-not-replace"`}}, Body: body, Request: request}, nil
	})}, "https://catalog.test/api.json", func() time.Time { return time.UnixMilli(99) })
	previous := Metadata{ETag: `"old"`, LastModified: "yesterday", CheckedAtMillis: 1, SuccessfulFetchAtMillis: 2}

	result, err := client.Sync(context.Background(), previous)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	want := previous
	want.CheckedAtMillis = 99
	if !result.NotModified || result.Metadata != want || result.RawJSON != nil || result.Snapshot != nil {
		t.Fatalf("304 result = %#v, want metadata %#v and no payload", result, want)
	}
	if !body.closed {
		t.Fatal("304 response body was not closed")
	}
}

func TestClientSyncRejectsNonSuccessCancellationAndOversizeBodies(t *testing.T) {
	t.Run("non success", func(t *testing.T) {
		body := &trackedReadCloser{Reader: strings.NewReader("upstream detail")}
		client := newClientForTest(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: body, Request: request}, nil
		})}, "https://catalog.test/api.json", time.Now)
		if _, err := client.Sync(context.Background(), Metadata{}); err == nil {
			t.Fatal("Sync() accepted non-200/304 response")
		}
		if !body.closed {
			t.Fatal("non-success response body was not closed")
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		client := newClientForTest(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}, "https://catalog.test/api.json", time.Now)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := client.Sync(ctx, Metadata{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Sync() error = %v, want context.Canceled", err)
		}
	})

	t.Run("declared content length", func(t *testing.T) {
		body := &trackedReadCloser{Reader: strings.NewReader(`{}`)}
		client := newClientForTest(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: body, ContentLength: maxCatalogBodyBytes + 1, Request: request}, nil
		})}, "https://catalog.test/api.json", time.Now)
		if _, err := client.Sync(context.Background(), Metadata{}); err == nil || !strings.Contains(err.Error(), "32 MiB") {
			t.Fatalf("Sync() error = %v, want 32 MiB limit", err)
		}
		if !body.closed {
			t.Fatal("oversize response body was not closed")
		}
	})

	t.Run("unknown content length", func(t *testing.T) {
		body := &trackedReadCloser{Reader: io.LimitReader(repeatingReader(' '), maxCatalogBodyBytes+1)}
		client := newClientForTest(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: body, ContentLength: -1, Request: request}, nil
		})}, "https://catalog.test/api.json", time.Now)
		if _, err := client.Sync(context.Background(), Metadata{}); err == nil || !strings.Contains(err.Error(), "32 MiB") {
			t.Fatalf("Sync() error = %v, want streaming 32 MiB limit", err)
		}
		if !body.closed {
			t.Fatal("streaming oversize response body was not closed")
		}
	})
}

func TestClientSyncUsesManagedSameOriginRedirectPolicy(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		targetCalls++
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/same":
			http.Redirect(writer, request, "/final", http.StatusFound)
		case "/cross":
			http.Redirect(writer, request, target.URL+"/final", http.StatusFound)
		case "/final":
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte(`{}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer source.Close()

	client := NewClient(httpclient.NewHTTPClientManager(), nil)
	client.endpoint = source.URL + "/same"
	if _, err := client.Sync(context.Background(), Metadata{}); err != nil {
		t.Fatalf("same-origin redirect Sync() error = %v", err)
	}
	client.endpoint = source.URL + "/cross"
	if _, err := client.Sync(context.Background(), Metadata{}); err == nil {
		t.Fatal("cross-origin redirect Sync() error = nil")
	}
	if targetCalls != 0 {
		t.Fatalf("cross-origin redirect target received %d requests", targetCalls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type trackedReadCloser struct {
	io.Reader
	closed bool
}

func (body *trackedReadCloser) Close() error {
	body.closed = true
	return nil
}

type repeatingReader byte

func (reader repeatingReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = byte(reader)
	}
	return len(buffer), nil
}
