package embedded

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	antigravityauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/antigravity"
)

const (
	ProviderAntigravity      = "antigravity"
	AntigravityRedirectURI   = "http://localhost:51121/oauth-callback"
	antigravityLoginTimeout  = 5 * time.Minute
	antigravityExecutionBase = "https://daily-cloudcode-pa.googleapis.com"
)

// AntigravityCredential is the strict canonical credential retained by
// GPT-Load. account_id comes from Google UserInfo and never from a CPA file
// name or project identifier.
type AntigravityCredential struct {
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

// SecretValues returns every credential value that must be redacted before a
// provider error can cross the embedded boundary.
func (credential AntigravityCredential) SecretValues() []string {
	return []string{
		credential.AccessToken,
		credential.RefreshToken,
		credential.AccountID,
		credential.Email,
		credential.ProjectID,
	}
}

// ParseAntigravityCredentialJSON accepts only GPT-Load's canonical schema.
// CPA's native JSON lacks account_id and must first pass through the explicit
// importing/enrichment flow.
func ParseAntigravityCredentialJSON(raw []byte) (AntigravityCredential, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return AntigravityCredential{}, fmt.Errorf("credential JSON size is invalid")
	}
	allowed := map[string]struct{}{
		"type": {}, "access_token": {}, "refresh_token": {}, "account_id": {},
		"email": {}, "project_id": {}, "expires_in": {}, "timestamp": {},
		"expired": {}, "last_refresh": {},
	}
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return AntigravityCredential{}, err
	}
	var credential AntigravityCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return AntigravityCredential{}, fmt.Errorf("decode credential: %w", err)
	}
	normalizeAntigravityCredential(&credential)
	if err := validateAntigravityCredential(credential); err != nil {
		return AntigravityCredential{}, err
	}
	return credential, nil
}

// AntigravityCredentialExpiresAt returns the canonical access-token expiry.
func AntigravityCredentialExpiresAt(credential AntigravityCredential) (time.Time, bool) {
	if strings.TrimSpace(credential.Expire) == "" {
		return time.Time{}, false
	}
	value, err := time.Parse(time.RFC3339, credential.Expire)
	return value, err == nil
}

func normalizeAntigravityCredential(credential *AntigravityCredential) {
	credential.Type = strings.ToLower(strings.TrimSpace(credential.Type))
	credential.AccessToken = strings.TrimSpace(credential.AccessToken)
	credential.RefreshToken = strings.TrimSpace(credential.RefreshToken)
	credential.AccountID = strings.TrimSpace(credential.AccountID)
	credential.Email = strings.TrimSpace(credential.Email)
	credential.ProjectID = strings.TrimSpace(credential.ProjectID)
	credential.Expire = strings.TrimSpace(credential.Expire)
	credential.LastRefresh = strings.TrimSpace(credential.LastRefresh)
}

func validateAntigravityCredential(credential AntigravityCredential) error {
	if credential.Type != ProviderAntigravity {
		return fmt.Errorf("credential type must be antigravity")
	}
	for field, value := range map[string]string{
		"access_token":  credential.AccessToken,
		"refresh_token": credential.RefreshToken,
		"account_id":    credential.AccountID,
		"email":         credential.Email,
		"project_id":    credential.ProjectID,
	} {
		if value == "" {
			return fmt.Errorf("credential %s is required", field)
		}
		if len(value) > 16*1024 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("credential %s is invalid", field)
		}
	}
	if credential.ExpiresIn < 0 || credential.Timestamp < 0 {
		return fmt.Errorf("credential token lifetime is invalid")
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
	return nil
}

var _ = antigravityauth.CallbackPort
