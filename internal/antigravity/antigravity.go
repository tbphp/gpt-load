// Package antigravity owns GPT-Load's Antigravity subscription contract and
// prevents CPA internal types from escaping into the application.
package antigravity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

const (
	Provider    = cpaembedded.ProviderAntigravity
	RedirectURI = cpaembedded.AntigravityRedirectURI
)

var (
	ErrInvalidState              = errors.New("antigravity oauth state mismatch")
	ErrCredentialIdentityChanged = errors.New("refreshed antigravity credential identity changed")
)

// Credential is GPT-Load's encrypted canonical Antigravity credential.
type Credential struct {
	Type         string `json:"type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
	Email        string `json:"email"`
	ProjectID    string `json:"project_id"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	Timestamp    int64  `json:"timestamp,omitempty"`
	Expire       string `json:"expired"`
	LastRefresh  string `json:"last_refresh,omitempty"`
}

func ParseCredentialJSON(raw []byte) (Credential, error) {
	value, err := cpaembedded.ParseAntigravityCredentialJSON(raw)
	if err != nil {
		return Credential{}, normalizeError(err)
	}
	return credentialFromBridge(value), nil
}

func MarshalCredential(value Credential) ([]byte, error) {
	raw, err := cpaembedded.MarshalAntigravityCredential(credentialToBridge(value))
	if err != nil {
		return nil, normalizeError(err)
	}
	return raw, nil
}

func CredentialExpiresAt(value Credential) (time.Time, bool) {
	return cpaembedded.AntigravityCredentialExpiresAt(credentialToBridge(value))
}

func (value Credential) SecretValues() []string {
	return credentialToBridge(value).SecretValues()
}

type BrowserAuthorization struct {
	AuthorizationURL string
	State            string
	ExpiresAt        time.Time
}

type BrowserAuthorizationCompletion struct {
	ExpectedState string
	ReturnedState string
	Code          string
}

type TokenEndpointError struct {
	StatusCode int
	Code       string
}

func (err *TokenEndpointError) Error() string {
	if err == nil {
		return "Antigravity token endpoint failed"
	}
	return fmt.Sprintf("Antigravity token endpoint returned status %d", err.StatusCode)
}

