package embedded

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/lzw"
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
)

type claudeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Organization struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"organization"`
	Account struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
}

type claudeOAuthProfile struct {
	Account struct {
		UUID        string `json:"uuid"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		CreatedAt   string `json:"created_at"`
	} `json:"account"`
	Organization struct {
		UUID                  string `json:"uuid"`
		Name                  string `json:"name"`
		Type                  string `json:"organization_type"`
		RateLimitTier         string `json:"rate_limit_tier"`
		SeatTier              string `json:"seat_tier"`
		ExtraUsageEnabled     *bool  `json:"has_extra_usage_enabled"`
		BillingType           string `json:"billing_type"`
		SubscriptionCreatedAt string `json:"subscription_created_at"`
	} `json:"organization"`
}

func completeClaudeBrowserAuthorization(
	ctx context.Context,
	completion BrowserAuthorizationCompletion,
	options ClaudeOptions,
) (ClaudeCredential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !constantTimeEqual(completion.ExpectedState, completion.ReturnedState) {
		return ClaudeCredential{}, ErrClaudeInvalidState
	}
	if strings.TrimSpace(completion.Code) == "" {
		return ClaudeCredential{}, fmt.Errorf("authorization code is required")
	}
	if strings.TrimSpace(completion.CodeVerifier) == "" {
		return ClaudeCredential{}, fmt.Errorf("PKCE verifier is required")
	}
	token, err := requestClaudeTokens(ctx, options, struct {
		GrantType    string `json:"grant_type"`
		Code         string `json:"code"`
		RedirectURI  string `json:"redirect_uri"`
		ClientID     string `json:"client_id"`
		CodeVerifier string `json:"code_verifier"`
		State        string `json:"state"`
	}{
		GrantType: "authorization_code", Code: strings.TrimSpace(completion.Code),
		RedirectURI: ClaudeRedirectURI, ClientID: claudeauth.ClientID,
		CodeVerifier: completion.CodeVerifier, State: completion.ReturnedState,
	})
	if err != nil {
		return ClaudeCredential{}, fmt.Errorf("exchange Claude authorization code: %w", err)
	}
	now := claudeNow(options)
	credential := ClaudeCredential{
		Type: ProviderClaude, IDToken: token.IDToken,
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		Email: token.Account.EmailAddress, AccountUUID: token.Account.UUID,
		OrganizationUUID: token.Organization.UUID, OrganizationName: token.Organization.Name,
		LastRefresh: now.Format(time.RFC3339),
	}
	if token.ExpiresIn > 0 {
		credential.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if profile, profileErr := fetchClaudeOAuthProfile(ctx, options, token.AccessToken); profileErr == nil {
		applyClaudeProfile(&credential, profile)
	}
	_ = fetchClaudeOAuthRoles(ctx, options, token.AccessToken)
	deviceIDs, err := claudeauth.GenerateDeviceIDPool()
	if err != nil {
		return ClaudeCredential{}, err
	}
	credential.DeviceIDs = deviceIDs
	normalizeClaudeCredential(&credential)
	if err := validateClaudeCredential(credential); err != nil {
		return ClaudeCredential{}, fmt.Errorf("exchange Claude authorization code: %w", err)
	}
	return credential, nil
}

func refreshClaudeCredentialOnce(
	ctx context.Context,
	current ClaudeCredential,
	options ClaudeOptions,
) (ClaudeCredential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return ClaudeCredential{}, err
	}
	current, err = ParseClaudeCredentialJSON(raw)
	clear(raw)
	if err != nil {
		return ClaudeCredential{}, err
	}
	token, err := requestClaudeTokens(ctx, options, map[string]any{
		"client_id": claudeauth.ClientID, "grant_type": "refresh_token",
		"refresh_token": current.RefreshToken, "scope": claudeauth.ClaudeOAuthScope,
	})
	if err != nil {
		return ClaudeCredential{}, fmt.Errorf("refresh Claude credential: %w", err)
	}
	now := claudeNow(options)
	refreshed := current
	refreshed.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		refreshed.RefreshToken = token.RefreshToken
	}
	if token.IDToken != "" {
		refreshed.IDToken = token.IDToken
	}
	refreshed.LastRefresh = now.Format(time.RFC3339)
	if token.ExpiresIn > 0 {
		refreshed.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	candidate := ClaudeCredential{
		AccountUUID: token.Account.UUID, Email: token.Account.EmailAddress,
		OrganizationUUID: token.Organization.UUID, OrganizationName: token.Organization.Name,
	}
	if profile, profileErr := fetchClaudeOAuthProfile(ctx, options, token.AccessToken); profileErr == nil {
		applyClaudeProfile(&candidate, profile)
	}
	if candidate.AccountUUID != "" && candidate.AccountUUID != current.AccountUUID {
		return ClaudeCredential{}, ErrClaudeCredentialIdentityChanged
	}
	if current.OrganizationUUID != "" && candidate.OrganizationUUID != "" &&
		candidate.OrganizationUUID != current.OrganizationUUID {
		return ClaudeCredential{}, ErrClaudeOrganizationIdentityChanged
	}
	if candidate.AccountUUID != "" {
		refreshed.AccountUUID = candidate.AccountUUID
	}
	if candidate.Email != "" {
		refreshed.Email = candidate.Email
	}
	if candidate.OrganizationUUID != "" {
		refreshed.OrganizationUUID = candidate.OrganizationUUID
	}
	if candidate.OrganizationName != "" {
		refreshed.OrganizationName = candidate.OrganizationName
	}
	normalizeClaudeCredential(&refreshed)
	if err := validateClaudeCredential(refreshed); err != nil {
		return ClaudeCredential{}, fmt.Errorf("refresh Claude credential: %w", err)
	}
	return refreshed, nil
}

