// Package codex owns GPT-Load's Codex subscription contract and isolates the
// embedded CLIProxyAPI bridge from the rest of the application.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

// Provider is the stable provider value stored in Codex credentials.
const Provider = "codex"

var (
	ErrInvalidState              = errors.New("codex oauth state mismatch")
	ErrCredentialIdentityChanged = errors.New("refreshed codex credential identity changed")
)

// Credential is GPT-Load's encrypted, durable Codex credential schema. Its
// JSON shape deliberately remains compatible with CPA's exported auth file.
type Credential struct {
	Type         string `json:"type"`
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
	Email        string `json:"email,omitempty"`
	Expire       string `json:"expired,omitempty"`
	LastRefresh  string `json:"last_refresh,omitempty"`
}

// ParseCredentialJSON validates and normalizes one CPA-compatible Codex auth
// object without exposing CPA types to callers.
func ParseCredentialJSON(raw []byte) (Credential, error) {
	parsed, err := cpaembedded.ParseCodexCredentialJSON(raw)
	if err != nil {
		return Credential{}, err
	}
	return credentialFromBridge(parsed), nil
}

// MarshalCredential returns the canonical persisted representation.
func MarshalCredential(value Credential) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseCredentialJSON(raw)
	clear(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(parsed)
}

// CredentialExpiresAt returns the best known access-token expiration.
func CredentialExpiresAt(value Credential) (time.Time, bool) {
	return cpaembedded.CodexCredentialExpiresAt(credentialToBridge(value))
}

// SecretValues returns the exact sensitive token values used for redaction.
func (value Credential) SecretValues() []string {
	return []string{value.AccessToken, value.RefreshToken, value.IDToken}
}

// BrowserAuthorization contains one short-lived OAuth PKCE challenge.
type BrowserAuthorization struct {
	AuthorizationURL string
	State            string
	CodeVerifier     string
	CodeChallenge    string
	ExpiresAt        time.Time
}

// BrowserAuthorizationCompletion contains the callback values and Stage-bound verifier.
type BrowserAuthorizationCompletion struct {
	ExpectedState string
	ReturnedState string
	Code          string
	CodeVerifier  string
}

// Model identifies one model available to the subscription account.
type Model struct {
	ID string `json:"id"`
}

// AccountObservation carries the provider payload used by the tolerant control-plane parser.
type AccountObservation struct {
	Payload []byte
	Header  http.Header
}

// UpstreamHTTPError preserves only the status needed for stable control-plane
// classification. Upstream bodies never cross the embedded boundary.
type UpstreamHTTPError struct {
	Operation  string
	StatusCode int
}

func (e *UpstreamHTTPError) Error() string {
	return fmt.Sprintf("codex %s upstream returned status %d", e.Operation, e.StatusCode)
}

// TokenEndpointError retains only the bounded OAuth classification needed by
// the credential lifecycle. Provider response bodies are never exposed.
type TokenEndpointError struct {
	StatusCode int
	Code       string
}

func (e *TokenEndpointError) Error() string {
	return fmt.Sprintf("token endpoint returned status %d", e.StatusCode)
}

// IsDefinitiveRefreshRejection reports whether OAuth requires new browser authorization.
func IsDefinitiveRefreshRejection(code string) bool {
	return cpaembedded.IsDefinitiveRefreshRejection(code)
}

// BeginBrowserAuthorization creates an OAuth challenge without opening a browser or listener.
func BeginBrowserAuthorization() (BrowserAuthorization, error) {
	value, err := cpaembedded.BeginCodexBrowserAuthorization()
	if err != nil {
		return BrowserAuthorization{}, normalizeAuthorizationError(err)
	}
	return BrowserAuthorization{
		AuthorizationURL: value.AuthorizationURL,
		State:            value.State,
		CodeVerifier:     value.CodeVerifier,
		CodeChallenge:    value.CodeChallenge,
		ExpiresAt:        value.ExpiresAt,
	}, nil
}

