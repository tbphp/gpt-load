package embedded

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	xaiauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/xai"
)

const grokDefaultPollInterval = 5 * time.Second

type grokDiscovery struct {
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	UserInfoEndpoint            string `json:"userinfo_endpoint"`
}

type grokTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

type grokUserInfo struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

type grokImportFile struct {
	Type          string `json:"type"`
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	IDToken       string `json:"id_token"`
	TokenType     string `json:"token_type"`
	ExpiresIn     int    `json:"expires_in"`
	Expire        string `json:"expired"`
	LastRefresh   string `json:"last_refresh"`
	AccountID     string `json:"account_id"`
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	TokenEndpoint string `json:"token_endpoint"`
	BaseURL       string `json:"base_url"`
	RedirectURI   string `json:"redirect_uri"`
	AuthKind      string `json:"auth_kind"`
}

func beginGrokDeviceAuthorization(ctx context.Context, options GrokOptions) (GrokDeviceAuthorization, error) {
	discovery, err := discoverGrokOAuth(ctx, options)
	if err != nil {
		return GrokDeviceAuthorization{}, err
	}
	form := url.Values{"client_id": {xaiauth.ClientID}, "scope": {xaiauth.Scope}}
	body, status, err := doGrokForm(ctx, options, discovery.DeviceAuthorizationEndpoint, form)
	if err != nil {
		return GrokDeviceAuthorization{}, fmt.Errorf("begin Grok device authorization: %w", err)
	}
	defer clear(body)
	if status != http.StatusOK {
		return GrokDeviceAuthorization{}, &GrokUpstreamHTTPError{Operation: "device authorization", StatusCode: status}
	}
	var payload struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return GrokDeviceAuthorization{}, fmt.Errorf("decode Grok device authorization: %w", err)
	}
	verificationURL := strings.TrimSpace(payload.VerificationURIComplete)
	if verificationURL == "" {
		verificationURL = strings.TrimSpace(payload.VerificationURI)
	}
	if strings.TrimSpace(payload.DeviceCode) == "" || strings.TrimSpace(payload.UserCode) == "" ||
		verificationURL == "" || payload.ExpiresIn <= 0 {
		return GrokDeviceAuthorization{}, fmt.Errorf("Grok device authorization response is incomplete")
	}
	interval := time.Duration(payload.Interval) * time.Second
	if interval < grokDefaultPollInterval {
		interval = grokDefaultPollInterval
	}
	if interval > time.Minute {
		return GrokDeviceAuthorization{}, fmt.Errorf("Grok device authorization interval is invalid")
	}
	expiresIn := time.Duration(payload.ExpiresIn) * time.Second
	if expiresIn > xaiauth.MaxPollDuration {
		expiresIn = xaiauth.MaxPollDuration
	}
	return GrokDeviceAuthorization{
		VerificationURL: verificationURL,
		UserCode:        strings.TrimSpace(payload.UserCode),
		State: GrokDeviceState{
			DeviceCode: strings.TrimSpace(payload.DeviceCode), TokenEndpoint: discovery.TokenEndpoint,
			UserInfoEndpoint: discovery.UserInfoEndpoint, PollIntervalSeconds: int(interval / time.Second),
		},
		ExpiresAt: grokNow(options).Add(expiresIn), PollInterval: interval,
	}, nil
}