func requestClaudeTokens(ctx context.Context, options ClaudeOptions, payload any) (claudeTokenResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return claudeTokenResponse{}, err
	}
	defer clear(body)
	endpoint := strings.TrimSpace(options.TokenURL)
	if endpoint == "" {
		endpoint = claudeauth.TokenURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return claudeTokenResponse{}, err
	}
	applyClaudeOAuthHeaders(request)
	response, err := claudeOAuthClient(options).Do(request)
	if err != nil {
		return claudeTokenResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := readClaudeOAuthBody(response)
	if err != nil {
		return claudeTokenResponse{}, err
	}
	defer clear(responseBody)
	if response.StatusCode != http.StatusOK {
		return claudeTokenResponse{}, &TokenEndpointError{
			StatusCode: response.StatusCode,
			Code:       claudeTokenEndpointErrorCode(responseBody),
		}
	}
	var token claudeTokenResponse
	if err := json.Unmarshal(responseBody, &token); err != nil {
		return claudeTokenResponse{}, fmt.Errorf("decode Claude token response: %w", err)
	}
	token.AccessToken = strings.TrimSpace(token.AccessToken)
	token.RefreshToken = strings.TrimSpace(token.RefreshToken)
	token.IDToken = strings.TrimSpace(token.IDToken)
	if token.AccessToken == "" {
		return claudeTokenResponse{}, fmt.Errorf("Claude token response has no access token")
	}
	return token, nil
}

func fetchClaudeOAuthProfile(ctx context.Context, options ClaudeOptions, accessToken string) (claudeOAuthProfile, error) {
	endpoint := strings.TrimSpace(options.ProfileURL)
	if endpoint == "" {
		endpoint = claudeauth.ProfileURL
	}
	body, err := fetchClaudeOAuthJSON(ctx, options, endpoint, accessToken)
	if err != nil {
		return claudeOAuthProfile{}, err
	}
	defer clear(body)
	var profile claudeOAuthProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return claudeOAuthProfile{}, err
	}
	if strings.TrimSpace(profile.Account.UUID) == "" {
		return claudeOAuthProfile{}, fmt.Errorf("Claude OAuth profile has no account UUID")
	}
	return profile, nil
}

func fetchClaudeOAuthRoles(ctx context.Context, options ClaudeOptions, accessToken string) error {
	endpoint := strings.TrimSpace(options.RolesURL)
	if endpoint == "" {
		endpoint = claudeauth.RolesURL
	}
	body, err := fetchClaudeOAuthJSON(ctx, options, endpoint, accessToken)
	if err != nil {
		return err
	}
	defer clear(body)
	if !json.Valid(body) {
		return fmt.Errorf("Claude OAuth roles response is not JSON")
	}
	return nil
}

