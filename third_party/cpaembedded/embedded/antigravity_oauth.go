package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	antigravityauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/antigravity"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
)

var (
	ErrAntigravityInvalidState              = errors.New("antigravity oauth state mismatch")
	ErrAntigravityCredentialIdentityChanged = errors.New("refreshed antigravity credential identity changed")
)

// AntigravityTokenEndpointError retains only the bounded error code needed by
// the subscription lifecycle. Provider response bodies never leave this package.
type AntigravityTokenEndpointError struct {
	StatusCode int
	Code       string
}

func (err *AntigravityTokenEndpointError) Error() string {
	if err == nil {
		return "Antigravity token endpoint failed"
	}
	return fmt.Sprintf("Antigravity token endpoint returned status %d", err.StatusCode)
}

// AntigravityUpstreamHTTPError retains safe HTTP evidence for control-plane
// requests such as UserInfo, model discovery, and account observation.
type AntigravityUpstreamHTTPError struct {
	Operation  string
	StatusCode int
}

func (err *AntigravityUpstreamHTTPError) Error() string {
	if err == nil {
		return "Antigravity upstream request failed"
	}
	return fmt.Sprintf("Antigravity %s endpoint returned status %d", err.Operation, err.StatusCode)
}

// AntigravityOptions supplies testable HTTP endpoints and time boundaries. It
// never changes the fixed OAuth client identity or callback URI.
type AntigravityOptions struct {
	TokenURL             string
	UserInfoURL          string
	LoadCodeAssistURL    string
	RetrieveUserQuotaURL string
	OnboardUserURL       string
	FetchModelsURL       string
	HTTPClient           *http.Client
	Now                  func() time.Time
}

type antigravityTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type antigravityUserInfo struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail *bool  `json:"verified_email"`
}

