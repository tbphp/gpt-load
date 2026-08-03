package catalog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"gpt-load/internal/platform/httpclient"
)

const (
	modelsDevEndpoint   = "https://models.dev/api.json"
	maxCatalogBodyBytes = int64(32 * 1024 * 1024)
)

// Client conditionally downloads and validates the fixed Models.dev catalog.
type Client struct {
	httpClient *http.Client
	endpoint   string
	now        func() time.Time
}

// NewClient constructs the production Models.dev client from the shared HTTP
// client manager. The endpoint is intentionally not an argument.
func NewClient(manager *httpclient.HTTPClientManager, proxyURL string) *Client {
	if manager == nil {
		manager = httpclient.NewHTTPClientManager()
	}
	managedClient := manager.GetClient(&httpclient.Config{
		ConnectTimeout:        15 * time.Second,
		RequestTimeout:        30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   2,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    false,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ProxyURL:              proxyURL,
	})
	return newClientForTest(managedClient, modelsDevEndpoint, time.Now)
}

func newClientForTest(httpClient *http.Client, endpoint string, now func() time.Time) *Client {
	return &Client{httpClient: httpClient, endpoint: endpoint, now: now}
}

// Sync performs one conditional fetch. It never publishes or writes cache
// state; a 200 result is returned only after the raw document fully validates.
func (client *Client) Sync(ctx context.Context, previous Metadata) (SyncResult, error) {
	if client == nil || client.httpClient == nil || client.endpoint == "" || client.now == nil {
		return SyncResult{}, fmt.Errorf("catalog client is not initialized")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint, nil)
	if err != nil {
		return SyncResult{}, fmt.Errorf("create Models.dev request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if previous.ETag != "" {
		request.Header.Set("If-None-Match", previous.ETag)
	}
	if previous.LastModified != "" {
		request.Header.Set("If-Modified-Since", previous.LastModified)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return SyncResult{}, fmt.Errorf("request Models.dev catalog: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	checkedAt := client.now().UnixMilli()
	switch response.StatusCode {
	case http.StatusNotModified:
		metadata := previous
		metadata.CheckedAtMillis = checkedAt
		return SyncResult{Metadata: metadata, NotModified: true}, nil
	case http.StatusOK:
	default:
		return SyncResult{}, fmt.Errorf("Models.dev catalog returned HTTP %d", response.StatusCode)
	}

	if response.ContentLength > maxCatalogBodyBytes {
		return SyncResult{}, fmt.Errorf("Models.dev catalog exceeds 32 MiB limit")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogBodyBytes+1))
	if err != nil {
		return SyncResult{}, fmt.Errorf("read Models.dev catalog: %w", err)
	}
	if int64(len(raw)) > maxCatalogBodyBytes {
		return SyncResult{}, fmt.Errorf("Models.dev catalog exceeds 32 MiB limit")
	}
	snapshot, err := Parse(bytes.NewReader(raw))
	if err != nil {
		return SyncResult{}, fmt.Errorf("parse Models.dev catalog: %w", err)
	}
	metadata := Metadata{
		ETag:                    response.Header.Get("ETag"),
		LastModified:            response.Header.Get("Last-Modified"),
		CheckedAtMillis:         checkedAt,
		SuccessfulFetchAtMillis: checkedAt,
	}
	if err := validateMetadata(metadata, true); err != nil {
		return SyncResult{}, err
	}
	return SyncResult{
		Metadata: metadata,
		RawJSON:  append([]byte(nil), raw...),
		Snapshot: snapshot,
	}, nil
}

func validateMetadata(metadata Metadata, requireSuccessfulFetch bool) error {
	if requireSuccessfulFetch && metadata.SuccessfulFetchAtMillis <= 0 {
		return fmt.Errorf("successful fetch timestamp must be positive")
	}
	if metadata.CheckedAtMillis < 0 {
		return fmt.Errorf("checked timestamp must be non-negative")
	}
	for name, value := range map[string]string{
		"ETag":          metadata.ETag,
		"Last-Modified": metadata.LastModified,
	} {
		if len(value) > 8*1024 {
			return fmt.Errorf("%s validator is too large", name)
		}
		for _, character := range value {
			if unicode.IsControl(character) || strings.ContainsRune("\r\n", character) {
				return fmt.Errorf("%s validator contains control characters", name)
			}
		}
	}
	return nil
}
