// Package embedded exposes the smallest CPA surface required by GPT-Load.
// It deliberately excludes CPA's manager, selector, retry loop, server, watcher,
// file store, websocket executor, and automatic credential refresh.
package embedded

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	codexauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

const (
	ProviderCodex        = "codex"
	CodexRedirectURI     = codexauth.RedirectURI
	defaultTokenURL      = codexauth.TokenURL
	maxCredentialBytes   = 64 * 1024
	maxTokenResponse     = 1024 * 1024
	defaultLoginTimeout  = 5 * time.Minute
	defaultCodexBaseURL  = "https://chatgpt.com/backend-api/codex"
	defaultModelsVersion = "0.144.1"
)

var (
	ErrInvalidState       = errors.New("codex oauth state mismatch")
	ErrRedirectNotAllowed = errors.New("codex upstream redirect is not allowed")
)

// TokenEndpointError retains only the bounded OAuth error code needed to
// distinguish definitive token rejection from transient endpoint failures.
type TokenEndpointError struct {
	StatusCode int
	Code       string
}

func (e *TokenEndpointError) Error() string {
	return fmt.Sprintf("token endpoint returned status %d", e.StatusCode)
}

// CodexCredential is the canonical, execution-only Codex credential schema.
// Callers must encrypt the complete value before persistence.
type CodexCredential struct {
	Type         string `json:"type"`
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
	Email        string `json:"email,omitempty"`
	Expire       string `json:"expired,omitempty"`
	LastRefresh  string `json:"last_refresh,omitempty"`
}

// BrowserAuthorization contains the public authorization challenge and the
// secret verifier that GPT-Load must encrypt in its short-lived Stage.
type BrowserAuthorization struct {
	AuthorizationURL string
	State            string
	CodeVerifier     string
	CodeChallenge    string
	ExpiresAt        time.Time
}

// BrowserAuthorizationCompletion contains a callback and its Stage-bound secret.
type BrowserAuthorizationCompletion struct {
	ExpectedState string
	ReturnedState string
	Code          string
	CodeVerifier  string
}

// Options supplies testable transport boundaries without changing OAuth identity.
type Options struct {
	TokenURL   string
	HTTPClient *http.Client
	AccountID  string
	Email      string
}

// Model describes one upstream Codex model identity returned by the account.
type Model struct {
	ID string `json:"id"`
}

// AccountObservation is the provider response used by GPT-Load's dynamic,
// schema-tolerant subscription observation parser.
type AccountObservation struct {
	Payload []byte
	Header  http.Header
}

