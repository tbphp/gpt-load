package channel

import (
	"bytes"
	"fmt"
	"gpt-load/internal/models"
	"gpt-load/internal/types"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"

	"gorm.io/datatypes"
)

// UpstreamInfo holds the information for a single upstream server, including its weight.
type UpstreamInfo struct {
	ID            string
	URL           *url.URL
	Weight        int
	CurrentWeight int
}

// BaseChannel provides common functionality for channel proxies.
type BaseChannel struct {
	Name         string
	Upstreams    []UpstreamInfo
	HTTPClient   *http.Client
	StreamClient *http.Client
	TestModel    string
	upstreamLock sync.Mutex

	// Cached fields from the group for stale check
	channelType     string
	groupUpstreams  datatypes.JSON
	effectiveConfig *types.SystemSettings
}

// getUpstreamInfo selects an upstream using a smooth weighted round-robin algorithm.
func (b *BaseChannel) getUpstreamInfo() *UpstreamInfo {
	b.upstreamLock.Lock()
	defer b.upstreamLock.Unlock()

	if len(b.Upstreams) == 0 {
		return nil
	}
	if len(b.Upstreams) == 1 {
		return &b.Upstreams[0]
	}

	totalWeight := 0
	var best *UpstreamInfo

	for i := range b.Upstreams {
		up := &b.Upstreams[i]
		totalWeight += up.Weight
		up.CurrentWeight += up.Weight

		if best == nil || up.CurrentWeight > best.CurrentWeight {
			best = up
		}
	}

	if best == nil {
		return &b.Upstreams[0] // 降级到第一个可用的
	}

	best.CurrentWeight -= totalWeight
	return best
}

// getUpstreamURL selects an upstream URL using a smooth weighted round-robin algorithm.
func (b *BaseChannel) getUpstreamURL() *url.URL {
	upstream := b.getUpstreamInfo()
	if upstream == nil {
		return nil
	}
	return upstream.URL
}

// BuildUpstreamURL constructs the target URL for the upstream service.
func (b *BaseChannel) BuildUpstreamURL(originalURL *url.URL, group *models.Group) (string, error) {
	base := b.getUpstreamURL()
	if base == nil {
		return "", fmt.Errorf("no upstream URL configured for channel %s", b.Name)
	}

	finalURL := *base
	proxyPrefix := "/proxy/" + group.Name
	requestPath := originalURL.Path
	requestPath = strings.TrimPrefix(requestPath, proxyPrefix)

	finalURL.Path = strings.TrimRight(finalURL.Path, "/") + requestPath

	finalURL.RawQuery = originalURL.RawQuery

	return finalURL.String(), nil
}

// GetSelectedUpstreamID returns the ID of the currently selected upstream.
func (b *BaseChannel) GetSelectedUpstreamID() string {
	upstream := b.getUpstreamInfo()
	if upstream == nil {
		return "Default"
	}
	return upstream.ID
}

// IsConfigStale checks if the channel's configuration is stale compared to the provided group.
func (b *BaseChannel) IsConfigStale(group *models.Group) bool {
	if b.channelType != group.ChannelType {
		return true
	}
	if b.TestModel != group.TestModel {
		return true
	}
	if !bytes.Equal(b.groupUpstreams, group.Upstreams) {
		return true
	}
	if !reflect.DeepEqual(b.effectiveConfig, &group.EffectiveConfig) {
		return true
	}
	return false
}

// GetHTTPClient returns the client for standard requests.
func (b *BaseChannel) GetHTTPClient() *http.Client {
	return b.HTTPClient
}

// GetStreamClient returns the client for streaming requests.
func (b *BaseChannel) GetStreamClient() *http.Client {
	return b.StreamClient
}