func pollGrokDeviceAuthorizationOnce(ctx context.Context, state GrokDeviceState, options GrokOptions) (GrokDevicePoll, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return GrokDevicePoll{}, err
	}
	state, err = decodeGrokDeviceState(raw)
	clear(raw)
	if err != nil {
		return GrokDevicePoll{}, err
	}
	form := url.Values{
		"grant_type": {xaiauth.DeviceCodeGrantType}, "device_code": {state.DeviceCode}, "client_id": {xaiauth.ClientID},
	}
	body, status, err := doGrokForm(ctx, options, state.TokenEndpoint, form)
	if err != nil {
		return GrokDevicePoll{}, fmt.Errorf("poll Grok device authorization: %w", err)
	}
	defer clear(body)
	var token grokTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return GrokDevicePoll{}, fmt.Errorf("decode Grok device token response: %w", err)
	}
	code := strings.ToLower(boundedGrokOAuthCode(token.Error))
	interval := time.Duration(state.PollIntervalSeconds) * time.Second
	if code != "" {
		switch code {
		case "authorization_pending":
			return GrokDevicePoll{Status: GrokDevicePending, State: state, PollInterval: interval}, nil
		case "slow_down":
			interval += grokDefaultPollInterval
			if interval > time.Minute {
				interval = time.Minute
			}
			state.PollIntervalSeconds = int(interval / time.Second)
			return GrokDevicePoll{Status: GrokDevicePending, State: state, PollInterval: interval}, nil
		case "access_denied":
			return GrokDevicePoll{Status: GrokDeviceDenied, State: state}, nil
		case "expired_token":
			return GrokDevicePoll{Status: GrokDeviceExpired, State: state}, nil
		default:
			return GrokDevicePoll{}, &GrokTokenEndpointError{StatusCode: status, Code: code}
		}
	}
	if status != http.StatusOK {
		return GrokDevicePoll{}, &GrokTokenEndpointError{StatusCode: status}
	}
	credential, err := grokCredentialFromToken(ctx, token, "", state.TokenEndpoint, state.UserInfoEndpoint, options, true)
	if err != nil {
		return GrokDevicePoll{}, err
	}
	return GrokDevicePoll{Status: GrokDeviceAuthorized, State: state, Credential: credential}, nil
}

func importGrokCredential(ctx context.Context, raw []byte, options GrokOptions) (GrokCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return GrokCredential{}, fmt.Errorf("credential JSON size is invalid")
	}
	allowed := grokCanonicalFields()
	for _, field := range []string{"sub", "base_url", "redirect_uri", "auth_kind"} {
		allowed[field] = struct{}{}
	}
	allowCPAAuthFileControlFields(allowed)
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return GrokCredential{}, err
	}
	if err := validateCPAAuthFileControlMetadata(raw); err != nil {
		return GrokCredential{}, err
	}
	var imported grokImportFile
	if err := json.Unmarshal(raw, &imported); err != nil {
		return GrokCredential{}, fmt.Errorf("decode Grok credential: %w", err)
	}
	imported.Type = strings.ToLower(strings.TrimSpace(imported.Type))
	if imported.Type != ProviderGrok && imported.Type != "xai" {
		return GrokCredential{}, fmt.Errorf("credential type must be grok or xai")
	}
	if imported.AuthKind != "" && !strings.EqualFold(strings.TrimSpace(imported.AuthKind), "oauth") {
		return GrokCredential{}, fmt.Errorf("credential auth_kind must be oauth")
	}
	if imported.TokenType != "" && !strings.EqualFold(strings.TrimSpace(imported.TokenType), "Bearer") {
		return GrokCredential{}, fmt.Errorf("credential token_type must be Bearer")
	}
	if imported.ExpiresIn < 0 {
		return GrokCredential{}, fmt.Errorf("credential expires_in is invalid")
	}
	if strings.TrimSpace(imported.AccountID) != "" && strings.TrimSpace(imported.Subject) != "" &&
		strings.TrimSpace(imported.AccountID) != strings.TrimSpace(imported.Subject) {
		return GrokCredential{}, ErrGrokCredentialIdentityChanged
	}
	if imported.BaseURL != "" {
		baseURL := strings.TrimRight(strings.TrimSpace(imported.BaseURL), "/")
		if baseURL != strings.TrimRight(xaiauth.DefaultAPIBaseURL, "/") && baseURL != strings.TrimRight(xaiauth.CLIChatProxyBaseURL, "/") {
			return GrokCredential{}, fmt.Errorf("credential base_url is not supported")
		}
	}
	if imported.RedirectURI != "" {
		if _, err := validateGrokOAuthEndpoint(imported.RedirectURI, "redirect_uri", false); err != nil {
			return GrokCredential{}, fmt.Errorf("credential redirect_uri is invalid")
		}
	}
	if strings.TrimSpace(imported.AccessToken) == "" || strings.TrimSpace(imported.RefreshToken) == "" {
		return GrokCredential{}, fmt.Errorf("credential tokens are required")
	}
	discovery, err := resolveGrokTokenAndUserInfo(ctx, imported.TokenEndpoint, options)
	if err != nil {
		return GrokCredential{}, err
	}
	candidate := GrokCredential{
		Type: ProviderGrok, AccessToken: imported.AccessToken, RefreshToken: imported.RefreshToken,
		IDToken: imported.IDToken, TokenType: imported.TokenType, ExpiresIn: imported.ExpiresIn,
		Expire: imported.Expire, LastRefresh: imported.LastRefresh,
		AccountID: firstNonEmptyString(imported.AccountID, imported.Subject), Email: imported.Email,
		TokenEndpoint: discovery.TokenEndpoint,
	}
	normalizeGrokCredential(&candidate)
	refreshed := false
	if expiresAt, ok := GrokCredentialExpiresAt(candidate); !ok || !expiresAt.After(grokNow(options).Add(5*time.Minute)) {
		candidate, err = refreshGrokTokensOnly(ctx, candidate, options)
		if err != nil {
			return GrokCredential{}, err
		}
		refreshed = true
	}
	profile, err := fetchGrokUserInfo(ctx, discovery.UserInfoEndpoint, candidate.AccessToken, options)
	if err != nil && !refreshed {
		var upstream *GrokUpstreamHTTPError
		if errors.As(err, &upstream) && upstream.StatusCode == http.StatusUnauthorized {
			candidate, err = refreshGrokTokensOnly(ctx, candidate, options)
			if err == nil {
				profile, err = fetchGrokUserInfo(ctx, discovery.UserInfoEndpoint, candidate.AccessToken, options)
			}
		}
	}
	if err != nil {
		return GrokCredential{}, err
	}
	expectedID := firstNonEmptyString(imported.AccountID, imported.Subject)
	if expectedID != "" && profile.Subject != expectedID {
		return GrokCredential{}, ErrGrokCredentialIdentityChanged
	}
	if expectedID == "" && strings.TrimSpace(imported.Email) != "" && !strings.EqualFold(strings.TrimSpace(imported.Email), profile.Email) {
		return GrokCredential{}, ErrGrokCredentialIdentityChanged
	}
	candidate.AccountID = profile.Subject
	candidate.Email = profile.Email
	if candidate.Expire == "" {
		return GrokCredential{}, fmt.Errorf("credential expired is required")
	}
	normalizeGrokCredential(&candidate)
	if err := validateGrokCredentialWithOptions(candidate, options); err != nil {
		return GrokCredential{}, err
	}
	return candidate, nil
}

