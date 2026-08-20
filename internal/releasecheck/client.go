package releasecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"gpt-load/internal/platform/httpclient"
)

const githubReleasesEndpoint = "https://api.github.com/repos/tbphp/gpt-load/releases"

const (
	githubReleasesPerPage  = 30
	maxGitHubReleasePages  = 10
	maxGitHubResponseBytes = int64(1 << 20)
)

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
	releases := make([]Release, 0, githubReleasesPerPage)
	// 额外探测一页，用于区分刚好达到上限和仍有未读取数据。
	for page := 1; page <= maxGitHubReleasePages+1; page++ {
		pageEndpoint, err := releasePageEndpoint(endpoint, page)
		if err != nil {
			return nil, fmt.Errorf("fetch GitHub releases: create page endpoint: %w", err)
		}
		pageReleases, err := client.fetchPage(ctx, pageEndpoint)
		if err != nil {
			return nil, err
		}
		if len(pageReleases) > githubReleasesPerPage {
			return nil, fmt.Errorf(
				"fetch GitHub releases: page %d contains more than %d releases",
				page,
				githubReleasesPerPage,
			)
		}
		if page > maxGitHubReleasePages {
			if len(pageReleases) == 0 {
				return releases, nil
			}
			return nil, fmt.Errorf(
				"fetch GitHub releases: history exceeds %d pages",
				maxGitHubReleasePages,
			)
		}
		releases = append(releases, pageReleases...)
		if len(pageReleases) < githubReleasesPerPage {
			return releases, nil
		}
	}
	return nil, fmt.Errorf("fetch GitHub releases: pagination did not terminate")
}

func releasePageEndpoint(endpoint string, page int) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("per_page", strconv.Itoa(githubReleasesPerPage))
	query.Del("page")
	if page > 1 {
		query.Set("page", strconv.Itoa(page))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (client *Client) fetchPage(ctx context.Context, endpoint string) ([]Release, error) {
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
