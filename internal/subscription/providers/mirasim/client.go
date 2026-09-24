package mirasim

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const (
	sessionPath              = "/v1/device/session"
	modelsPath               = "/v1/models"
	limitsPath               = "/v1/limits"
	accessStaleLead          = 30 * time.Second
	ticketRefreshLead        = 2 * time.Minute
	ticketDefaultTTL         = 10 * time.Minute
	ticketBackoffBase        = time.Second
	ticketBackoffMax         = 30 * time.Second
	ticketRetryMax           = 15 * time.Minute
	ticketRefusalFloor       = 30 * time.Second
	ticketRouteAbsentQuiet   = time.Minute
	ticketUnimplementedQuiet = 15 * time.Minute
	maxErrorBody             = 1 << 20
	maxErrorMessage          = 4 << 10
	claudeOAuthBeta          = "oauth-2025-04-20"
)

// RelayOptions carries the small set of inference metadata the official client
// attaches. HTTP/1.1 is enforced on the transport. Lower-case header spelling
// is intentionally not rewritten here: Go canonicalizes names, and the
// signature does not depend on the on-wire case.
// ponytail: a byte-for-byte client needs a RoundTripper that rewrites the
// request line; do not add that until a relay rejects canonical headers.
type RelayOptions struct {
	Collect *bool
	Locale  string
}

type requestIdentityKey struct{}

type requestIdentity struct{ session, turn string }

func safeMetadata(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return value
}

type Client struct {
	options               RelayOptions
	storage               Storage
	mu                    sync.Mutex
	loaded                bool
	accessToken           string
	relayAccountID        string
	refreshToken          string
	accessExpiresAt       time.Time
	privateKey            ed25519.PrivateKey
	publicKeyBase64       string
	deviceID              string
	sessionID             string
	ticket                string
	ticketExpiresAt       time.Time
	ticketRetryAt         time.Time
	ticketFailures        int
	ticketLastError       error
	ticketRefusedUntil    time.Time
	ticketUnmintableUntil time.Time
	refreshRequired       bool
	now                   func() time.Time
}

func NewClient(storage Storage) *Client {
	storage.applyDefaults()
	return &Client{storage: storage, now: time.Now}
}