func refreshGrokCredentialOnce(ctx context.Context, current GrokCredential, options GrokOptions) (GrokCredential, error) {
	normalizeGrokCredential(&current)
	if err := validateGrokCredentialWithOptions(current, options); err != nil {
		return GrokCredential{}, err
	}
	refreshed, err := refreshGrokTokensOnly(ctx, current, options)
	if err != nil {
		return GrokCredential{}, err
	}
	if subject := grokJWTSubject(refreshed.IDToken); subject != "" && subject != current.AccountID {
		return GrokCredential{}, ErrGrokCredentialIdentityChanged
	}
	discovery, discoveryErr := resolveGrokTokenAndUserInfo(ctx, refreshed.TokenEndpoint, options)
	if discoveryErr == nil {
		if profile, profileErr := fetchGrokUserInfo(ctx, discovery.UserInfoEndpoint, refreshed.AccessToken, options); profileErr == nil {
			if profile.Subject != current.AccountID {
				return GrokCredential{}, ErrGrokCredentialIdentityChanged
			}
			refreshed.Email = profile.Email
		}
	}
	refreshed.AccountID = current.AccountID
	normalizeGrokCredential(&refreshed)
	if err := validateGrokCredentialWithOptions(refreshed, options); err != nil {
		return GrokCredential{}, err
	}
	return refreshed, nil
}