// CompleteBrowserAuthorization exchanges one verified callback for a durable credential.
func CompleteBrowserAuthorization(
	ctx context.Context,
	completion BrowserAuthorizationCompletion,
) (Credential, error) {
	options, err := codexOptions(ctx)
	if err != nil {
		return Credential{}, err
	}
	value, err := cpaembedded.CompleteCodexBrowserAuthorization(ctx, cpaembedded.BrowserAuthorizationCompletion{
		ExpectedState: completion.ExpectedState,
		ReturnedState: completion.ReturnedState,
		Code:          completion.Code,
		CodeVerifier:  completion.CodeVerifier,
	}, options)
	if err != nil {
		return Credential{}, normalizeAuthorizationError(err)
	}
	return credentialFromBridge(value), nil
}

// RefreshCredentialOnce performs exactly one token refresh without persistence or retries.
func RefreshCredentialOnce(ctx context.Context, current Credential) (Credential, error) {
	options, err := codexOptions(ctx)
	if err != nil {
		return Credential{}, err
	}
	value, err := cpaembedded.RefreshCodexCredentialOnce(ctx, credentialToBridge(current), options)
	if err != nil {
		return Credential{}, normalizeAuthorizationError(err)
	}
	return credentialFromBridge(value), nil
}

// ListModels returns the models visible to one Codex subscription account.
func ListModels(ctx context.Context, credential Credential) ([]Model, error) {
	options, err := codexOptions(ctx)
	if err != nil {
		return nil, err
	}
	values, err := cpaembedded.ListCodexModels(ctx, credentialToBridge(credential), "", options)
	if err != nil {
		return nil, err
	}
	result := make([]Model, len(values))
	for index, value := range values {
		result[index] = Model{ID: value.ID}
	}
	return result, nil
}

// ObserveAccount retrieves account entitlement and quota metadata.
func ObserveAccount(ctx context.Context, credential Credential) (AccountObservation, error) {
	options, err := codexOptions(ctx)
	if err != nil {
		return AccountObservation{}, err
	}
	value, err := cpaembedded.ObserveCodexAccount(ctx, credentialToBridge(credential), "", options)
	if err != nil {
		return AccountObservation{}, normalizeUpstreamError(err)
	}
	return AccountObservation{Payload: append([]byte(nil), value.Payload...), Header: value.Header.Clone()}, nil
}

// ObserveResetCredits retrieves the available reset-credit detail payload.
func ObserveResetCredits(ctx context.Context, credential Credential) (AccountObservation, error) {
	options, err := codexOptions(ctx)
	if err != nil {
		return AccountObservation{}, err
	}
	value, err := cpaembedded.ObserveCodexResetCredits(ctx, credentialToBridge(credential), "", options)
	if err != nil {
		return AccountObservation{}, normalizeUpstreamError(err)
	}
	return AccountObservation{Payload: append([]byte(nil), value.Payload...), Header: value.Header.Clone()}, nil
}

// ConsumeResetCredit consumes the next available credit with a caller-owned,
// durable upstream idempotency identity.
func ConsumeResetCredit(ctx context.Context, credential Credential, redeemRequestID string) (AccountObservation, error) {
	options, err := codexOptions(ctx)
	if err != nil {
		return AccountObservation{}, err
	}
	value, err := cpaembedded.ConsumeCodexResetCredit(ctx, credentialToBridge(credential), "", redeemRequestID, options)
	if err != nil {
		return AccountObservation{}, normalizeUpstreamError(err)
	}
	return AccountObservation{Payload: append([]byte(nil), value.Payload...), Header: value.Header.Clone()}, nil
}

func codexOptions(ctx context.Context) (cpaembedded.Options, error) {
	client, err := subscriptionruntime.HTTPClient(ctx)
	if err != nil {
		return cpaembedded.Options{}, err
	}
	return cpaembedded.Options{HTTPClient: client}, nil
}

func normalizeUpstreamError(err error) error {
	var upstream *cpaembedded.UpstreamHTTPError
	if errors.As(err, &upstream) {
		return &UpstreamHTTPError{Operation: upstream.Operation, StatusCode: upstream.StatusCode}
	}
	return err
}

// ExecuteRequest is the canonical request accepted by the embedded CPA bridge.
type ExecuteRequest struct {
	Model                string
	Payload              []byte
	Format               string
	RequestPath          string
	Headers              http.Header
	OriginalRequest      []byte
	ProxyURL             string
	ProxyFromEnvironment bool
}

// ExecuteResponse is one converted non-streaming bridge response.
type ExecuteResponse struct {
	Payload                []byte
	Headers                http.Header
	AppliedReasoningEffort string
	UpstreamRequestPath    string
}

