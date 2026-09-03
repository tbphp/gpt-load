package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	kiroDefaultPollInterval = 5 * time.Second
	kiroClientID            = "kiro-cli"
)

// kiroValidTokenEndpointHosts is the allowlist of hostnames permitted for the
// Kiro OAuth token endpoint. Stored token_endpoint values are only honored
// when their hostname appears here; anything else is rejected to prevent SSRF
// via a tampered credential field.
var kiroValidTokenEndpointHosts = map[string]bool{
	"prod.us-east-1.auth.desktop.kiro.dev": true,
	"prod.eu-west-1.auth.desktop.kiro.dev": true,
	"prod.us-west-2.auth.desktop.kiro.dev": true,
}

// kiroDeviceAuthorizationRequest is the body sent to the Kiro device
// authorization endpoint.
type kiroDeviceAuthorizationRequest struct {
	ClientID      string `json:"clientId"`
	LoginProvider string `json:"loginProvider"`
}

// kiroDeviceAuthorizationResponse is the Kiro device authorization response.
type kiroDeviceAuthorizationResponse struct {
	DeviceCode              string `json:"deviceCode"`
	ExpiresInMilliseconds   int64  `json:"expiresInMilliseconds"`
	IntervalInMilliseconds  int64  `json:"intervalInMilliseconds"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
}

// kiroDevicePollResponse is the Kiro device poll response.
type kiroDevicePollResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
	ProfileARN   string `json:"profileArn"`
	Error        string `json:"error"`
}

// kiroRefreshResponse is the Kiro token refresh response.
type kiroRefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
	ProfileARN   string `json:"profileArn"`
	Error        string `json:"error"`
}

// kiroImportFile is the flexible import shape accepted from credential files.
type kiroImportFile struct {
	Type          string `json:"type"`
	AuthKind      string `json:"auth_kind"`
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	TokenType     string `json:"token_type"`
	ExpiresIn     int    `json:"expires_in"`
	Expire        string `json:"expired"`
	LastRefresh   string `json:"last_refresh"`
	AccountID     string `json:"account_id"`
	Email         string `json:"email"`
	Region        string `json:"region"`
	ProfileARN    string `json:"profile_arn"`
	TokenEndpoint string `json:"token_endpoint"`
}

// BeginKiroDeviceAuthorization begins a Kiro device authorization flow for the
// given social login provider (e.g. "Google", "Github").
func BeginKiroDeviceAuthorization(ctx context.Context, options KiroOptions, loginProvider string) (KiroDeviceAuthorization, error) {
	region := strings.TrimSpace(options.Region)
	if region == "" {
		region = DefaultKiroRegion
	}
	region, err := validateKiroRegion(region)
	if err != nil {
		return KiroDeviceAuthorization{}, err
	}
	loginProvider = strings.TrimSpace(loginProvider)
	if loginProvider == "" {
		loginProvider = "Google"
	}
	authHost, err := KiroAuthURL(region)
	if err != nil {
		return KiroDeviceAuthorization{}, err
	}
	body, err := json.Marshal(kiroDeviceAuthorizationRequest{ClientID: kiroClientID, LoginProvider: loginProvider})
	if err != nil {
		return KiroDeviceAuthorization{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, authHost+"oauth/device/authorization", strings.NewReader(string(body)))
	if err != nil {
		return KiroDeviceAuthorization{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := kiroHTTPClient(options).Do(request)
	if err != nil {
		return KiroDeviceAuthorization{}, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponse+1))
	if err != nil {
		return KiroDeviceAuthorization{}, err
	}
	defer clear(raw)
	if len(raw) > maxTokenResponse {
		return KiroDeviceAuthorization{}, fmt.Errorf("Kiro device authorization response is too large")
	}
	if response.StatusCode != http.StatusOK {
		return KiroDeviceAuthorization{}, &KiroUpstreamHTTPError{Operation: "device authorization", StatusCode: response.StatusCode}
	}
	var payload kiroDeviceAuthorizationResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return KiroDeviceAuthorization{}, fmt.Errorf("decode Kiro device authorization: %w", err)
	}
	verificationURL := strings.TrimSpace(payload.VerificationURIComplete)
	if verificationURL == "" {
		verificationURL = strings.TrimSpace(payload.VerificationURI)
	}
	if strings.TrimSpace(payload.DeviceCode) == "" || strings.TrimSpace(payload.UserCode) == "" ||
		verificationURL == "" || payload.ExpiresInMilliseconds <= 0 {
		return KiroDeviceAuthorization{}, fmt.Errorf("Kiro device authorization response is incomplete")
	}
	interval := time.Duration(payload.IntervalInMilliseconds) * time.Millisecond
	if interval < kiroDefaultPollInterval {
		interval = kiroDefaultPollInterval
	}
	if interval > time.Minute {
		interval = time.Minute
	}
	expiresIn := time.Duration(payload.ExpiresInMilliseconds) * time.Millisecond
	if expiresIn > 15*time.Minute {
		expiresIn = 15 * time.Minute
	}
	state := KiroDeviceState{
		DeviceCode:          strings.TrimSpace(payload.DeviceCode),
		TokenEndpoint:       authHost + "oauth/device/poll",
		PollIntervalSeconds: int(interval / time.Second),
		LoginProvider:       loginProvider,
	}
	return KiroDeviceAuthorization{
		VerificationURL: verificationURL, UserCode: strings.TrimSpace(payload.UserCode),
		State: state, ExpiresAt: kiroNow(options).Add(expiresIn), PollInterval: interval,
	}, nil
}

// PollKiroDeviceAuthorizationOnce polls the Kiro device authorization state.
func PollKiroDeviceAuthorizationOnce(ctx context.Context, state KiroDeviceState, options KiroOptions) (KiroDevicePoll, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return KiroDevicePoll{}, err
	}
	decoded, err := decodeKiroDeviceState(raw)
	clear(raw)
	if err != nil {
		return KiroDevicePoll{}, err
	}
	pollBody, err := json.Marshal(map[string]string{"deviceCode": decoded.DeviceCode})
	if err != nil {
		return KiroDevicePoll{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, decoded.TokenEndpoint, strings.NewReader(string(pollBody)))
	if err != nil {
		return KiroDevicePoll{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := kiroHTTPClient(options).Do(request)
	if err != nil {
		return KiroDevicePoll{}, fmt.Errorf("poll Kiro device authorization: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	rawBody, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponse+1))
	if err != nil {
		return KiroDevicePoll{}, err
	}
	defer clear(rawBody)
	if len(rawBody) > maxTokenResponse {
		return KiroDevicePoll{}, fmt.Errorf("Kiro device poll response is too large")
	}
	var payload kiroDevicePollResponse
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return KiroDevicePoll{}, fmt.Errorf("decode Kiro device poll response: %w", err)
	}
	interval := time.Duration(decoded.PollIntervalSeconds) * time.Second
	code := strings.ToLower(strings.TrimSpace(payload.Error))
	if code != "" || response.StatusCode == http.StatusBadRequest {
		switch code {
		case "authorization_pending", "":
			return KiroDevicePoll{Status: KiroDevicePending, State: decoded, PollInterval: interval}, nil
		case "access_denied":
			return KiroDevicePoll{Status: KiroDeviceDenied, State: decoded}, nil
		case "expired_token":
			return KiroDevicePoll{Status: KiroDeviceExpired, State: decoded}, nil
		default:
			return KiroDevicePoll{}, &KiroTokenEndpointError{StatusCode: response.StatusCode, Code: code}
		}
	}
	if response.StatusCode != http.StatusOK {
		return KiroDevicePoll{}, &KiroTokenEndpointError{StatusCode: response.StatusCode}
	}
	region := strings.TrimSpace(options.Region)
	if region == "" {
		region = DefaultKiroRegion
	}
	credential := KiroCredential{
		Type: ProviderKiro, AuthKind: string(KiroAuthSocial),
		AccessToken: strings.TrimSpace(payload.AccessToken), RefreshToken: strings.TrimSpace(payload.RefreshToken),
		TokenType: "Bearer", ExpiresIn: int(payload.ExpiresIn), Region: region,
		ProfileARN: strings.TrimSpace(payload.ProfileARN),
	}
	now := kiroNow(options)
	if payload.ExpiresIn > 0 {
		credential.Expire = now.Add(time.Duration(payload.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	credential.LastRefresh = now.Format(time.RFC3339)
	normalizeKiroCredential(&credential)
	if strings.TrimSpace(credential.AccessToken) == "" || strings.TrimSpace(credential.RefreshToken) == "" {
		return KiroDevicePoll{}, fmt.Errorf("Kiro device poll returned incomplete credential")
	}
	return KiroDevicePoll{Status: KiroDeviceAuthorized, State: decoded, Credential: credential}, nil
}

// ImportKiroCredential imports a Kiro credential from a JSON payload.
func ImportKiroCredential(ctx context.Context, raw []byte, options KiroOptions) (KiroCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return KiroCredential{}, fmt.Errorf("credential JSON size is invalid")
	}
	allowed := kiroCanonicalFields()
	allowCPAAuthFileControlFields(allowed)
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return KiroCredential{}, err
	}
	if err := validateCPAAuthFileControlMetadata(raw); err != nil {
		return KiroCredential{}, err
	}
	var imported kiroImportFile
	if err := json.Unmarshal(raw, &imported); err != nil {
		return KiroCredential{}, fmt.Errorf("decode Kiro credential: %w", err)
	}
	imported.Type = strings.ToLower(strings.TrimSpace(imported.Type))
	if imported.Type != ProviderKiro {
		return KiroCredential{}, fmt.Errorf("credential type must be kiro")
	}
	kind := strings.ToLower(strings.TrimSpace(imported.AuthKind))
	if kind == "" {
		kind = string(KiroAuthSocial)
	}
	if kind != string(KiroAuthAPIKey) && kind != string(KiroAuthSocial) && kind != string(KiroAuthOIDC) {
		return KiroCredential{}, fmt.Errorf("Kiro credential auth_kind is unsupported")
	}
	region := strings.TrimSpace(imported.Region)
	if region == "" {
		region = strings.TrimSpace(options.Region)
	}
	if region == "" {
		region = DefaultKiroRegion
	}
	if _, err := validateKiroRegion(region); err != nil {
		return KiroCredential{}, err
	}
	candidate := KiroCredential{
		Type: ProviderKiro, AuthKind: kind,
		AccessToken: imported.AccessToken, RefreshToken: imported.RefreshToken,
		TokenType: imported.TokenType, ExpiresIn: imported.ExpiresIn,
		Expire: imported.Expire, LastRefresh: imported.LastRefresh,
		AccountID: imported.AccountID, Email: imported.Email,
		Region: region, ProfileARN: imported.ProfileARN,
		TokenEndpoint: imported.TokenEndpoint,
	}
	normalizeKiroCredential(&candidate)
	if err := validateKiroCredentialWithOptions(candidate, options); err != nil {
		return KiroCredential{}, err
	}
	if kind == string(KiroAuthSocial) {
		if expiresAt, ok := KiroCredentialExpiresAt(candidate); !ok || !expiresAt.After(kiroNow(options).Add(5*time.Minute)) {
			refreshed, refreshErr := refreshKiroCredentialOnce(ctx, candidate, options)
			if refreshErr != nil {
				return KiroCredential{}, refreshErr
			}
			candidate = refreshed
		}
	}
	return candidate, nil
}

// RefreshKiroCredentialOnce refreshes a Kiro OAuth credential.
func RefreshKiroCredentialOnce(ctx context.Context, current KiroCredential, options KiroOptions) (KiroCredential, error) {
	normalizeKiroCredential(&current)
	if err := validateKiroCredentialWithOptions(current, options); err != nil {
		return KiroCredential{}, err
	}
	if KiroAuthKind(current.AuthKind) != KiroAuthSocial {
		return KiroCredential{}, fmt.Errorf("Kiro credential auth_kind does not support refresh")
	}
	return refreshKiroCredentialOnce(ctx, current, options)
}

func refreshKiroCredentialOnce(ctx context.Context, current KiroCredential, options KiroOptions) (KiroCredential, error) {
	if strings.TrimSpace(current.RefreshToken) == "" {
		return KiroCredential{}, fmt.Errorf("Kiro refresh token is required")
	}
	region := strings.TrimSpace(current.Region)
	if region == "" {
		region = strings.TrimSpace(options.Region)
	}
	if region == "" {
		region = DefaultKiroRegion
	}
	tokenEndpoint := strings.TrimSpace(current.TokenEndpoint)
	if tokenEndpoint == "" {
		authHost, err := KiroAuthURL(region)
		if err != nil {
			return KiroCredential{}, err
		}
		tokenEndpoint = authHost + "refreshToken"
	}
	// SECURITY: a stored token_endpoint must resolve to a Kiro-auth allowlisted
	// host, otherwise we refuse to call it. This prevents an SSRF where a
	// tampered credential field redirects the token refresh to an arbitrary
	// internal or external host. When no safe stored endpoint is available we
	// fall back to the hardcoded per-region default.
	parsed, parseErr := url.Parse(tokenEndpoint)
	if parseErr != nil || !kiroValidTokenEndpointHosts[parsed.Hostname()] {
		authHost, err := KiroAuthURL(region)
		if err != nil {
			return KiroCredential{}, err
		}
		tokenEndpoint = authHost + "refreshToken"
	}
	body, err := json.Marshal(map[string]string{"refreshToken": strings.TrimSpace(current.RefreshToken)})
	if err != nil {
		return KiroCredential{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return KiroCredential{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := kiroHTTPClient(options).Do(request)
	if err != nil {
		return KiroCredential{}, fmt.Errorf("refresh Kiro credential: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponse+1))
	if err != nil {
		return KiroCredential{}, err
	}
	defer clear(raw)
	if len(raw) > maxTokenResponse {
		return KiroCredential{}, fmt.Errorf("Kiro refresh response is too large")
	}
	var token kiroRefreshResponse
	if response.StatusCode != http.StatusOK {
		_ = json.Unmarshal(raw, &token)
		return KiroCredential{}, &KiroTokenEndpointError{
			StatusCode: response.StatusCode, Code: strings.TrimSpace(token.Error),
		}
	}
	if err := json.Unmarshal(raw, &token); err != nil {
		return KiroCredential{}, fmt.Errorf("decode Kiro refresh response: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return KiroCredential{}, fmt.Errorf("Kiro refresh response has no access token")
	}
	now := kiroNow(options)
	refreshed := current
	refreshed.AccessToken = strings.TrimSpace(token.AccessToken)
	if strings.TrimSpace(token.RefreshToken) != "" {
		refreshed.RefreshToken = strings.TrimSpace(token.RefreshToken)
	}
	if token.ExpiresIn > 0 {
		refreshed.ExpiresIn = int(token.ExpiresIn)
		refreshed.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if strings.TrimSpace(refreshed.ProfileARN) == "" && strings.TrimSpace(token.ProfileARN) != "" {
		refreshed.ProfileARN = strings.TrimSpace(token.ProfileARN)
	}
	refreshed.LastRefresh = now.Format(time.RFC3339)
	if err := validateKiroCredentialWithOptions(refreshed, options); err != nil {
		return KiroCredential{}, err
	}
	return refreshed, nil
}

// kiroDefaultTokenEndpoint returns the default Kiro token endpoint for a region.
func kiroDefaultTokenEndpoint(region string) (string, error) {
	authHost, err := KiroAuthURL(region)
	if err != nil {
		return "", err
	}
	return authHost + "refreshToken", nil
}

var _ = errors.Is
var _ = url.Values{}
var _ = context.Canceled