func refreshGrokTokensOnly(ctx context.Context, current GrokCredential, options GrokOptions) (GrokCredential, error) {
	if strings.TrimSpace(current.RefreshToken) == "" {
		return GrokCredential{}, fmt.Errorf("Grok refresh token is required")
	}
	discovery, err := resolveGrokTokenAndUserInfo(ctx, current.TokenEndpoint, options)
	if err != nil {
		return GrokCredential{}, err
	}
	form := url.Values{
		"grant_type": {"refresh_token"}, "client_id": {xaiauth.ClientID}, "refresh_token": {strings.TrimSpace(current.RefreshToken)},
	}
	body, status, err := doGrokForm(ctx, options, discovery.TokenEndpoint, form)
	if err != nil {
		return GrokCredential{}, fmt.Errorf("refresh Grok credential: %w", err)
	}
	defer clear(body)
	var token grokTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return GrokCredential{}, fmt.Errorf("decode Grok refresh response: %w", err)
	}
	if status != http.StatusOK {
		return GrokCredential{}, &GrokTokenEndpointError{StatusCode: status, Code: boundedGrokOAuthCode(token.Error)}
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return GrokCredential{}, fmt.Errorf("Grok refresh response has no access token")
	}
	now := grokNow(options)
	refreshed := current
	refreshed.Type = ProviderGrok
	refreshed.AccessToken = strings.TrimSpace(token.AccessToken)
	if strings.TrimSpace(token.RefreshToken) != "" {
		refreshed.RefreshToken = strings.TrimSpace(token.RefreshToken)
	}
	if strings.TrimSpace(token.IDToken) != "" {
		refreshed.IDToken = strings.TrimSpace(token.IDToken)
	}
	if strings.TrimSpace(token.TokenType) != "" {
		refreshed.TokenType = strings.TrimSpace(token.TokenType)
	}
	if token.ExpiresIn > 0 {
		refreshed.ExpiresIn = token.ExpiresIn
		refreshed.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	refreshed.LastRefresh = now.Format(time.RFC3339)
	refreshed.TokenEndpoint = discovery.TokenEndpoint
	return refreshed, nil
}