// ExecuteStreamResponse contains converted streaming chunks and response metadata.
type ExecuteStreamResponse struct {
	Headers                http.Header
	Chunks                 <-chan ExecuteStreamChunk
	AppliedReasoningEffort string
	UpstreamRequestPath    string
}

// ExecuteStreamChunk contains one converted payload or terminal bridge error.
type ExecuteStreamChunk struct {
	Payload []byte
	Err     error
}

// Executor is the isolated CPA execution surface consumed by GPT-Load.
type Executor interface {
	Execute(context.Context, string, Credential, ExecuteRequest) (ExecuteResponse, error)
	CountTokens(context.Context, string, Credential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStream(context.Context, string, Credential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type executor struct {
	bridge cpaembedded.HTTPExecutor
}

// NewExecutor creates the production Codex bridge executor.
func NewExecutor() Executor {
	return &executor{bridge: cpaembedded.NewCodexHTTPExecutor()}
}

func (e *executor) Execute(
	ctx context.Context,
	credentialID string,
	credential Credential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	response, err := e.bridge.ExecuteCanonical(
		ctx,
		credentialID,
		credentialToBridge(credential),
		executeRequestToBridge(request),
	)
	return ExecuteResponse{
		Payload:                append([]byte(nil), response.Payload...),
		Headers:                response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
		UpstreamRequestPath:    response.UpstreamRequestPath,
	}, err
}

func (e *executor) CountTokens(
	ctx context.Context,
	credentialID string,
	credential Credential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	response, err := e.bridge.CountTokensCanonical(
		ctx,
		credentialID,
		credentialToBridge(credential),
		executeRequestToBridge(request),
	)
	return ExecuteResponse{
		Payload:                append([]byte(nil), response.Payload...),
		Headers:                response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
		UpstreamRequestPath:    response.UpstreamRequestPath,
	}, err
}

func (e *executor) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential Credential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	response, err := e.bridge.ExecuteStreamCanonical(
		ctx,
		credentialID,
		credentialToBridge(credential),
		executeRequestToBridge(request),
	)
	if response == nil {
		return nil, err
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Err: chunk.Err}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &ExecuteStreamResponse{
		Headers:                response.Headers.Clone(),
		Chunks:                 chunks,
		AppliedReasoningEffort: response.AppliedReasoningEffort,
		UpstreamRequestPath:    response.UpstreamRequestPath,
	}, err
}

func executeRequestToBridge(value ExecuteRequest) cpaembedded.ExecuteRequest {
	return cpaembedded.ExecuteRequest{
		Model:                value.Model,
		Payload:              append([]byte(nil), value.Payload...),
		Format:               value.Format,
		RequestPath:          value.RequestPath,
		Headers:              value.Headers.Clone(),
		OriginalRequest:      append([]byte(nil), value.OriginalRequest...),
		ProxyURL:             value.ProxyURL,
		ProxyFromEnvironment: value.ProxyFromEnvironment,
	}
}

func credentialFromBridge(value cpaembedded.CodexCredential) Credential {
	return Credential{
		Type:         value.Type,
		IDToken:      value.IDToken,
		AccessToken:  value.AccessToken,
		RefreshToken: value.RefreshToken,
		AccountID:    value.AccountID,
		Email:        value.Email,
		Expire:       value.Expire,
		LastRefresh:  value.LastRefresh,
	}
}

func credentialToBridge(value Credential) cpaembedded.CodexCredential {
	return cpaembedded.CodexCredential{
		Type:         value.Type,
		IDToken:      value.IDToken,
		AccessToken:  value.AccessToken,
		RefreshToken: value.RefreshToken,
		AccountID:    value.AccountID,
		Email:        value.Email,
		Expire:       value.Expire,
		LastRefresh:  value.LastRefresh,
	}
}

func normalizeAuthorizationError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, cpaembedded.ErrInvalidState) {
		return ErrInvalidState
	}
	if errors.Is(err, cpaembedded.ErrCredentialIdentityChanged) {
		return ErrCredentialIdentityChanged
	}
	var tokenErr *cpaembedded.TokenEndpointError
	if errors.As(err, &tokenErr) {
		return &TokenEndpointError{StatusCode: tokenErr.StatusCode, Code: strings.TrimSpace(tokenErr.Code)}
	}
	return err
}

var _ Executor = (*executor)(nil)
