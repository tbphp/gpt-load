package channel

import (
	"encoding/json"
	"fmt"
	"gpt-load/internal/config"
	"gpt-load/internal/httpclient"
	"gpt-load/internal/models"
	"net/url"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// channelConstructor defines the function signature for creating a new channel proxy.
type channelConstructor func(f *Factory, group *models.Group) (ChannelProxy, error)

var (
	// channelRegistry holds the mapping from channel type string to its constructor.
	channelRegistry = make(map[string]channelConstructor)
)

// Register adds a new channel constructor to the registry.
func Register(channelType string, constructor channelConstructor) {
	if _, exists := channelRegistry[channelType]; exists {
		panic(fmt.Sprintf("channel type '%s' is already registered", channelType))
	}
	channelRegistry[channelType] = constructor
}

// GetChannels returns a slice of all registered channel type names.
func GetChannels() []string {
	supportedTypes := make([]string, 0, len(channelRegistry))
	for t := range channelRegistry {
		supportedTypes = append(supportedTypes, t)
	}
	return supportedTypes
}

// Factory is responsible for creating channel proxies.
type Factory struct {
	settingsManager *config.SystemSettingsManager
	clientManager   *httpclient.HTTPClientManager
	channelCache    map[uint]ChannelProxy
	cacheLock       sync.Mutex
}

// NewFactory creates a new channel factory.
func NewFactory(settingsManager *config.SystemSettingsManager, clientManager *httpclient.HTTPClientManager) *Factory {
	return &Factory{
		settingsManager: settingsManager,
		clientManager:   clientManager,
		channelCache:    make(map[uint]ChannelProxy),
	}
}

// GetChannel returns a channel proxy based on the group's channel type.
func (f *Factory) GetChannel(group *models.Group) (ChannelProxy, error) {
	f.cacheLock.Lock()
	defer f.cacheLock.Unlock()

	if channel, ok := f.channelCache[group.ID]; ok {
		if !channel.IsConfigStale(group) {
			return channel, nil
		}
	}

	logrus.Debugf("Creating new channel for group %d with type '%s'", group.ID, group.ChannelType)

	constructor, ok := channelRegistry[group.ChannelType]
	if !ok {
		return nil, fmt.Errorf("unsupported channel type: %s", group.ChannelType)
	}
	channel, err := constructor(f, group)
	if err != nil {
		return nil, err
	}
	f.channelCache[group.ID] = channel
	return channel, nil
}

// newBaseChannel is a helper function to create and configure a BaseChannel.
func (f *Factory) newBaseChannel(name string, group *models.Group) (*BaseChannel, error) {
	type upstreamDef struct {
		URL    string `json:"url"`
		Weight int    `json:"weight"`
	}

	var defs []upstreamDef
	if err := json.Unmarshal(group.Upstreams, &defs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal upstreams for %s channel: %w", name, err)
	}

	if len(defs) == 0 {
		return nil, fmt.Errorf("at least one upstream is required for %s channel", name)
	}

	var upstreamInfos []UpstreamInfo
	for _, def := range defs {
		u, err := url.Parse(def.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse upstream url '%s' for %s channel: %w", def.URL, name, err)
		}
		weight := def.Weight
		if weight <= 0 {
			weight = 1
		}
		upstreamInfos = append(upstreamInfos, UpstreamInfo{URL: u, Weight: weight})
	}

	// Base configuration for regular requests, derived from the group's effective settings.
	clientConfig := &httpclient.Config{
		ConnectTimeout:        time.Duration(group.EffectiveConfig.ConnectTimeout) * time.Second,
		RequestTimeout:        time.Duration(group.EffectiveConfig.RequestTimeout) * time.Second,
		IdleConnTimeout:       time.Duration(group.EffectiveConfig.IdleConnTimeout) * time.Second,
		MaxIdleConns:          group.EffectiveConfig.MaxIdleConns,
		MaxIdleConnsPerHost:   group.EffectiveConfig.MaxIdleConnsPerHost,
		ResponseHeaderTimeout: time.Duration(group.EffectiveConfig.ResponseHeaderTimeout) * time.Second,
		ProxyURL:              group.EffectiveConfig.ProxyURL,
		DisableCompression:    false,
		WriteBufferSize:       32 * 1024,
		ReadBufferSize:        32 * 1024,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// Create a dedicated configuration for streaming requests.
	streamConfig := *clientConfig
	streamConfig.RequestTimeout = 0
	streamConfig.DisableCompression = true
	streamConfig.WriteBufferSize = 0
	streamConfig.ReadBufferSize = 0
	// Use a larger, independent connection pool for streaming clients to avoid exhaustion.
	streamConfig.MaxIdleConns = max(group.EffectiveConfig.MaxIdleConns*2, 50)
	streamConfig.MaxIdleConnsPerHost = max(group.EffectiveConfig.MaxIdleConnsPerHost*2, 20)

	// Get both clients from the manager using their respective configurations.
	httpClient := f.clientManager.GetClient(clientConfig)
	streamClient := f.clientManager.GetClient(&streamConfig)

	return &BaseChannel{
		Name:               name,
		Upstreams:          upstreamInfos,
		HTTPClient:         httpClient,
		StreamClient:       streamClient,
		TestModel:          group.TestModel,
		ValidationEndpoint: group.ValidationEndpoint,
		channelType:        group.ChannelType,
		groupUpstreams:     group.Upstreams,
		effectiveConfig:    &group.EffectiveConfig,
	}, nil
}
