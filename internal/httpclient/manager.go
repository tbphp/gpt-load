package httpclient

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Config defines the parameters for creating an HTTP client.
// This struct is used to generate a unique fingerprint for client reuse.
type Config struct {
	ConnectTimeout        time.Duration
	RequestTimeout        time.Duration
	IdleConnTimeout       time.Duration
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	ResponseHeaderTimeout time.Duration
	DisableCompression    bool
	WriteBufferSize       int
	ReadBufferSize        int
	ForceAttemptHTTP2     bool
	TLSHandshakeTimeout   time.Duration
	ExpectContinueTimeout time.Duration
	ProxyURL              string
}

// HTTPClientManager manages the lifecycle of HTTP clients.
// It creates and caches clients based on their configuration fingerprint,
// ensuring that clients with the same configuration are reused.
type HTTPClientManager struct {
	clients map[string]*http.Client
	lock    sync.RWMutex
}

// NewHTTPClientManager creates a new client manager.
func NewHTTPClientManager() *HTTPClientManager {
	return &HTTPClientManager{
		clients: make(map[string]*http.Client),
	}
}

// GetClient returns an HTTP client that matches the given configuration.
// If a matching client already exists in the cache, it is returned.
// Otherwise, a new client is created, cached, and returned.
func (m *HTTPClientManager) GetClient(config *Config) *http.Client {
	fingerprint := config.getFingerprint()

	// Fast path with read lock
	m.lock.RLock()
	client, exists := m.clients[fingerprint]
	m.lock.RUnlock()
	if exists {
		return client
	}

	// Slow path with write lock
	m.lock.Lock()
	defer m.lock.Unlock()

	// Double-check in case another goroutine created the client while we were waiting for the lock.
	if client, exists = m.clients[fingerprint]; exists {
		return client
	}

	// Create a new transport and client with the specified configuration.
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   config.ConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     config.ForceAttemptHTTP2,
		MaxIdleConns:          config.MaxIdleConns,
		MaxIdleConnsPerHost:   config.MaxIdleConnsPerHost,
		IdleConnTimeout:       config.IdleConnTimeout,
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ExpectContinueTimeout: config.ExpectContinueTimeout,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,
		DisableCompression:    config.DisableCompression,
		WriteBufferSize:       config.WriteBufferSize,
		ReadBufferSize:        config.ReadBufferSize,
	}

	// Set http proxy.
	if config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			logrus.Warnf("Invalid proxy URL '%s' provided, falling back to environment settings: %v", config.ProxyURL, err)
			transport.Proxy = http.ProxyFromEnvironment
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}

	newClient := &http.Client{
		Transport: transport,
		Timeout:   config.RequestTimeout,
	}

	m.clients[fingerprint] = newClient
	return newClient
}

// getFingerprint generates a unique string representation of the client configuration.
func (c *Config) getFingerprint() string {
	return fmt.Sprintf(
		"ct:%.0fs|rt:%.0fs|it:%.0fs|mic:%d|mich:%d|rht:%.0fs|dc:%t|wbs:%d|rbs:%d|fh2:%t|tlst:%.0fs|ect:%.0fs|proxy:%s",
		c.ConnectTimeout.Seconds(),
		c.RequestTimeout.Seconds(),
		c.IdleConnTimeout.Seconds(),
		c.MaxIdleConns,
		c.MaxIdleConnsPerHost,
		c.ResponseHeaderTimeout.Seconds(),
		c.DisableCompression,
		c.WriteBufferSize,
		c.ReadBufferSize,
		c.ForceAttemptHTTP2,
		c.TLSHandshakeTimeout.Seconds(),
		c.ExpectContinueTimeout.Seconds(),
		c.ProxyURL,
	)
}