func (c *Client) Storage() Storage {
	if c == nil {
		return Storage{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	storage := c.storage
	storage.PlanExpiresAt = cloneInt64(c.storage.PlanExpiresAt)
	return storage
}

type HTTPResult struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func (c *Client) Do(ctx context.Context, method, requestPath string, query url.Values, headers http.Header, body []byte) (HTTPResult, error) {
	return c.do(ctx, method, requestPath, query, headers, body, false)
}

func (c *Client) doControl(ctx context.Context, method, requestPath string, headers http.Header) (HTTPResult, error) {
	return c.do(ctx, method, requestPath, nil, headers, nil, true)
}

func (c *Client) do(ctx context.Context, method, requestPath string, query url.Values, headers http.Header, body []byte, controlPlane bool) (HTTPResult, error) {
	endpoint, signaturePath, err := c.endpoint(requestPath, query)
	if err != nil {
		return HTTPResult{}, err
	}
	httpClient, err := relayHTTPClient(ctx)
	if err != nil {
		return HTTPResult{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		authHeaders, errAuth := c.authHeaders(ctx, httpClient, method, signaturePath, body, attempt > 0, controlPlane)
		if errAuth != nil {
			return HTTPResult{}, errAuth
		}
		outbound := prepareHeaders(headers, authHeaders, false)
		request, errRequest := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if errRequest != nil {
			return HTTPResult{}, errRequest
		}
		request.Header = outbound
		response, errDo := httpClient.Do(request)
		if errDo != nil {
			return HTTPResult{}, errDo
		}
		responseBody, errRead := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
		_ = response.Body.Close()
		if errRead != nil {
			return HTTPResult{}, errRead
		}
		if len(responseBody) > maxErrorBody {
			responseBody = responseBody[:maxErrorBody]
		}
		if response.StatusCode != http.StatusUnauthorized || attempt == 1 {
			if response.StatusCode == http.StatusUnauthorized {
				c.markAccessRefreshRequired()
			}
			return HTTPResult{StatusCode: response.StatusCode, Header: response.Header.Clone(), Body: responseBody}, nil
		}
	}
	return HTTPResult{}, fmt.Errorf("Mirasim request retry exhausted")
}

type StreamResult struct {
	StatusCode int
	Header     http.Header
	Chunks     <-chan []byte
	Err        <-chan error
}

func (c *Client) DoStream(ctx context.Context, method, requestPath string, query url.Values, headers http.Header, body []byte) (HTTPResult, io.ReadCloser, error) {
	endpoint, signaturePath, err := c.endpoint(requestPath, query)
	if err != nil {
		return HTTPResult{}, nil, err
	}
	httpClient, err := relayHTTPClient(ctx)
	if err != nil {
		return HTTPResult{}, nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		authHeaders, errAuth := c.authHeaders(ctx, httpClient, method, signaturePath, body, attempt > 0, false)
		if errAuth != nil {
			return HTTPResult{}, nil, errAuth
		}
		outbound := prepareHeaders(headers, authHeaders, true)
		request, errRequest := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if errRequest != nil {
			return HTTPResult{}, nil, errRequest
		}
		request.Header = outbound
		response, errDo := httpClient.Do(request)
		if errDo != nil {
			return HTTPResult{}, nil, errDo
		}
		if response.StatusCode != http.StatusUnauthorized || attempt == 1 {
			if response.StatusCode == http.StatusUnauthorized {
				c.markAccessRefreshRequired()
			}
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				responseBody, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
				_ = response.Body.Close()
				return HTTPResult{}, nil, NewStatusError(response.StatusCode, responseBody, response.Header)
			}
			return HTTPResult{StatusCode: response.StatusCode, Header: response.Header.Clone()}, response.Body, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBody))
		_ = response.Body.Close()
	}
	return HTTPResult{}, nil, fmt.Errorf("Mirasim stream retry exhausted")
}

func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	response, err := c.doControl(ctx, http.MethodGet, modelsPath, http.Header{"Accept": []string{"application/json"}})
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, NewStatusError(response.StatusCode, response.Body, response.Header)
	}
	return ParseModelIDs(response.Body)
}