func grokCredentialFromToken(
	ctx context.Context,
	token grokTokenResponse,
	expectedID string,
	tokenEndpoint string,
	userInfoEndpoint string,
	options GrokOptions,
	requireRefreshToken bool,
) (GrokCredential, error) {
	if strings.TrimSpace(token.AccessToken) == "" || (requireRefreshToken && strings.TrimSpace(token.RefreshToken) == "") {
		return GrokCredential{}, fmt.Errorf("Grok token response is incomplete")
	}
	profile, err := fetchGrokUserInfo(ctx, userInfoEndpoint, token.AccessToken, options)
	if err != nil {
		return GrokCredential{}, err
	}
	if expectedID != "" && profile.Subject != expectedID {
		return GrokCredential{}, ErrGrokCredentialIdentityChanged
	}
	if subject := grokJWTSubject(token.IDToken); subject != "" && subject != profile.Subject {
		return GrokCredential{}, ErrGrokCredentialIdentityChanged
	}
	now := grokNow(options)
	credential := GrokCredential{
		Type: ProviderGrok, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		IDToken: token.IDToken, TokenType: token.TokenType, ExpiresIn: token.ExpiresIn,
		LastRefresh: now.Format(time.RFC3339), AccountID: profile.Subject, Email: profile.Email,
		TokenEndpoint: tokenEndpoint,
	}
	if token.ExpiresIn > 0 {
		credential.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	normalizeGrokCredential(&credential)
	if err := validateGrokCredentialWithOptions(credential, options); err != nil {
		return GrokCredential{}, err
	}
	return credential, nil
}

func discoverGrokOAuth(ctx context.Context, options GrokOptions) (grokDiscovery, error) {
	discoveryURL := strings.TrimSpace(options.DiscoveryURL)
	allowTestEndpoint := discoveryURL != ""
	if discoveryURL == "" {
		discoveryURL = xaiauth.DiscoveryURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return grokDiscovery{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := grokHTTPClient(options).Do(request)
	if err != nil {
		return grokDiscovery{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponse+1))
	if err != nil {
		return grokDiscovery{}, err
	}
	defer clear(body)
	if len(body) > maxTokenResponse {
		return grokDiscovery{}, fmt.Errorf("Grok discovery response is too large")
	}
	if response.StatusCode != http.StatusOK {
		return grokDiscovery{}, &GrokUpstreamHTTPError{Operation: "discovery", StatusCode: response.StatusCode}
	}
	var discovery grokDiscovery
	if err := json.Unmarshal(body, &discovery); err != nil {
		return grokDiscovery{}, fmt.Errorf("decode Grok discovery: %w", err)
	}
	for field, raw := range map[string]string{
		"device_authorization_endpoint": discovery.DeviceAuthorizationEndpoint,
		"token_endpoint":                discovery.TokenEndpoint,
		"userinfo_endpoint":             discovery.UserInfoEndpoint,
	} {
		validated, err := validateGrokOAuthEndpoint(raw, field, allowTestEndpoint)
		if err != nil {
			return grokDiscovery{}, err
		}
		switch field {
		case "device_authorization_endpoint":
			discovery.DeviceAuthorizationEndpoint = validated
		case "token_endpoint":
			discovery.TokenEndpoint = validated
		case "userinfo_endpoint":
			discovery.UserInfoEndpoint = validated
		}
	}
	return discovery, nil
}

func resolveGrokTokenAndUserInfo(ctx context.Context, tokenEndpoint string, options GrokOptions) (grokDiscovery, error) {
	userInfo := strings.TrimSpace(options.UserInfoURL)
	allowTestEndpoint := strings.TrimSpace(options.DiscoveryURL) != "" || userInfo != ""
	if strings.TrimSpace(tokenEndpoint) != "" && userInfo != "" {
		validatedToken, err := validateGrokOAuthEndpoint(tokenEndpoint, "token_endpoint", allowTestEndpoint)
		if err != nil {
			return grokDiscovery{}, err
		}
		validatedUserInfo, err := validateGrokOAuthEndpoint(userInfo, "userinfo_endpoint", allowTestEndpoint)
		if err != nil {
			return grokDiscovery{}, err
		}
		return grokDiscovery{TokenEndpoint: validatedToken, UserInfoEndpoint: validatedUserInfo}, nil
	}
	discovery, err := discoverGrokOAuth(ctx, options)
	if err != nil {
		return grokDiscovery{}, err
	}
	if strings.TrimSpace(tokenEndpoint) != "" {
		validated, err := validateGrokOAuthEndpoint(tokenEndpoint, "token_endpoint", allowTestEndpoint)
		if err != nil {
			return grokDiscovery{}, err
		}
		discovery.TokenEndpoint = validated
	}
	if userInfo != "" {
		validated, err := validateGrokOAuthEndpoint(userInfo, "userinfo_endpoint", allowTestEndpoint)
		if err != nil {
			return grokDiscovery{}, err
		}
		discovery.UserInfoEndpoint = validated
	}
	return discovery, nil
}

func validateGrokOAuthEndpoint(raw, field string, allowTestEndpoint bool) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("Grok %s is invalid", field)
	}
	if allowTestEndpoint {
		return raw, nil
	}
	return xaiauth.ValidateOAuthEndpoint(raw, field)
}

func fetchGrokUserInfo(ctx context.Context, endpoint, accessToken string, options GrokOptions) (grokUserInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(endpoint), nil)
	if err != nil {
		return grokUserInfo{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	response, err := grokHTTPClient(options).Do(request)
	if err != nil {
		return grokUserInfo{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponse+1))
	if err != nil {
		return grokUserInfo{}, err
	}
	defer clear(body)
	if len(body) > maxTokenResponse {
		return grokUserInfo{}, fmt.Errorf("Grok userinfo response is too large")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return grokUserInfo{}, &GrokUpstreamHTTPError{Operation: "userinfo", StatusCode: response.StatusCode}
	}
	var profile grokUserInfo
	if err := json.Unmarshal(body, &profile); err != nil {
		return grokUserInfo{}, fmt.Errorf("decode Grok userinfo: %w", err)
	}
	profile.Subject = strings.TrimSpace(profile.Subject)
	profile.Email = strings.TrimSpace(profile.Email)
	if profile.Subject == "" || profile.Email == "" || !profile.EmailVerified ||
		strings.ContainsAny(profile.Subject+profile.Email, "\r\n\x00") {
		return grokUserInfo{}, fmt.Errorf("Grok userinfo identity is incomplete")
	}
	return profile, nil
}

func doGrokForm(ctx context.Context, options GrokOptions, endpoint string, form url.Values) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(endpoint), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := grokHTTPClient(options).Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponse+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if len(body) > maxTokenResponse {
		clear(body)
		return nil, response.StatusCode, fmt.Errorf("Grok OAuth response is too large")
	}
	return body, response.StatusCode, nil
}

func grokJWTSubject(token string) string {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	defer clear(payload)
	var claims struct {
		Subject string `json:"sub"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return strings.TrimSpace(claims.Subject)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func boundedGrokOAuthCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}
