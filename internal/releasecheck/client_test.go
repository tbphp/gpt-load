package releasecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/httpclient"
)

func TestClientUsesLatestGlobalProxyPolicyForEachFetch(t *testing.T) {
	t.Parallel()

	writeReleases := func(writer http.ResponseWriter) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[{"tag_name":"v2.0.1","html_url":"https://github.com/tbphp/gpt-load/releases/tag/v2.0.1","published_at":"2026-08-19T13:09:53Z","draft":false}]`))
	}
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		writeReleases(writer)
	}))
	defer target.Close()
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		proxyCalls.Add(1)
		writeReleases(writer)
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
	if _, err := client.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if proxyCalls.Load() != 1 || targetCalls.Load() != 0 {
		t.Fatalf("custom proxy/target calls = %d/%d, want 1/0", proxyCalls.Load(), targetCalls.Load())
	}

	effective = outboundproxy.Effective{
		Config: outboundproxy.Config{Mode: outboundproxy.ModeDirect},
		Source: outboundproxy.SourceGlobal,
	}
	if _, err := client.Fetch(t.Context()); err != nil {
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

func TestClientFetchUsesFixedPublicGitHubContract(t *testing.T) {
	published := "2026-08-19T13:09:53Z"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/repos/tbphp/gpt-load/releases" ||
			request.URL.Query().Get("per_page") != "30" || len(request.URL.Query()) != 1 {
			t.Errorf("request = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Accept") != "application/vnd.github+json" ||
			request.Header.Get("X-GitHub-Api-Version") != "2022-11-28" ||
			request.Header.Get("User-Agent") != "GPT-Load" ||
			request.Header.Get("Authorization") != "" {
			t.Errorf("request headers = %#v", request.Header)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `[
          {"tag_name":"v2.0.0-beta.7","html_url":"%s","published_at":"%s","draft":false,"prerelease":true,"ignored":"value"}
        ]`, testReleaseURL("v2.0.0-beta.7"), published)
	}))
	defer server.Close()

	client := &Client{
		httpClient:       server.Client(),
		endpoint:         server.URL + "/repos/tbphp/gpt-load/releases?per_page=30",
		maxResponseBytes: maxGitHubResponseBytes,
	}
	releases, err := client.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(releases) != 1 || releases[0].TagName != "v2.0.0-beta.7" ||
		releases[0].HTMLURL != testReleaseURL("v2.0.0-beta.7") || releases[0].Draft ||
		!releases[0].PublishedAt.Equal(mustParseReleaseTime(published)) {
		t.Fatalf("Fetch() = %#v", releases)
	}
}

func TestClientFetchReadsEligibleV2ReleaseFromSecondPage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/repos/tbphp/gpt-load/releases" ||
			request.URL.Query().Get("per_page") != "30" {
			t.Errorf("request = %s %s", request.Method, request.URL.String())
		}
		page := request.URL.Query().Get("page")
		switch requests {
		case 1:
			if page != "" {
				t.Errorf("first page query = %q, want empty", page)
			}
			writer.Header().Set("Link", `<http://127.0.0.1:1/untrusted?page=2>; rel="next"`)
			writeGitHubReleasePage(t, writer, releasePageTags("v3.0", githubReleasesPerPage))
		case 2:
			if page != "2" {
				t.Errorf("second page query = %q, want 2", page)
			}
			writeGitHubReleasePage(t, writer, []string{"v2.0.1"})
		default:
			t.Errorf("unexpected request %d: %s", requests, request.URL.String())
			writeGitHubReleasePage(t, writer, nil)
		}
	}))
	defer server.Close()

	client := &Client{
		httpClient:       server.Client(),
		endpoint:         server.URL + "/repos/tbphp/gpt-load/releases?per_page=30",
		maxResponseBytes: maxGitHubResponseBytes,
	}
	releases, err := client.Fetch(t.Context())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	update := SelectUpdate("v2.0.0", releases)
	if update == nil || update.Version != "v2.0.1" || requests != 2 {
		t.Fatalf("SelectUpdate()/requests = %#v/%d, want v2.0.1/2", update, requests)
	}
}

func TestClientFetchRejectsReleaseHistoryBeyondPageLimit(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		page := request.URL.Query().Get("page")
		wantPage := ""
		if requests > 1 {
			wantPage = strconv.Itoa(requests)
		}
		if page != wantPage {
			t.Errorf("request %d page = %q, want %q", requests, page, wantPage)
		}
		writeGitHubReleasePage(t, writer, releasePageTags("v3."+strconv.Itoa(requests), githubReleasesPerPage))
	}))
	defer server.Close()

	client := &Client{
		httpClient:       server.Client(),
		endpoint:         server.URL + "/repos/tbphp/gpt-load/releases?per_page=30",
		maxResponseBytes: maxGitHubResponseBytes,
	}
	if releases, err := client.Fetch(t.Context()); err == nil || releases != nil ||
		requests != maxGitHubReleasePages+1 {
		t.Fatalf(
			"Fetch() = %#v, %v after %d requests, want nil/error after %d",
			releases,
			err,
			requests,
			maxGitHubReleasePages+1,
		)
	}
}

func TestClientFetchRejectsUnusableResponses(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		limit       int64
	}{
		{name: "non success", status: http.StatusForbidden, contentType: "application/json", body: `{"message":"limited"}`},
		{name: "wrong content type", status: http.StatusOK, contentType: "text/html", body: `[]`},
		{name: "invalid json", status: http.StatusOK, contentType: "application/json", body: `[`},
		{name: "trailing json", status: http.StatusOK, contentType: "application/json", body: `[] {}`},
		{name: "oversized", status: http.StatusOK, contentType: "application/json", body: strings.Repeat(" ", 33), limit: 32},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", test.contentType)
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			limit := test.limit
			if limit == 0 {
				limit = maxGitHubResponseBytes
			}
			client := &Client{httpClient: server.Client(), endpoint: server.URL, maxResponseBytes: limit}
			if releases, err := client.Fetch(context.Background()); err == nil || releases != nil {
				t.Fatalf("Fetch() = %#v, %v, want error", releases, err)
			}
		})
	}
}

func TestClientFetchHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), endpoint: server.URL, maxResponseBytes: maxGitHubResponseBytes}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.Fetch(ctx)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Fetch() did not start an HTTP request")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Fetch() error = nil after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("Fetch() did not return after cancellation")
	}
}

func writeGitHubReleasePage(t *testing.T, writer http.ResponseWriter, tags []string) {
	t.Helper()
	published := "2026-08-19T13:09:53Z"
	payload := make([]map[string]any, 0, len(tags))
	for _, tag := range tags {
		payload = append(payload, map[string]any{
			"tag_name":     tag,
			"html_url":     testReleaseURL(tag),
			"published_at": published,
			"draft":        false,
		})
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		t.Errorf("encode release page: %v", err)
	}
}

func releasePageTags(prefix string, count int) []string {
	tags := make([]string, 0, count)
	for index := 0; index < count; index++ {
		tags = append(tags, prefix+"."+strconv.Itoa(index))
	}
	return tags
}
