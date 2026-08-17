package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
)

const (
	ProviderClaude     = "claude"
	ClaudeRedirectURI  = claudeauth.RedirectURI
	ClaudeBootstrapURL = "https://api.anthropic.com/api/claude_cli/bootstrap"
	ClaudeUsageURL     = "https://api.anthropic.com/api/oauth/usage"
	claudeLoginTimeout = 5 * time.Minute
)

var (
	ErrClaudeInvalidState                = errors.New("claude oauth state mismatch")
	ErrClaudeCredentialIdentityChanged   = errors.New("refreshed claude credential identity changed")
	ErrClaudeOrganizationIdentityChanged = errors.New("refreshed claude organization identity changed")
)

// ClaudeCredential is the canonical, execution-only Claude credential schema.
type ClaudeCredential struct {
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

// ClaudeOptions supplies testable OAuth transport boundaries without changing
// the production OAuth identity or redirect URI.
type ClaudeOptions struct {
	TokenURL     string
	ProfileURL   string
	RolesURL     string
	BootstrapURL string
	UsageURL     string
	HTTPClient   *http.Client
	Now          func() time.Time
}

func ParseClaudeCredentialJSON(raw []byte) (ClaudeCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return ClaudeCredential{}, fmt.Errorf("credential JSON size is invalid")
	}
	allowed := map[string]struct{}{
		"type": {}, "id_token": {}, "access_token": {}, "refresh_token": {},
		"last_refresh": {}, "email": {}, "account_uuid": {},
		"organization_uuid": {}, "organization_name": {},
		"claude_device_ids": {}, "expired": {},
	}
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return ClaudeCredential{}, err
	}
	var credential ClaudeCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return ClaudeCredential{}, fmt.Errorf("decode credential: %w", err)
	}
	normalizeClaudeCredential(&credential)
	if len(credential.DeviceIDs) == 0 {
		deviceIDs, err := claudeauth.GenerateDeviceIDPool()
		if err != nil {
			return ClaudeCredential{}, err
		}
		credential.DeviceIDs = deviceIDs
	}
	if err := validateClaudeCredential(credential); err != nil {
		return ClaudeCredential{}, err
	}
	credential.DeviceIDs = append([]string(nil), credential.DeviceIDs...)
	return credential, nil
}

func validateClaudeCredentialObject(raw []byte, allowed map[string]struct{}) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return fmt.Errorf("credential must be one JSON object")
	}
	seen := make(map[string]struct{}, len(allowed))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("credential must be one JSON object")
		}
		field, ok := token.(string)
		if !ok {
			return fmt.Errorf("credential must be one JSON object")
		}
		if _, duplicate := seen[field]; duplicate {
			return fmt.Errorf("credential field %q is duplicated", field)
		}
		if _, ok := allowed[field]; !ok {
			return fmt.Errorf("credential field %q is not allowed", field)
		}
		seen[field] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("decode credential field %q: %w", field, err)
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return fmt.Errorf("credential must be one JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("credential must contain exactly one JSON object")
	}
	return nil
}

func ClaudeCredentialExpiresAt(credential ClaudeCredential) (time.Time, bool) {
	if credential.Expire == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, credential.Expire)
	return parsed, err == nil
}

func BeginClaudeBrowserAuthorization() (BrowserAuthorization, error) {
	pkce, err := claudeauth.GeneratePKCECodes()
	if err != nil {
		return BrowserAuthorization{}, fmt.Errorf("generate Claude PKCE: %w", err)
	}
	state, err := misc.GenerateRandomState()
	if err != nil {
		return BrowserAuthorization{}, fmt.Errorf("generate Claude OAuth state: %w", err)
	}
	authorizationURL, returnedState, err := claudeauth.NewClaudeAuth(&internalconfig.Config{}).GenerateAuthURL(state, pkce)
	if err != nil {
		return BrowserAuthorization{}, fmt.Errorf("generate Claude authorization URL: %w", err)
	}
	return BrowserAuthorization{
		AuthorizationURL: authorizationURL,
		State:            returnedState,
		CodeVerifier:     pkce.CodeVerifier,
		CodeChallenge:    pkce.CodeChallenge,
		ExpiresAt:        time.Now().UTC().Add(claudeLoginTimeout),
	}, nil
}

func CompleteClaudeBrowserAuthorization(
	ctx context.Context,
	completion BrowserAuthorizationCompletion,
	options ClaudeOptions,
) (ClaudeCredential, error) {
	return completeClaudeBrowserAuthorization(ctx, completion, options)
}

func RefreshClaudeCredentialOnce(
	ctx context.Context,
	current ClaudeCredential,
	options ClaudeOptions,
) (ClaudeCredential, error) {
	return refreshClaudeCredentialOnce(ctx, current, options)
}

func normalizeClaudeCredential(credential *ClaudeCredential) {
	credential.Type = strings.ToLower(strings.TrimSpace(credential.Type))
	credential.IDToken = strings.TrimSpace(credential.IDToken)
	credential.AccessToken = strings.TrimSpace(credential.AccessToken)
	credential.RefreshToken = strings.TrimSpace(credential.RefreshToken)
	credential.LastRefresh = strings.TrimSpace(credential.LastRefresh)
	credential.Email = strings.TrimSpace(credential.Email)
	credential.AccountUUID = strings.TrimSpace(credential.AccountUUID)
	credential.OrganizationUUID = strings.TrimSpace(credential.OrganizationUUID)
	credential.OrganizationName = strings.TrimSpace(credential.OrganizationName)
	credential.Expire = strings.TrimSpace(credential.Expire)
}

func validateClaudeCredential(credential ClaudeCredential) error {
	if credential.Type != ProviderClaude {
		return fmt.Errorf("credential type must be claude")
	}
	if credential.AccessToken == "" {
		return fmt.Errorf("credential access_token is required")
	}
	if credential.RefreshToken == "" {
		return fmt.Errorf("credential refresh_token is required")
	}
	if credential.AccountUUID == "" {
		return fmt.Errorf("credential account_uuid is required")
	}
	if !claudeauth.HasCanonicalDeviceIDPool(credential.DeviceIDs) {
		return fmt.Errorf("credential claude_device_ids must contain one canonical device ID")
	}
	if credential.Expire == "" {
		return fmt.Errorf("credential expired is required")
	}
	if err := validateTimestamp("expired", credential.Expire); err != nil {
		return err
	}
	if err := validateTimestamp("last_refresh", credential.LastRefresh); err != nil {
		return err
	}
	for field, value := range map[string]string{
		"account_uuid": credential.AccountUUID, "organization_uuid": credential.OrganizationUUID,
		"email": credential.Email, "organization_name": credential.OrganizationName,
	} {
		if len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("credential %s is invalid", field)
		}
	}
	return nil
}