// HTTPExecutor is the stable execution facade consumed by GPT-Load. CPA's
// concrete executor and auth types stay inside this nested module.
type HTTPExecutor interface {
	ExecuteCanonical(context.Context, string, CodexCredential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStreamCanonical(context.Context, string, CodexCredential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type ExecuteRequest struct {
	Model           string
	Payload         []byte
	Format          string
	Headers         http.Header
	OriginalRequest []byte
}

type ExecuteResponse struct {
	Payload []byte
	Headers http.Header
}

type ExecuteStreamResponse struct {
	Headers http.Header
	Chunks  <-chan ExecuteStreamChunk
}

type ExecuteStreamChunk struct {
	Payload []byte
	Err     error
}

// BeginCodexBrowserAuthorization creates one browser OAuth challenge without
// starting a listener, opening a browser, writing a file, or launching a goroutine.
func BeginCodexBrowserAuthorization() (BrowserAuthorization, error) {
	pkce, err := codexauth.GeneratePKCECodes()
	if err != nil {
		return BrowserAuthorization{}, fmt.Errorf("generate PKCE: %w", err)
	}
	state, err := misc.GenerateRandomState()
	if err != nil {
		return BrowserAuthorization{}, fmt.Errorf("generate OAuth state: %w", err)
	}
	authorizationURL, err := codexauth.NewCodexAuth(&internalconfig.Config{}).GenerateAuthURL(state, pkce)
	if err != nil {
		return BrowserAuthorization{}, fmt.Errorf("generate authorization URL: %w", err)
	}
	return BrowserAuthorization{
		AuthorizationURL: authorizationURL,
		State:            state,
		CodeVerifier:     pkce.CodeVerifier,
		CodeChallenge:    pkce.CodeChallenge,
		ExpiresAt:        time.Now().UTC().Add(defaultLoginTimeout),
	}, nil
}

// CompleteCodexBrowserAuthorization validates state and exchanges one code once.
func CompleteCodexBrowserAuthorization(ctx context.Context, completion BrowserAuthorizationCompletion, options Options) (CodexCredential, error) {
	if !constantTimeEqual(completion.ExpectedState, completion.ReturnedState) {
		return CodexCredential{}, ErrInvalidState
	}
	if strings.TrimSpace(completion.Code) == "" {
		return CodexCredential{}, fmt.Errorf("authorization code is required")
	}
	if strings.TrimSpace(completion.CodeVerifier) == "" {
		return CodexCredential{}, fmt.Errorf("PKCE verifier is required")
	}

	values := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexauth.ClientID},
		"code":          {strings.TrimSpace(completion.Code)},
		"redirect_uri":  {CodexRedirectURI},
		"code_verifier": {completion.CodeVerifier},
	}
	credential, err := exchangeToken(ctx, values, options)
	if err != nil {
		return CodexCredential{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	if err := validateCredential(credential); err != nil {
		return CodexCredential{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	return credential, nil
}

// RefreshCodexCredentialOnce refreshes exactly once and preserves stable identity
// when the token response omits identity claims or a rotated refresh token.
func RefreshCodexCredentialOnce(ctx context.Context, current CodexCredential, options Options) (CodexCredential, error) {
	if err := validateCredential(current); err != nil {
		return CodexCredential{}, err
	}
	values := url.Values{
		"client_id":     {codexauth.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {current.RefreshToken},
		"scope":         {"openid profile email"},
	}
	refreshed, err := exchangeToken(ctx, values, options)
	if err != nil {
		return CodexCredential{}, fmt.Errorf("refresh credential: %w", err)
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = current.RefreshToken
	}
	if refreshed.IDToken == "" {
		refreshed.IDToken = current.IDToken
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = current.AccountID
	}
	if refreshed.Email == "" {
		refreshed.Email = current.Email
	}
	if refreshed.AccountID != current.AccountID {
		return CodexCredential{}, fmt.Errorf("refreshed credential identity changed")
	}
	if err := validateCredential(refreshed); err != nil {
		return CodexCredential{}, fmt.Errorf("refresh credential: %w", err)
	}
	return refreshed, nil
}

// ParseCodexCredentialJSON strictly normalizes the CPA flat Codex auth format.
func ParseCodexCredentialJSON(raw []byte) (CodexCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return CodexCredential{}, fmt.Errorf("credential JSON size is invalid")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return CodexCredential{}, fmt.Errorf("credential must be one JSON object")
	}
	for _, forbidden := range []string{
		"proxy", "proxy_url", "headers", "request_retry", "retry", "cooldown",
		"model_alias", "model_aliases", "aliases",
	} {
		if _, ok := fields[forbidden]; ok {
			return CodexCredential{}, fmt.Errorf("credential field %q is not allowed", forbidden)
		}
	}
	var credential CodexCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return CodexCredential{}, fmt.Errorf("decode credential: %w", err)
	}
	credential.Type = strings.ToLower(strings.TrimSpace(credential.Type))
	credential.AccessToken = strings.TrimSpace(credential.AccessToken)
	credential.RefreshToken = strings.TrimSpace(credential.RefreshToken)
	credential.IDToken = strings.TrimSpace(credential.IDToken)
	credential.AccountID = strings.TrimSpace(credential.AccountID)
	credential.Email = strings.TrimSpace(credential.Email)
	credential.Expire = strings.TrimSpace(credential.Expire)
	credential.LastRefresh = strings.TrimSpace(credential.LastRefresh)
	if err := validateCredential(credential); err != nil {
		return CodexCredential{}, err
	}
	if err := validateTimestamp("expired", credential.Expire); err != nil {
		return CodexCredential{}, err
	}
	if err := validateTimestamp("last_refresh", credential.LastRefresh); err != nil {
		return CodexCredential{}, err
	}
	return credential, nil
}

// CodexCredentialExpiresAt returns the best known access-token expiration.
// The explicit CPA timestamp wins; JWT exp is a compatibility fallback.
func CodexCredentialExpiresAt(credential CodexCredential) (time.Time, bool) {
	if value := strings.TrimSpace(credential.Expire); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		return parsed, err == nil
	}
	parts := strings.Split(strings.TrimSpace(credential.AccessToken), ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	claims, err := codexauth.ParseJWTToken(credential.AccessToken)
	if err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0).UTC(), true
}

// NewCodexAuth maps canonical data to the small CPA Auth surface used by the
// stateless HTTP executor. It does not attach storage or runtime pool state.
func NewCodexAuth(id string, credential CodexCredential, baseURL string) *cliproxyauth.Auth {
	metadata := map[string]any{
		"type":          ProviderCodex,
		"access_token":  credential.AccessToken,
		"refresh_token": credential.RefreshToken,
		"account_id":    credential.AccountID,
	}
	if credential.IDToken != "" {
		metadata["id_token"] = credential.IDToken
	}
	if credential.Email != "" {
		metadata["email"] = credential.Email
	}
	if credential.Expire != "" {
		metadata["expired"] = credential.Expire
	}
	if credential.LastRefresh != "" {
		metadata["last_refresh"] = credential.LastRefresh
	}
	attributes := map[string]string{}
	if trimmed := strings.TrimSpace(baseURL); trimmed != "" {
		attributes["base_url"] = trimmed
	}
	return &cliproxyauth.Auth{
		ID:         strings.TrimSpace(id),
		Provider:   ProviderCodex,
		Attributes: attributes,
		Metadata:   metadata,
	}
}

// CodexHTTPExecutor is an execution-only wrapper around CPA's stateless HTTP
// Codex executor. It rejects redirects before net/http can replay a POST.
type CodexHTTPExecutor struct {
	cfg   *internalconfig.Config
	inner *internalexecutor.CodexExecutor
}

// NewCodexHTTPExecutor constructs an HTTP-only executor with no CPA manager.
func NewCodexHTTPExecutor() *CodexHTTPExecutor {
	cfg := &internalconfig.Config{}
	return &CodexHTTPExecutor{cfg: cfg, inner: internalexecutor.NewCodexExecutor(cfg)}
}

func (e *CodexHTTPExecutor) Identifier() string { return ProviderCodex }

func (e *CodexHTTPExecutor) ExecuteCanonical(ctx context.Context, credentialID string, credential CodexCredential, request ExecuteRequest) (ExecuteResponse, error) {
	format := sdktranslator.FromString(request.Format)
	auth := NewCodexAuth(credentialID, credential, "")
	response, err := e.inner.Execute(e.executionContext(ctx, auth), auth, cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, cliproxyexecutor.Options{
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		SourceFormat: format, ResponseFormat: format,
	})
	if err != nil {
		return ExecuteResponse{}, err
	}
	return ExecuteResponse{Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone()}, nil
}

func (e *CodexHTTPExecutor) ExecuteStreamCanonical(ctx context.Context, credentialID string, credential CodexCredential, request ExecuteRequest) (*ExecuteStreamResponse, error) {
	format := sdktranslator.FromString(request.Format)
	auth := NewCodexAuth(credentialID, credential, "")
	response, err := e.inner.ExecuteStream(e.executionContext(ctx, auth), auth, cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, cliproxyexecutor.Options{
		Stream: true, Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		SourceFormat: format, ResponseFormat: format,
	})
	if err != nil {
		return nil, err
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			select {
			case chunks <- ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Err: chunk.Err}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &ExecuteStreamResponse{Headers: response.Headers.Clone(), Chunks: chunks}, nil
}

func (e *CodexHTTPExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return e.inner.Execute(e.executionContext(ctx, auth), auth, req, opts)
}

func (e *CodexHTTPExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return e.inner.ExecuteStream(e.executionContext(ctx, auth), auth, req, opts)
}

func (e *CodexHTTPExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	credential, err := credentialFromAuth(auth)
	if err != nil {
		return nil, err
	}
	refreshed, err := RefreshCodexCredentialOnce(ctx, credential, Options{})
	if err != nil {
		return nil, err
	}
	return NewCodexAuth(auth.ID, refreshed, auth.Attributes["base_url"]), nil
}

func (e *CodexHTTPExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return e.inner.CountTokens(ctx, auth, req, opts)
}

func (e *CodexHTTPExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	return e.inner.HttpRequest(e.executionContext(ctx, auth), auth, req)
}

// ListCodexModels performs exactly one account-bound models request.
func ListCodexModels(ctx context.Context, credential CodexCredential, baseURL string) ([]Model, error) {
	if err := validateCredential(credential); err != nil {
		return nil, err
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultCodexBaseURL
	}
	target, err := url.Parse(strings.TrimRight(baseURL, "/") + "/models")
	if err != nil {
		return nil, err
	}
	query := target.Query()
	query.Set("client_version", defaultModelsVersion)
	target.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	applyCodexReadHeaders(req, credential)
	resp, err := clientWithoutRedirects(nil).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse+1))
	if err != nil || len(body) > maxTokenResponse {
		return nil, fmt.Errorf("read Codex models response")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Codex models endpoint returned status %d", resp.StatusCode)
	}
	var payload struct {
		Models []struct {
			Slug  string `json:"slug"`
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Models == nil {
		return nil, fmt.Errorf("decode Codex models response")
	}
	models := make([]Model, 0, len(payload.Models))
	seen := make(map[string]struct{}, len(payload.Models))
	for _, item := range payload.Models {
		id := strings.TrimSpace(item.Slug)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id == "" {
			id = strings.TrimSpace(item.Model)
		}
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, Model{ID: id})
	}
	return models, nil
}

// ObserveCodexAccount performs exactly one fixed usage request. The response
// schema remains opaque here so GPT-Load can version and normalize it itself.
func ObserveCodexAccount(ctx context.Context, credential CodexCredential, baseURL string) (AccountObservation, error) {
	if err := validateCredential(credential); err != nil {
		return AccountObservation{}, err
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://chatgpt.com/backend-api"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/wham/usage", nil)
	if err != nil {
		return AccountObservation{}, err
	}
	applyCodexReadHeaders(req, credential)
	resp, err := clientWithoutRedirects(nil).Do(req)
	if err != nil {
		return AccountObservation{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse+1))
	if err != nil || len(body) > maxTokenResponse {
		return AccountObservation{}, fmt.Errorf("read Codex usage response")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return AccountObservation{}, fmt.Errorf("Codex usage endpoint returned status %d", resp.StatusCode)
	}
	if !json.Valid(body) {
		return AccountObservation{}, fmt.Errorf("decode Codex usage response")
	}
	return AccountObservation{Payload: body, Header: resp.Header.Clone()}, nil
}

func applyCodexReadHeaders(req *http.Request, credential CodexCredential) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	req.Header.Set("Chatgpt-Account-Id", credential.AccountID)
	req.Header.Set("Originator", "codex_cli_rs")
	req.Header.Set("User-Agent", "codex_cli_rs/"+defaultModelsVersion)
}

func (e *CodexHTTPExecutor) executionContext(ctx context.Context, auth *cliproxyauth.Auth) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	baseClient := helps.NewUtlsHTTPClient(ctx, e.cfg, auth, 0)
	transport := baseClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return context.WithValue(ctx, "cliproxy.roundtripper", noRedirectRoundTripper{base: transport})
}

type noRedirectRoundTripper struct {
	base http.RoundTripper
}

func (t noRedirectRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" {
		_ = resp.Body.Close()
		return nil, ErrRedirectNotAllowed
	}
	return resp, nil
}

func exchangeToken(ctx context.Context, values url.Values, options Options) (CodexCredential, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tokenURL := strings.TrimSpace(options.TokenURL)
	if tokenURL == "" {
		tokenURL = defaultTokenURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return CodexCredential{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := clientWithoutRedirects(options.HTTPClient)
	resp, err := client.Do(req)
	if err != nil {
		return CodexCredential{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse+1))
	if err != nil {
		return CodexCredential{}, err
	}
	if len(body) > maxTokenResponse {
		return CodexCredential{}, fmt.Errorf("token response is too large")
	}
	if resp.StatusCode != http.StatusOK {
		return CodexCredential{}, &TokenEndpointError{
			StatusCode: resp.StatusCode,
			Code:       tokenEndpointErrorCode(body),
		}
	}
	var tokenResponse struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return CodexCredential{}, fmt.Errorf("decode token response: %w", err)
	}
	credential := CodexCredential{
		Type:         ProviderCodex,
		IDToken:      strings.TrimSpace(tokenResponse.IDToken),
		AccessToken:  strings.TrimSpace(tokenResponse.AccessToken),
		RefreshToken: strings.TrimSpace(tokenResponse.RefreshToken),
		AccountID:    strings.TrimSpace(options.AccountID),
		Email:        strings.TrimSpace(options.Email),
		LastRefresh:  time.Now().UTC().Format(time.RFC3339),
	}
	if tokenResponse.ExpiresIn > 0 {
		credential.Expire = time.Now().UTC().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if credential.IDToken != "" {
		claims, parseErr := codexauth.ParseJWTToken(credential.IDToken)
		if parseErr != nil {
			return CodexCredential{}, fmt.Errorf("parse ID token: %w", parseErr)
		}
		if accountID := strings.TrimSpace(claims.GetAccountID()); accountID != "" {
			credential.AccountID = accountID
		}
		if email := strings.TrimSpace(claims.GetUserEmail()); email != "" {
			credential.Email = email
		}
	}
	if strings.TrimSpace(credential.AccessToken) == "" {
		return CodexCredential{}, fmt.Errorf("token response has no access token")
	}
	return credential, nil
}

func tokenEndpointErrorCode(body []byte) string {
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	for _, value := range []string{payload.Error, payload.Code} {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && len(value) <= 64 && !strings.ContainsAny(value, "\r\n\x00") {
			return value
		}
	}
	return ""
}

func clientWithoutRedirects(source *http.Client) *http.Client {
	if source == nil {
		source = &http.Client{}
	}
	clone := *source
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

func validateCredential(credential CodexCredential) error {
	if strings.ToLower(strings.TrimSpace(credential.Type)) != ProviderCodex {
		return fmt.Errorf("credential type must be codex")
	}
	if strings.TrimSpace(credential.AccessToken) == "" {
		return fmt.Errorf("credential access_token is required")
	}
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return fmt.Errorf("credential refresh_token is required")
	}
	if strings.TrimSpace(credential.AccountID) == "" {
		return fmt.Errorf("credential account_id is required")
	}
	return nil
}

func validateTimestamp(field, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("credential %s must be RFC3339", field)
	}
	return nil
}

func constantTimeEqual(left, right string) bool {
	if left == "" || len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func credentialFromAuth(auth *cliproxyauth.Auth) (CodexCredential, error) {
	if auth == nil {
		return CodexCredential{}, fmt.Errorf("codex auth is required")
	}
	stringMetadata := func(key string) string {
		value, _ := auth.Metadata[key].(string)
		return strings.TrimSpace(value)
	}
	credential := CodexCredential{
		Type:         ProviderCodex,
		IDToken:      stringMetadata("id_token"),
		AccessToken:  stringMetadata("access_token"),
		RefreshToken: stringMetadata("refresh_token"),
		AccountID:    stringMetadata("account_id"),
		Email:        stringMetadata("email"),
		Expire:       stringMetadata("expired"),
		LastRefresh:  stringMetadata("last_refresh"),
	}
	if err := validateCredential(credential); err != nil {
		return CodexCredential{}, err
	}
	return credential, nil
}

var _ cliproxyauth.ProviderExecutor = (*CodexHTTPExecutor)(nil)
var _ HTTPExecutor = (*CodexHTTPExecutor)(nil)