func fetchClaudeOAuthJSON(
	ctx context.Context,
	options ClaudeOptions,
	endpoint string,
	accessToken string,
) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	applyClaudeOAuthHeaders(request)
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	request.Header.Set("Cache-Control", "no-cache")
	response, err := claudeOAuthClient(options).Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := readClaudeOAuthBody(response)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		clear(body)
		return nil, &ClaudeUpstreamHTTPError{StatusCode: response.StatusCode}
	}
	return body, nil
}

func applyClaudeProfile(credential *ClaudeCredential, profile claudeOAuthProfile) {
	if value := strings.TrimSpace(profile.Account.UUID); value != "" {
		credential.AccountUUID = value
	}
	if value := strings.TrimSpace(profile.Account.Email); value != "" {
		credential.Email = value
	}
	if value := strings.TrimSpace(profile.Organization.UUID); value != "" {
		credential.OrganizationUUID = value
	}
	if value := strings.TrimSpace(profile.Organization.Name); value != "" {
		credential.OrganizationName = value
	}
}

func claudeNow(options ClaudeOptions) time.Time {
	if options.Now != nil {
		return options.Now().UTC()
	}
	return time.Now().UTC()
}

func claudeOAuthClient(options ClaudeOptions) *http.Client {
	if options.HTTPClient != nil {
		return clientWithoutRedirects(options.HTTPClient)
	}
	return clientWithoutRedirects(claudeauth.NewAnthropicHttpClient(nil))
}

func applyClaudeOAuthHeaders(request *http.Request) {
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "axios/1.15.2")
	request.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	request.Header.Set("Connection", "close")
	request.Close = true
}

func readClaudeOAuthBody(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("Claude OAuth response body is unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponse+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxTokenResponse {
		clear(body)
		return nil, fmt.Errorf("Claude OAuth response is too large")
	}
	encodings := strings.Split(strings.Join(response.Header.Values("Content-Encoding"), ","), ",")
	for index := len(encodings) - 1; index >= 0; index-- {
		encoding := strings.ToLower(strings.TrimSpace(encodings[index]))
		if encoding == "" || encoding == "identity" {
			continue
		}
		decoded, decodeErr := decodeClaudeOAuthBody(body, encoding)
		clear(body)
		if decodeErr != nil {
			return nil, decodeErr
		}
		body = decoded
	}
	return body, nil
}

func decodeClaudeOAuthBody(body []byte, encoding string) ([]byte, error) {
	var reader io.ReadCloser
	switch encoding {
	case "gzip":
		value, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		reader = value
	case "deflate":
		value, err := zlib.NewReader(bytes.NewReader(body))
		if err == nil {
			reader = value
		} else {
			reader = flate.NewReader(bytes.NewReader(body))
		}
	case "br":
		reader = io.NopCloser(brotli.NewReader(bytes.NewReader(body)))
	case "compress":
		reader = lzw.NewReader(bytes.NewReader(body), lzw.MSB, 8)
	default:
		return nil, fmt.Errorf("unsupported Claude OAuth content encoding %q", encoding)
	}
	defer func() { _ = reader.Close() }()
	decoded, err := io.ReadAll(io.LimitReader(reader, maxTokenResponse+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > maxTokenResponse {
		clear(decoded)
		return nil, fmt.Errorf("Claude OAuth response is too large")
	}
	return decoded, nil
}

func claudeTokenEndpointErrorCode(body []byte) string {
	var payload struct {
		Error json.RawMessage `json:"error"`
		Code  string          `json:"code"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	candidates := []string{payload.Code}
	var text string
	if json.Unmarshal(payload.Error, &text) == nil {
		candidates = append(candidates, text)
	} else {
		var nested struct {
			Type string `json:"type"`
			Code string `json:"code"`
		}
		if json.Unmarshal(payload.Error, &nested) == nil {
			candidates = append(candidates, nested.Code, nested.Type)
		}
	}
	for _, value := range candidates {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && len(value) <= 64 && !strings.ContainsAny(value, "\r\n\x00") {
			return value
		}
	}
	return ""
}