func (err *TokenEndpointError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

type UpstreamHTTPError struct {
	Operation  string
	StatusCode int
}

func (err *UpstreamHTTPError) Error() string {
	if err == nil {
		return "Antigravity upstream request failed"
	}
	return fmt.Sprintf("Antigravity %s endpoint returned status %d", err.Operation, err.StatusCode)
}

func (err *UpstreamHTTPError) HTTPStatusCode() int {
	if err == nil {
		return 0
	}
	return err.StatusCode
}

type Model struct {
	ID              string
	DisplayName     string
	MaxTokens       int64
	MaxOutputTokens int64
}

type GoogleOneAICredit struct {
	Amount        float64
	MinimumAmount float64
}

type QuotaBucket struct {
	ID                string
	DisplayName       string
	Window            string
	ResetTime         string
	RemainingFraction *float64
}

type QuotaGroup struct {
	DisplayName string
	Buckets     []QuotaBucket
}

type AccountObservation struct {
	PlanID             string
	CurrentTierID      string
	GoogleOneAICredits *GoogleOneAICredit
	QuotaGroups        []QuotaGroup
	AccountObserved    bool
	QuotaObserved      bool
	IncompleteSources  []string
}

func IsDefinitiveRefreshRejection(code string) bool {
	return cpaembedded.IsDefinitiveAntigravityRefreshRejection(code)
}

func BeginBrowserAuthorization() (BrowserAuthorization, error) {
	value, err := cpaembedded.BeginAntigravityBrowserAuthorization()
	if err != nil {
		return BrowserAuthorization{}, normalizeError(err)
	}
	return BrowserAuthorization{
		AuthorizationURL: value.AuthorizationURL, State: value.State, ExpiresAt: value.ExpiresAt,
	}, nil
}

func CompleteBrowserAuthorization(
	ctx context.Context,
	completion BrowserAuthorizationCompletion,
) (Credential, error) {
	value, err := cpaembedded.CompleteAntigravityBrowserAuthorization(ctx, cpaembedded.BrowserAuthorizationCompletion{
		ExpectedState: completion.ExpectedState, ReturnedState: completion.ReturnedState, Code: completion.Code,
	}, cpaembedded.AntigravityOptions{})
	if err != nil {
		return Credential{}, normalizeError(err)
	}
	return credentialFromBridge(value), nil
}

func RefreshCredentialOnce(ctx context.Context, current Credential) (Credential, error) {
	value, err := cpaembedded.RefreshAntigravityCredentialOnce(ctx, credentialToBridge(current), cpaembedded.AntigravityOptions{})
	if err != nil {
		return Credential{}, normalizeError(err)
	}
	return credentialFromBridge(value), nil
}

func ImportCredential(ctx context.Context, raw []byte) (Credential, error) {
	value, err := cpaembedded.ImportAntigravityCredential(ctx, raw, cpaembedded.AntigravityOptions{})
	if err != nil {
		return Credential{}, normalizeError(err)
	}
	return credentialFromBridge(value), nil
}

func ListModels(ctx context.Context, credential Credential) ([]Model, error) {
	values, err := cpaembedded.DiscoverAntigravityModels(ctx, credentialToBridge(credential), cpaembedded.AntigravityOptions{})
	if err != nil {
		return nil, normalizeError(err)
	}
	result := make([]Model, 0, len(values))
	for _, value := range values {
		result = append(result, Model{
			ID: value.ID, DisplayName: value.DisplayName,
			MaxTokens: value.MaxTokens, MaxOutputTokens: value.MaxOutputTokens,
		})
	}
	return result, nil
}

func ObserveAccount(ctx context.Context, credential Credential) (AccountObservation, error) {
	value, err := cpaembedded.ObserveAntigravityAccount(ctx, credentialToBridge(credential), cpaembedded.AntigravityOptions{})
	if err != nil {
		return AccountObservation{}, normalizeError(err)
	}
	result := AccountObservation{
		PlanID: value.PlanID, CurrentTierID: value.CurrentTierID,
		AccountObserved: value.AccountObserved, QuotaObserved: value.QuotaObserved,
		IncompleteSources: append([]string(nil), value.IncompleteSources...),
	}
	if value.GoogleOneAICredits != nil {
		result.GoogleOneAICredits = &GoogleOneAICredit{
			Amount: value.GoogleOneAICredits.Amount, MinimumAmount: value.GoogleOneAICredits.MinimumAmount,
		}
	}
	for _, group := range value.QuotaGroups {
		mapped := QuotaGroup{DisplayName: group.DisplayName, Buckets: make([]QuotaBucket, 0, len(group.Buckets))}
		for _, bucket := range group.Buckets {
			mapped.Buckets = append(mapped.Buckets, QuotaBucket{
				ID: bucket.ID, DisplayName: bucket.DisplayName, Window: bucket.Window,
				ResetTime: bucket.ResetTime, RemainingFraction: cloneFloat64(bucket.RemainingFraction),
			})
		}
		result.QuotaGroups = append(result.QuotaGroups, mapped)
	}
	return result, nil
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func credentialFromBridge(value cpaembedded.AntigravityCredential) Credential {
	return Credential{
		Type: value.Type, AccessToken: value.AccessToken, RefreshToken: value.RefreshToken,
		AccountID: value.AccountID, Email: value.Email, ProjectID: value.ProjectID,
		ExpiresIn: value.ExpiresIn, Timestamp: value.Timestamp, Expire: value.Expire,
		LastRefresh: value.LastRefresh,
	}
}

func credentialToBridge(value Credential) cpaembedded.AntigravityCredential {
	return cpaembedded.AntigravityCredential{
		Type: value.Type, AccessToken: value.AccessToken, RefreshToken: value.RefreshToken,
		AccountID: value.AccountID, Email: value.Email, ProjectID: value.ProjectID,
		ExpiresIn: value.ExpiresIn, Timestamp: value.Timestamp, Expire: value.Expire,
		LastRefresh: value.LastRefresh,
	}
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, cpaembedded.ErrAntigravityInvalidState) {
		return ErrInvalidState
	}
	if errors.Is(err, cpaembedded.ErrAntigravityCredentialIdentityChanged) {
		return ErrCredentialIdentityChanged
	}
	var tokenErr *cpaembedded.AntigravityTokenEndpointError
	if errors.As(err, &tokenErr) {
		return &TokenEndpointError{StatusCode: tokenErr.StatusCode, Code: strings.TrimSpace(tokenErr.Code)}
	}
	var upstream *cpaembedded.AntigravityUpstreamHTTPError
	if errors.As(err, &upstream) {
		return &UpstreamHTTPError{Operation: strings.TrimSpace(upstream.Operation), StatusCode: upstream.StatusCode}
	}
	return err
}