type antigravityImportedCredential struct {
	Type         string `json:"type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id,omitempty"`
	Email        string `json:"email"`
	ProjectID    string `json:"project_id,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	Timestamp    int64  `json:"timestamp,omitempty"`
	Expire       string `json:"expired,omitempty"`
	LastRefresh  string `json:"last_refresh,omitempty"`
	Disabled     bool   `json:"disabled,omitempty"`
}

// BeginAntigravityBrowserAuthorization creates a browser challenge without
// opening a browser, binding a listener, or writing a credential file.
func BeginAntigravityBrowserAuthorization() (BrowserAuthorization, error) {
	state, err := misc.GenerateRandomState()
	if err != nil {
		return BrowserAuthorization{}, fmt.Errorf("generate Antigravity OAuth state: %w", err)
	}
	authorizationURL := antigravityauth.NewAntigravityAuth(&internalconfig.Config{}, nil).
		BuildAuthURL(state, AntigravityRedirectURI)
	return BrowserAuthorization{
		AuthorizationURL: authorizationURL,
		State:            state,
		ExpiresAt:        time.Now().UTC().Add(antigravityLoginTimeout),
	}, nil
}

// CompleteAntigravityBrowserAuthorization verifies the stage-bound state,
// exchanges one authorization code, and enriches the result with Google user
// identity plus the execution project required by Antigravity.
func CompleteAntigravityBrowserAuthorization(
	ctx context.Context,
	completion BrowserAuthorizationCompletion,
	options AntigravityOptions,
) (AntigravityCredential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !constantTimeEqual(completion.ExpectedState, completion.ReturnedState) {
		return AntigravityCredential{}, ErrAntigravityInvalidState
	}
	if strings.TrimSpace(completion.Code) == "" {
		return AntigravityCredential{}, fmt.Errorf("authorization code is required")
	}
	token, err := requestAntigravityToken(ctx, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {strings.TrimSpace(completion.Code)},
		"redirect_uri": {AntigravityRedirectURI},
	}, options)
	if err != nil {
		return AntigravityCredential{}, fmt.Errorf("exchange Antigravity authorization code: %w", err)
	}
	identity, err := fetchAntigravityUserInfo(ctx, token.AccessToken, options)
	if err != nil {
		return AntigravityCredential{}, fmt.Errorf("fetch Antigravity user identity: %w", err)
	}
	projectID, _, err := discoverAntigravityProject(ctx, token.AccessToken, options)
	if err != nil {
		return AntigravityCredential{}, fmt.Errorf("discover Antigravity project: %w", err)
	}
	now := antigravityNow(options)
	credential := AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		AccountID: identity.ID, Email: identity.Email, ProjectID: projectID,
		ExpiresIn: token.ExpiresIn, Timestamp: now.UnixMilli(), LastRefresh: now.Format(time.RFC3339),
	}
	if token.ExpiresIn > 0 {
		credential.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	normalizeAntigravityCredential(&credential)
	if err := validateAntigravityCredential(credential); err != nil {
		return AntigravityCredential{}, fmt.Errorf("exchange Antigravity authorization code: %w", err)
	}
	return credential, nil
}

// RefreshAntigravityCredentialOnce refreshes exactly once under the caller's
// context, then verifies that Google still identifies the same account. It
// deliberately bypasses CPA's executor refresh path so refreshed tokens always
// return to GPT-Load for CAS persistence.
func RefreshAntigravityCredentialOnce(
	ctx context.Context,
	current AntigravityCredential,
	options AntigravityOptions,
) (AntigravityCredential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateAntigravityCredential(current); err != nil {
		return AntigravityCredential{}, err
	}
	token, err := requestAntigravityToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {current.RefreshToken},
	}, options)
	if err != nil {
		return AntigravityCredential{}, fmt.Errorf("refresh Antigravity credential: %w", err)
	}
	identity, err := fetchAntigravityUserInfo(ctx, token.AccessToken, options)
	if err != nil {
		return AntigravityCredential{}, fmt.Errorf("verify refreshed Antigravity identity: %w", err)
	}
	if identity.ID != current.AccountID {
		return AntigravityCredential{}, ErrAntigravityCredentialIdentityChanged
	}
	refreshed := current
	refreshed.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		refreshed.RefreshToken = token.RefreshToken
	}
	refreshed.Email = identity.Email
	refreshed.ExpiresIn = token.ExpiresIn
	now := antigravityNow(options)
	refreshed.Timestamp = now.UnixMilli()
	refreshed.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	refreshed.LastRefresh = now.Format(time.RFC3339)
	normalizeAntigravityCredential(&refreshed)
	if err := validateAntigravityCredential(refreshed); err != nil {
		return AntigravityCredential{}, fmt.Errorf("refresh Antigravity credential: %w", err)
	}
	return refreshed, nil
}

// ImportAntigravityCredential enriches CPA's native OAuth JSON or revalidates
// a GPT-Load canonical download before it enters the encrypted-stage lifecycle.
// disabled is intentionally discarded because it is source-instance state.
func ImportAntigravityCredential(
	ctx context.Context,
	raw []byte,
	options AntigravityOptions,
) (AntigravityCredential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	imported, err := parseAntigravityImportedCredential(raw)
	if err != nil {
		return AntigravityCredential{}, err
	}
	now := antigravityNow(options)
	refreshed := false
	if expiresAt, known := imported.expiresAt(); !known || !expiresAt.After(now.Add(5*time.Minute)) {
		token, refreshErr := requestAntigravityToken(ctx, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {imported.RefreshToken},
		}, options)
		if refreshErr != nil {
			return AntigravityCredential{}, fmt.Errorf("refresh imported Antigravity credential: %w", refreshErr)
		}
		imported.applyToken(token, now)
		refreshed = true
	}
	identity, err := fetchAntigravityUserInfo(ctx, imported.AccessToken, options)
	if _, ok := antigravityUnauthorized(err); ok && !refreshed {
		token, refreshErr := requestAntigravityToken(ctx, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {imported.RefreshToken},
		}, options)
		if refreshErr != nil {
			return AntigravityCredential{}, fmt.Errorf("refresh imported Antigravity credential: %w", refreshErr)
		}
		imported.applyToken(token, now)
		identity, err = fetchAntigravityUserInfo(ctx, imported.AccessToken, options)
	}
	if err != nil {
		return AntigravityCredential{}, fmt.Errorf("verify imported Antigravity identity: %w", err)
	}
	if !strings.EqualFold(imported.Email, identity.Email) {
		return AntigravityCredential{}, fmt.Errorf("imported Antigravity email does not match userinfo")
	}
	if imported.AccountID != "" && imported.AccountID != identity.ID {
		return AntigravityCredential{}, fmt.Errorf("imported Antigravity account does not match userinfo")
	}
	projectID, _, err := discoverAntigravityProject(ctx, imported.AccessToken, options)
	if err != nil {
		return AntigravityCredential{}, fmt.Errorf("discover imported Antigravity project: %w", err)
	}
	credential := AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: imported.AccessToken, RefreshToken: imported.RefreshToken,
		AccountID: identity.ID, Email: identity.Email, ProjectID: projectID,
		ExpiresIn: imported.ExpiresIn, Timestamp: imported.Timestamp, Expire: imported.Expire,
		LastRefresh: imported.LastRefresh,
	}
	if credential.LastRefresh == "" {
		credential.LastRefresh = now.Format(time.RFC3339)
	}
	normalizeAntigravityCredential(&credential)
	if err := validateAntigravityCredential(credential); err != nil {
		return AntigravityCredential{}, fmt.Errorf("import Antigravity credential: %w", err)
	}
	return credential, nil
}

// MarshalAntigravityCredential returns a revalidated canonical JSON document
// suitable for GPT-Load encryption.
func MarshalAntigravityCredential(credential AntigravityCredential) ([]byte, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseAntigravityCredentialJSON(raw)
	clear(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(parsed)
}

func IsDefinitiveAntigravityRefreshRejection(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_grant", "invalid_token", "invalid_request":
		return true
	default:
		return false
	}
}

func antigravityNow(options AntigravityOptions) time.Time {
	if options.Now != nil {
		return options.Now().UTC()
	}
	return time.Now().UTC()
}

func antigravityOAuthClient(options AntigravityOptions) *http.Client {
	return clientWithoutRedirects(options.HTTPClient)
}

func requestAntigravityToken(
	ctx context.Context,
	values url.Values,
	options AntigravityOptions,
) (antigravityTokenResponse, error) {
	values = cloneURLValues(values)
	values.Set("client_id", antigravityauth.ClientID)
	values.Set("client_secret", antigravityauth.ClientSecret)
	endpoint := strings.TrimSpace(options.TokenURL)
	if endpoint == "" {
		endpoint = antigravityauth.TokenEndpoint
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return antigravityTokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := antigravityOAuthClient(options).Do(request)
	if err != nil {
		return antigravityTokenResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := readClaudeOAuthBody(response)
	if err != nil {
		return antigravityTokenResponse{}, err
	}
	defer clear(body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return antigravityTokenResponse{}, &AntigravityTokenEndpointError{
			StatusCode: response.StatusCode,
			Code:       antigravityTokenEndpointErrorCode(body),
		}
	}
	var token antigravityTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return antigravityTokenResponse{}, fmt.Errorf("decode Antigravity token response: %w", err)
	}
	token.AccessToken = strings.TrimSpace(token.AccessToken)
	token.RefreshToken = strings.TrimSpace(token.RefreshToken)
	if token.AccessToken == "" || token.ExpiresIn <= 0 {
		return antigravityTokenResponse{}, fmt.Errorf("Antigravity token response is incomplete")
	}
	if values.Get("grant_type") == "authorization_code" && token.RefreshToken == "" {
		return antigravityTokenResponse{}, fmt.Errorf("Antigravity token response is incomplete")
	}
	return token, nil
}

func fetchAntigravityUserInfo(
	ctx context.Context,
	accessToken string,
	options AntigravityOptions,
) (antigravityUserInfo, error) {
	endpoint := strings.TrimSpace(options.UserInfoURL)
	if endpoint == "" {
		endpoint = antigravityauth.UserInfoEndpoint
	}
	body, err := fetchAntigravityJSON(ctx, endpoint, accessToken, nil, "userinfo", options)
	if err != nil {
		return antigravityUserInfo{}, err
	}
	defer clear(body)
	var identity antigravityUserInfo
	if err := json.Unmarshal(body, &identity); err != nil {
		return antigravityUserInfo{}, fmt.Errorf("decode Antigravity userinfo response: %w", err)
	}
	identity.ID = strings.TrimSpace(identity.ID)
	identity.Email = strings.TrimSpace(identity.Email)
	if identity.ID == "" || identity.Email == "" || identity.VerifiedEmail == nil || !*identity.VerifiedEmail {
		return antigravityUserInfo{}, fmt.Errorf("Antigravity userinfo response is incomplete")
	}
	return identity, nil
}

func discoverAntigravityProject(
	ctx context.Context,
	accessToken string,
	options AntigravityOptions,
) (string, map[string]any, error) {
	endpoint := strings.TrimSpace(options.LoadCodeAssistURL)
	if endpoint == "" {
		endpoint = antigravityauth.APIEndpoint + "/" + antigravityauth.APIVersion + ":loadCodeAssist"
	}
	body, err := json.Marshal(map[string]any{"metadata": map[string]string{"ideType": "ANTIGRAVITY"}})
	if err != nil {
		return "", nil, err
	}
	defer clear(body)
	response, err := fetchAntigravityJSON(ctx, endpoint, accessToken, body, "loadCodeAssist", options)
	if err != nil {
		return "", nil, err
	}
	defer clear(response)
	var payload map[string]any
	if err := json.Unmarshal(response, &payload); err != nil || payload == nil {
		return "", nil, fmt.Errorf("decode Antigravity loadCodeAssist response")
	}
	if projectID := antigravityProjectID(payload); projectID != "" {
		return projectID, payload, nil
	}
	projectID, err := onboardAntigravityUser(ctx, accessToken, antigravityDefaultTierID(payload), options)
	if err != nil {
		return "", nil, err
	}
	if projectID == "" {
		return "", nil, fmt.Errorf("Antigravity project ID is missing")
	}
	return projectID, payload, nil
}

func fetchAntigravityJSON(
	ctx context.Context,
	endpoint string,
	accessToken string,
	payload []byte,
	operation string,
	options AntigravityOptions,
) ([]byte, error) {
	var reader *bytes.Reader
	method := http.MethodGet
	if payload != nil {
		method = http.MethodPost
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", misc.AntigravityRequestUserAgent(""))
	switch operation {
	case "loadCodeAssist":
		request.Header.Set("Accept", "*/*")
	case "onboardUser":
		request.Header.Set("Accept", "*/*")
		request.Header.Set("User-Agent", misc.AntigravityOnboardUserUserAgent(""))
		request.Header.Set("X-Goog-Api-Client", misc.AntigravityGoogAPIClientUA)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := antigravityOAuthClient(options).Do(request)
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
		return nil, &AntigravityUpstreamHTTPError{Operation: operation, StatusCode: response.StatusCode}
	}
	return body, nil
}

func onboardAntigravityUser(
	ctx context.Context,
	accessToken string,
	tierID string,
	options AntigravityOptions,
) (string, error) {
	endpoint := strings.TrimSpace(options.OnboardUserURL)
	if endpoint == "" {
		endpoint = antigravityauth.DailyAPIEndpoint + "/" + antigravityauth.APIVersion + ":onboardUser"
	}
	userAgent := misc.AntigravityOnboardUserUserAgent("")
	payload, err := json.Marshal(map[string]any{
		"tier_id": tierID,
		"metadata": map[string]string{
			"ide_type": "ANTIGRAVITY", "ide_name": "antigravity",
			"ide_version": misc.AntigravityVersionFromUserAgent(userAgent),
		},
	})
	if err != nil {
		return "", err
	}
	defer clear(payload)
	for attempt := 0; attempt < 5; attempt++ {
		body, err := fetchAntigravityJSON(ctx, endpoint, accessToken, payload, "onboardUser", options)
		if err != nil {
			return "", err
		}
		var result struct {
			Done     bool           `json:"done"`
			Response map[string]any `json:"response"`
		}
		err = json.Unmarshal(body, &result)
		clear(body)
		if err != nil {
			return "", fmt.Errorf("decode Antigravity onboardUser response")
		}
		if result.Done {
			return antigravityProjectID(result.Response), nil
		}
		if attempt == 4 {
			break
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", fmt.Errorf("Antigravity onboarding did not complete")
}

func antigravityProjectID(payload map[string]any) string {
	for _, key := range []string{"cloudaicompanionProject", "projectId", "project"} {
		switch value := payload[key].(type) {
		case string:
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		case map[string]any:
			if id, ok := value["id"].(string); ok {
				if id = strings.TrimSpace(id); id != "" {
					return id
				}
			}
		}
	}
	return ""
}

func antigravityDefaultTierID(payload map[string]any) string {
	if tiers, ok := payload["allowedTiers"].([]any); ok {
		for _, raw := range tiers {
			tier, ok := raw.(map[string]any)
			if !ok || tier["isDefault"] != true {
				continue
			}
			if id, ok := tier["id"].(string); ok && strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	}
	if tier, ok := payload["currentTier"].(map[string]any); ok {
		if id, ok := tier["id"].(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return "free-tier"
}

func antigravityTokenEndpointErrorCode(body []byte) string {
	var payload struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	value := strings.ToLower(strings.TrimSpace(payload.Error))
	if value == "" || len(value) > 64 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func cloneURLValues(values url.Values) url.Values {
	result := make(url.Values, len(values))
	for key, value := range values {
		result[key] = append([]string(nil), value...)
	}
	return result
}

func parseAntigravityImportedCredential(raw []byte) (antigravityImportedCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return antigravityImportedCredential{}, fmt.Errorf("credential JSON size is invalid")
	}
	allowed := map[string]struct{}{
		"type": {}, "access_token": {}, "refresh_token": {}, "account_id": {}, "email": {}, "project_id": {},
		"expires_in": {}, "timestamp": {}, "expired": {}, "last_refresh": {}, "disabled": {},
	}
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return antigravityImportedCredential{}, err
	}
	if err := validateAntigravityImportedDisabled(raw); err != nil {
		return antigravityImportedCredential{}, err
	}
	var credential antigravityImportedCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return antigravityImportedCredential{}, fmt.Errorf("decode imported Antigravity credential: %w", err)
	}
	credential.Type = strings.ToLower(strings.TrimSpace(credential.Type))
	credential.AccessToken = strings.TrimSpace(credential.AccessToken)
	credential.RefreshToken = strings.TrimSpace(credential.RefreshToken)
	credential.AccountID = strings.TrimSpace(credential.AccountID)
	credential.Email = strings.TrimSpace(credential.Email)
	credential.ProjectID = strings.TrimSpace(credential.ProjectID)
	credential.Expire = strings.TrimSpace(credential.Expire)
	credential.LastRefresh = strings.TrimSpace(credential.LastRefresh)
	if credential.Type != ProviderAntigravity || credential.AccessToken == "" || credential.RefreshToken == "" || credential.Email == "" {
		return antigravityImportedCredential{}, fmt.Errorf("imported Antigravity credential is incomplete")
	}
	for _, value := range []string{credential.AccessToken, credential.RefreshToken, credential.AccountID, credential.Email, credential.ProjectID} {
		if len(value) > 16*1024 || strings.ContainsAny(value, "\r\n\x00") {
			return antigravityImportedCredential{}, fmt.Errorf("imported Antigravity credential is invalid")
		}
	}
	if credential.ExpiresIn < 0 || credential.Timestamp < 0 || validateTimestamp("expired", credential.Expire) != nil ||
		validateTimestamp("last_refresh", credential.LastRefresh) != nil {
		return antigravityImportedCredential{}, fmt.Errorf("imported Antigravity credential token lifetime is invalid")
	}
	if credential.Expire == "" && (credential.ExpiresIn <= 0 || credential.Timestamp <= 0) {
		return antigravityImportedCredential{}, fmt.Errorf("imported Antigravity credential expiry is missing")
	}
	if credential.Expire == "" {
		credential.Expire = time.UnixMilli(credential.Timestamp).
			Add(time.Duration(credential.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	return credential, nil
}

func validateAntigravityImportedDisabled(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("decode imported Antigravity credential: %w", err)
	}
	value, present := fields["disabled"]
	if !present {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(string(value)), "null") {
		return fmt.Errorf("imported Antigravity credential disabled is invalid")
	}
	var disabled bool
	if err := json.Unmarshal(value, &disabled); err != nil {
		return fmt.Errorf("imported Antigravity credential disabled is invalid")
	}
	return nil
}

func (credential antigravityImportedCredential) expiresAt() (time.Time, bool) {
	value, err := time.Parse(time.RFC3339, credential.Expire)
	return value, err == nil
}

func (credential *antigravityImportedCredential) applyToken(token antigravityTokenResponse, now time.Time) {
	credential.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		credential.RefreshToken = token.RefreshToken
	}
	credential.ExpiresIn = token.ExpiresIn
	credential.Timestamp = now.UnixMilli()
	credential.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	credential.LastRefresh = now.Format(time.RFC3339)
}

func antigravityUnauthorized(err error) (*AntigravityUpstreamHTTPError, bool) {
	var upstream *AntigravityUpstreamHTTPError
	if errors.As(err, &upstream) && upstream != nil && upstream.StatusCode == http.StatusUnauthorized {
		return upstream, true
	}
	return nil, false
}
