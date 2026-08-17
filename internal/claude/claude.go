// Package claude owns GPT-Load's Claude subscription contract and isolates the
// embedded CLIProxyAPI bridge from the rest of the application.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

const (
	Provider    = "claude"
	RedirectURI = cpaembedded.ClaudeRedirectURI
)

var (
	ErrInvalidState                = errors.New("claude oauth state mismatch")
	ErrCredentialIdentityChanged   = errors.New("refreshed claude credential identity changed")
	ErrOrganizationIdentityChanged = errors.New("refreshed claude organization identity changed")
)

// Credential is GPT-Load's encrypted, durable Claude credential schema.
type Credential struct {
	Type             string   `json:"type"`
	IDToken          string   `json:"id_token,omitempty"`
	AccessToken      string   `json:"access_token"`
	RefreshToken     string   `json:"refresh_token"`
	LastRefresh      string   `json:"last_refresh,omitempty"`
	Email            string   `json:"email,omitempty"`
	AccountUUID      string   `json:"account_uuid"`
	OrganizationUUID string   `json:"organization_uuid,omitempty"`
	OrganizationName string   `json:"organization_name,omitempty"`
	DeviceIDs        []string `json:"claude_device_ids"`
	Expire           string   `json:"expired,omitempty"`
}

func ParseCredentialJSON(raw []byte) (Credential, error) {
	value, err := cpaembedded.ParseClaudeCredentialJSON(raw)
	if err != nil {
		return Credential{}, err
	}
	return credentialFromBridge(value), nil
}

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

func CredentialExpiresAt(value Credential) (time.Time, bool) {
	return cpaembedded.ClaudeCredentialExpiresAt(credentialToBridge(value))
}

func (value Credential) SecretValues() []string {
	values := []string{
		value.AccessToken, value.RefreshToken, value.IDToken,
		value.AccountUUID, value.OrganizationUUID, value.Email,
	}
	values = append(values, value.DeviceIDs...)
	return values
}

type BrowserAuthorization struct {
	AuthorizationURL string
	State            string
	CodeVerifier     string
	CodeChallenge    string
	ExpiresAt        time.Time
}

type BrowserAuthorizationCompletion struct {
	ExpectedState string
	ReturnedState string
	Code          string
	CodeVerifier  string
}

type TokenEndpointError struct {
	StatusCode int
	Code       string
}

func (e *TokenEndpointError) Error() string {
	return fmt.Sprintf("Claude token endpoint returned status %d", e.StatusCode)
}

type UpstreamHTTPError struct {
	StatusCode int
}

func (err *UpstreamHTTPError) Error() string {
	return fmt.Sprintf("Claude upstream returned status %d", err.StatusCode)
}

func IsDefinitiveRefreshRejection(code string) bool {
	return cpaembedded.IsDefinitiveRefreshRejection(code)
}

func BeginBrowserAuthorization() (BrowserAuthorization, error) {
	value, err := cpaembedded.BeginClaudeBrowserAuthorization()
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

func CompleteBrowserAuthorization(
	ctx context.Context,
	completion BrowserAuthorizationCompletion,
) (Credential, error) {
	value, err := cpaembedded.CompleteClaudeBrowserAuthorization(ctx, cpaembedded.BrowserAuthorizationCompletion{
		ExpectedState: completion.ExpectedState,
		ReturnedState: completion.ReturnedState,
		Code:          completion.Code,
		CodeVerifier:  completion.CodeVerifier,
	}, cpaembedded.ClaudeOptions{})
	if err != nil {
		return Credential{}, normalizeAuthorizationError(err)
	}
	return credentialFromBridge(value), nil
}

func RefreshCredentialOnce(ctx context.Context, current Credential) (Credential, error) {
	value, err := cpaembedded.RefreshClaudeCredentialOnce(ctx, credentialToBridge(current), cpaembedded.ClaudeOptions{})
	if err != nil {
		return Credential{}, normalizeAuthorizationError(err)
	}
	return credentialFromBridge(value), nil
}

func credentialFromBridge(value cpaembedded.ClaudeCredential) Credential {
	return Credential{
		Type: value.Type, IDToken: value.IDToken,
		AccessToken: value.AccessToken, RefreshToken: value.RefreshToken,
		LastRefresh: value.LastRefresh, Email: value.Email, AccountUUID: value.AccountUUID,
		OrganizationUUID: value.OrganizationUUID, OrganizationName: value.OrganizationName,
		DeviceIDs: append([]string(nil), value.DeviceIDs...), Expire: value.Expire,
	}
}

func credentialToBridge(value Credential) cpaembedded.ClaudeCredential {
	return cpaembedded.ClaudeCredential{
		Type: value.Type, IDToken: value.IDToken,
		AccessToken: value.AccessToken, RefreshToken: value.RefreshToken,
		LastRefresh: value.LastRefresh, Email: value.Email, AccountUUID: value.AccountUUID,
		OrganizationUUID: value.OrganizationUUID, OrganizationName: value.OrganizationName,
		DeviceIDs: append([]string(nil), value.DeviceIDs...), Expire: value.Expire,
	}
}

func normalizeAuthorizationError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, cpaembedded.ErrClaudeInvalidState) {
		return ErrInvalidState
	}
	if errors.Is(err, cpaembedded.ErrClaudeCredentialIdentityChanged) {
		return ErrCredentialIdentityChanged
	}
	if errors.Is(err, cpaembedded.ErrClaudeOrganizationIdentityChanged) {
		return ErrOrganizationIdentityChanged
	}
	var tokenErr *cpaembedded.TokenEndpointError
	if errors.As(err, &tokenErr) {
		return &TokenEndpointError{StatusCode: tokenErr.StatusCode, Code: strings.TrimSpace(tokenErr.Code)}
	}
	var upstream *cpaembedded.ClaudeUpstreamHTTPError
	if errors.As(err, &upstream) {
		return &UpstreamHTTPError{StatusCode: upstream.StatusCode}
	}
	return err
}
