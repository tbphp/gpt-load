package releasecheck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
