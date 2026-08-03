package catalog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/platform/httpclient"
)

func TestNewClientUsesFixedManagedTransportContract(t *testing.T) {
	manager := httpclient.NewHTTPClientManager()
	client := NewClient(manager, "http://proxy.example:8080")
	if client.endpoint != modelsDevEndpoint {
		t.Fatalf("production endpoint = %q, want %q", client.endpoint, modelsDevEndpoint)
	}
	if client.httpClient.Timeout != 30*time.Second {
		t.Fatalf("request timeout = %s, want 30s", client.httpClient.Timeout)
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.httpClient.Transport)
	}
	if transport.ResponseHeaderTimeout != 30*time.Second || transport.DisableCompression {
		t.Fatalf("transport header timeout/compression = %s/%t", transport.ResponseHeaderTimeout, transport.DisableCompression)
	}
	request := &http.Request{URL: &url.URL{Scheme: "https", Host: "models.dev"}}
	proxy, err := transport.Proxy(request)
	if err != nil || proxy == nil || proxy.String() != "http://proxy.example:8080" {
		t.Fatalf("transport proxy = %v, %v", proxy, err)
	}
}

func TestClientSyncSendsConditionalHeadersAndReturnsValidated200(t *testing.T) {
	raw := []byte(`{"openai":{"id":"openai","name":"OpenAI","models":{}}}`)
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

	client := NewClient(httpclient.NewHTTPClientManager(), "")
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