func (c *Client) FetchLimits(ctx context.Context) (Limits, error) {
	response, err := c.doControl(ctx, http.MethodGet, limitsPath, http.Header{"Accept": []string{"application/json"}})
	if err != nil {
		return Limits{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Limits{}, NewStatusError(response.StatusCode, response.Body, response.Header)
	}
	return ParseLimits(response.Body)
}

func (c *Client) Refresh(ctx context.Context) (Storage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadLocked(); err != nil {
		return Storage{}, err
	}
	before := c.storage
	if err := c.refreshAccessLocked(ctx); err != nil {
		return Storage{}, err
	}
	if !matchesRefreshIdentity(before, c.storage) {
		return Storage{}, errIdentityChanged
	}
	return c.StorageUnlocked(), nil
}

func (c *Client) StorageUnlocked() Storage {
	storage := c.storage
	storage.PlanExpiresAt = cloneInt64(c.storage.PlanExpiresAt)
	return storage
}

var errIdentityChanged = errors.New("Mirasim credential identity changed")

func (c *Client) authHeaders(ctx context.Context, httpClient *http.Client, method, requestPath string, body []byte, forceTicket, controlPlane bool) (http.Header, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadLocked(); err != nil {
		return nil, err
	}
	if err := c.loadSignerLocked(); err != nil {
		return nil, err
	}
	if forceTicket {
		if err := c.refuseTicketLocked(); err != nil {
			return nil, err
		}
	}
	ticket, err := c.ticketLocked(ctx, httpClient)
	if err != nil {
		return nil, err
	}
	var metadata map[string]string
	if !controlPlane {
		relayMetadata, errMetadata := c.relayMetadataLocked(ctx, requestPath)
		if errMetadata != nil {
			return nil, errMetadata
		}
		metadata = relayMetadata
	}
	headers, err := c.signatureHeadersLocked(method, requestPath, ticket, metadata, body)
	if err != nil {
		return nil, err
	}
	headers.Set("Authorization", "Bearer "+ticket)
	if controlPlane {
		return headers, nil
	}
	if err := sealRelayHeaders(headers, method, requestPath); err != nil {
		return nil, err
	}
	return headers, nil
}

func (c *Client) ticketLocked(ctx context.Context, httpClient *http.Client) (string, error) {
	now := c.nowTime()
	if c.ticket != "" && now.Before(c.ticketExpiresAt.Add(-ticketRefreshLead)) {
		return c.ticket, nil
	}
	if now.Before(c.ticketUnmintableUntil) {
		return c.accessCredentialLocked()
	}
	if now.Before(c.ticketRetryAt) {
		if c.ticket != "" && now.Before(c.ticketExpiresAt) {
			return c.ticket, nil
		}
		return "", newTicketBackoffError(c.ticketLastError, c.ticketRetryAt.Sub(now))
	}
	if err := c.ensureAccessTokenLocked(); err != nil {
		return "", err
	}
	body, err := json.Marshal(struct {
		PublicKey string `json:"publicKey"`
		DeviceID  string `json:"deviceId"`
	}{PublicKey: c.publicKeyBase64, DeviceID: c.deviceID})
	if err != nil {
		return "", err
	}
	signed, err := c.signatureHeadersLocked(http.MethodPost, sessionPath, c.accessToken, nil, body)
	if err != nil {
		return "", err
	}
	signed.Set("Authorization", "Bearer "+c.accessToken)
	signed.Set("Content-Type", "application/json")
	endpoint, _, err := c.endpoint(sessionPath, nil)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header = signed
	response, err := httpClient.Do(request)
	if err != nil {
		errTicket := fmt.Errorf("mint Mirasim device ticket: %w", err)
		c.noteTicketFailureLocked(errTicket, nil, true)
		return c.staleTicketOrErrorLocked(now, errTicket)
	}
	responseBody, errRead := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
	_ = response.Body.Close()
	if errRead != nil {
		errTicket := fmt.Errorf("read Mirasim device ticket: %w", errRead)
		c.noteTicketFailureLocked(errTicket, nil, true)
		return c.staleTicketOrErrorLocked(now, errTicket)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		errStatus := NewStatusError(response.StatusCode, responseBody, response.Header)
		if quiet := ticketUnmintableWindow(response.StatusCode); quiet > 0 {
			c.resetTicketBackoffLocked()
			c.ticketUnmintableUntil = now.Add(quiet)
			return c.accessCredentialLocked()
		}
		if response.StatusCode == http.StatusUnauthorized {
			c.refreshRequired = true
		}
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError
		c.noteTicketFailureLocked(errStatus, response.Header, retryable)
		return c.staleTicketOrErrorLocked(now, errStatus)
	}
	var payload struct {
		Ticket    string   `json:"ticket"`
		ExpiresIn *float64 `json:"expiresIn"`
		ExpiresAt *float64 `json:"expiresAt"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil || strings.TrimSpace(payload.Ticket) == "" {
		errTicket := fmt.Errorf("Mirasim device ticket response is incomplete")
		c.noteTicketFailureLocked(errTicket, nil, false)
		return c.staleTicketOrErrorLocked(now, errTicket)
	}
	c.ticket = strings.TrimSpace(payload.Ticket)
	c.ticketExpiresAt = resolveTicketExpiry(now, payload.ExpiresIn, payload.ExpiresAt)
	c.resetTicketBackoffLocked()
	return c.ticket, nil
}

func (c *Client) accessCredentialLocked() (string, error) {
	if err := c.ensureAccessTokenLocked(); err != nil {
		return "", err
	}
	return c.accessToken, nil
}

func (c *Client) ensureAccessTokenLocked() error {
	now := c.nowTime()
	if c.accessToken == "" {
		c.refreshRequired = true
		return NewStatusError(http.StatusUnauthorized, []byte(`{"error":"Mirasim access token is missing"}`), nil)
	}
	if c.accessExpiresAt.IsZero() || now.Before(c.accessExpiresAt.Add(-accessStaleLead)) {
		return nil
	}
	c.refreshRequired = true
	return NewStatusError(http.StatusUnauthorized, []byte(`{"error":"Mirasim access token requires refresh"}`), nil)
}

func (c *Client) refreshAccessLocked(ctx context.Context) error {
	if strings.TrimSpace(c.refreshToken) == "" {
		return fmt.Errorf("Mirasim refresh token is missing")
	}
	body, err := json.Marshal(map[string]string{"refresh_token": c.refreshToken})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.storage.AdminURL+"/auth/refresh", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Mirasim token refresh request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	// Refresh uses its own client so the refresh token never rides the relay
	// transport that carries signed inference calls.
	authClient := &http.Client{Timeout: 60 * time.Second, Transport: http1Transport(nil)}
	response, err := authClient.Do(request)
	if err != nil {
		return newRefreshTransportError(err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
	if err != nil {
		return newRefreshTransportError(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return newRefreshHTTPError(response.StatusCode, response.Header, responseBody)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return newRefreshProtocolError("decode_token_response", err)
	}
	payload.AccessToken = strings.TrimSpace(payload.AccessToken)
	payload.RefreshToken = strings.TrimSpace(payload.RefreshToken)
	if payload.AccessToken == "" {
		return newRefreshProtocolError("missing_access_token", nil)
	}
	now := c.nowTime().UTC()
	c.accessToken = payload.AccessToken
	c.relayAccountID = AccessTokenAgentAccount(payload.AccessToken)
	c.storage.AccessToken = payload.AccessToken
	c.storage.PopulateIdentityFromAccessToken()
	if plan, planExpiresAt := AccessTokenPlan(payload.AccessToken); plan != "" && strings.TrimSpace(c.storage.Plan) == "" {
		c.storage.Plan = plan
		c.storage.PlanExpiresAt = planExpiresAt
	}
	c.storage.RecordTokenTiming(payload.AccessToken, payload.ExpiresIn, now)
	c.accessExpiresAt = c.storage.AccessTokenExpiry(now)
	if payload.RefreshToken != "" {
		c.refreshToken = payload.RefreshToken
		c.storage.RefreshToken = payload.RefreshToken
	}
	return nil
}

func (c *Client) loadLocked() error {
	if c.loaded {
		return nil
	}
	c.refreshToken = strings.TrimSpace(c.storage.RefreshToken)
	c.accessToken = strings.TrimSpace(c.storage.AccessToken)
	c.relayAccountID = AccessTokenAgentAccount(c.accessToken)
	c.accessExpiresAt = c.storage.AccessTokenExpiry(c.nowTime())
	c.loaded = true
	return nil
}

func (c *Client) loadSignerLocked() error {
	if len(c.privateKey) != 0 {
		return nil
	}
	block, _ := pem.Decode([]byte(strings.TrimSpace(c.storage.DevicePrivateKey)))
	if block == nil {
		return fmt.Errorf("decode Mirasim device private key PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse Mirasim device private key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return fmt.Errorf("Mirasim device private key is not Ed25519")
	}
	publicDER, err := x509.MarshalPKIXPublicKey(privateKey.Public())
	if err != nil {
		return fmt.Errorf("marshal Mirasim device public key: %w", err)
	}
	publicBase64 := base64.StdEncoding.EncodeToString(publicDER)
	digest := sha256.Sum256([]byte(publicBase64))
	c.privateKey = append(ed25519.PrivateKey(nil), privateKey...)
	c.publicKeyBase64 = publicBase64
	c.deviceID = base64.RawURLEncoding.EncodeToString(digest[:])[:22]
	return nil
}

func (c *Client) endpoint(requestPath string, query url.Values) (string, string, error) {
	base, err := url.Parse(c.storage.RelayURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", "", fmt.Errorf("invalid Mirasim relay URL %q", c.storage.RelayURL)
	}
	requestPath = "/" + strings.TrimLeft(strings.TrimSpace(requestPath), "/")
	base.Path = strings.TrimRight(base.Path, "/") + requestPath
	base.RawPath = ""
	if query != nil {
		base.RawQuery = query.Encode()
	}
	return base.String(), base.Path, nil
}

func relayHTTPClient(ctx context.Context) (*http.Client, error) {
	client, err := subscriptionruntime.HTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{}
	}
	base, _ := client.Transport.(*http.Transport)
	client.Transport = http1Transport(base)
	return client, nil
}

func http1Transport(base *http.Transport) *http.Transport {
	var transport *http.Transport
	switch {
	case base != nil:
		transport = base.Clone()
	default:
		if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok && defaultTransport != nil {
			transport = defaultTransport.Clone()
		} else {
			transport = &http.Transport{}
		}
	}
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	return transport
}

func prepareHeaders(source, auth http.Header, stream bool) http.Header {
	headers := cloneHeader(source)
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "x-mirasim-") {
			headers.Del(name)
		}
	}
	for _, name := range []string{
		"Authorization", "Proxy-Authorization", "X-Api-Key", "Host", "Content-Length",
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		headers.Del(name)
	}
	dropCommaSeparatedHeaderValue(headers, "Anthropic-Beta", claudeOAuthBeta)
	for key, values := range auth {
		headers[key] = append([]string(nil), values...)
	}
	if headers.Get("Content-Type") == "" && !stream {
		headers.Set("Content-Type", "application/json")
	}
	if stream {
		headers.Set("Accept", "text/event-stream")
	} else if headers.Get("Accept") == "" {
		headers.Set("Accept", "application/json")
	}
	return headers
}

func dropCommaSeparatedHeaderValue(headers http.Header, name, drop string) {
	for key, values := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		filtered := make([]string, 0, len(values))
		for _, value := range values {
			kept := make([]string, 0)
			for _, token := range strings.Split(value, ",") {
				token = strings.TrimSpace(token)
				if token != "" && token != drop {
					kept = append(kept, token)
				}
			}
			if len(kept) > 0 {
				filtered = append(filtered, strings.Join(kept, ","))
			}
		}
		if len(filtered) == 0 {
			delete(headers, key)
			continue
		}
		headers[key] = filtered
	}
}

func cloneHeader(source http.Header) http.Header {
	if source == nil {
		return make(http.Header)
	}
	out := make(http.Header, len(source))
	for key, values := range source {
		out[key] = append([]string(nil), values...)
	}
	return out
}

var datedModelSuffix = regexp.MustCompile(`-20\d{6}$`)

func ParseModelIDs(raw []byte) ([]string, error) {
	var payload struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode Mirasim model catalog: %w", err)
	}
	items := payload.Data
	if len(items) == 0 {
		items = payload.Models
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		var object struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item, &object); err != nil || strings.TrimSpace(object.ID) == "" {
			var id string
			if err := json.Unmarshal(item, &id); err != nil {
				continue
			}
			object.ID = id
		}
		id := strings.TrimSpace(object.ID)
		if id != "" {
			ids = append(ids, id)
		}
	}
	ids = servableModelIDs(ids)
	if len(ids) == 0 {
		return nil, fmt.Errorf("Mirasim model catalog contains no models")
	}
	return ids, nil
}

func servableModelIDs(parsed []string) []string {
	undated := make(map[string]struct{}, len(parsed))
	for _, id := range parsed {
		if !strings.Contains(id, "/") && !datedModelSuffix.MatchString(id) {
			undated[id] = struct{}{}
		}
	}
	models := make([]string, 0, len(parsed))
	seen := make(map[string]struct{}, len(parsed))
	for _, id := range parsed {
		if _, duplicate := seen[id]; duplicate || id == "*" || strings.Contains(id, "/") {
			continue
		}
		lower := strings.ToLower(id)
		if !strings.HasPrefix(lower, "claude-") && !strings.HasPrefix(lower, "gpt-") {
			continue
		}
		if datedModelSuffix.MatchString(id) {
			if _, twin := undated[datedModelSuffix.ReplaceAllString(id, "")]; twin {
				continue
			}
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	return models
}

type LimitWindow struct {
	Name        string
	Budget      float64
	Used        float64
	ResetAt     *time.Time
	ModelScoped bool
}

type Limits struct {
	Paid    *bool
	Windows []LimitWindow
}

func ParseLimits(raw []byte) (Limits, error) {
	var payload struct {
		Windows []struct {
			Name        string          `json:"name"`
			Budget      *float64        `json:"budget"`
			Used        *float64        `json:"used"`
			ResetAt     json.RawMessage `json:"reset_at"`
			ModelScoped bool            `json:"model_scoped"`
		} `json:"windows"`
		Paid *bool `json:"paid"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Limits{}, fmt.Errorf("decode Mirasim limits: %w", err)
	}
	limits := Limits{Paid: payload.Paid, Windows: make([]LimitWindow, 0, len(payload.Windows))}
	for _, window := range payload.Windows {
		name := strings.TrimSpace(window.Name)
		if name == "" || window.Budget == nil || window.Used == nil || *window.Budget < 0 {
			continue
		}
		limits.Windows = append(limits.Windows, LimitWindow{
			Name: name, Budget: *window.Budget, Used: *window.Used,
			ResetAt: resetTimeJSON(window.ResetAt), ModelScoped: window.ModelScoped,
		})
	}
	return limits, nil
}

func resetTimeJSON(raw json.RawMessage) *time.Time {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if parsed, ok := parseTimestamp(text); ok {
			return &parsed
		}
	}
	var seconds float64
	if err := json.Unmarshal(raw, &seconds); err == nil && seconds > 0 {
		parsed := time.Unix(int64(seconds), 0).UTC()
		return &parsed
	}
	return nil
}

type StatusError struct {
	status  int
	body    []byte
	headers http.Header
}

func NewStatusError(status int, body []byte, headers http.Header) *StatusError {
	if len(body) > maxErrorBody {
		body = body[:maxErrorBody]
	}
	return &StatusError{status: status, body: append([]byte(nil), body...), headers: cloneHeader(headers)}
}

func (e *StatusError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.status
}

func (e *StatusError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return refreshErrorCode(e.body)
}

func (e *StatusError) RetryAfter() *time.Duration {
	if e == nil {
		return nil
	}
	return parseRetryAfter(e.headers, time.Now())
}

func (e *StatusError) Error() string {
	if e == nil {
		return "Mirasim upstream request failed"
	}
	message := strings.TrimSpace(string(e.body))
	if message == "" {
		message = http.StatusText(e.status)
	}
	if len(message) > maxErrorMessage {
		message = message[:maxErrorMessage] + "..."
	}
	return fmt.Sprintf("Mirasim upstream returned HTTP %d: %s", e.status, message)
}

func (e *StatusError) Retryable() bool {
	return e != nil && (e.status == http.StatusTooManyRequests || e.status >= http.StatusInternalServerError)
}
