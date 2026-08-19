package releasecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"gpt-load/internal/platform/httpclient"
)

const githubReleasesEndpoint = "https://api.github.com/repos/tbphp/gpt-load/releases?per_page=30"

const maxGitHubResponseBytes int64 = 1 << 20

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client reads public GPT-Load release metadata from GitHub.
type Client struct {
	httpClient       httpDoer
	endpoint         string
	maxResponseBytes int64
}

// NewClient creates the fixed public GitHub release client.
func NewClient(manager *httpclient.HTTPClientManager) *Client {
	if manager == nil {
		manager = httpclient.NewHTTPClientManager()
	}
	return &Client{
		httpClient: manager.GetClient(&httpclient.Config{
			ConnectTimeout:        5 * time.Second,
			RequestTimeout:        8 * time.Second,
			IdleConnTimeout:       30 * time.Second,
			MaxIdleConns:          2,
			MaxIdleConnsPerHost:   2,
			ResponseHeaderTimeout: 5 * time.Second,
			DisableCompression:    true,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: time.Second,
			DisableRedirects:      true,
		}),
		endpoint:         githubReleasesEndpoint,
		maxResponseBytes: maxGitHubResponseBytes,
	}
}

// Fetch returns the public releases used by the selector.
func (client *Client) Fetch(ctx context.Context) ([]Release, error) {
	if client == nil || client.httpClient == nil {
		return nil, fmt.Errorf("fetch GitHub releases: HTTP client is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint := client.endpoint
	if endpoint == "" {
		endpoint = githubReleasesEndpoint
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch GitHub releases: create request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "GPT-Load")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch GitHub releases: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch GitHub releases: status %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/json" && mediaType != "application/vnd.github+json") {
		return nil, fmt.Errorf("fetch GitHub releases: invalid content type")
	}
	limit := client.maxResponseBytes
	if limit <= 0 {
		limit = maxGitHubResponseBytes
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("fetch GitHub releases: read response: %w", err)
	}
	if int64(len(payload)) > limit {
		return nil, fmt.Errorf("fetch GitHub releases: response exceeds %d bytes", limit)
	}
	var upstream []struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
	}
	if err := json.Unmarshal(payload, &upstream); err != nil {
		return nil, fmt.Errorf("fetch GitHub releases: decode response: %w", err)
	}
	releases := make([]Release, 0, len(upstream))
	for _, release := range upstream {
		releases = append(releases, Release{
			TagName:     release.TagName,
			HTMLURL:     release.HTMLURL,
			PublishedAt: release.PublishedAt,
			Draft:       release.Draft,
		})
	}
	return releases, nil
}
