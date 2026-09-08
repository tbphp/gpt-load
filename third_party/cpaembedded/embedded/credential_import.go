package embedded

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	codexauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
)

// ImportCodexCredential 将导入字段补全为已有的持久凭据。
func ImportCodexCredential(ctx context.Context, raw []byte, options Options) (CodexCredential, error) {
	fields, err := credentialImportFields(raw, codexauth.ClientID)
	if err != nil {
		return CodexCredential{}, err
	}
	var original CodexCredential
	if err := json.Unmarshal(raw, &original); err != nil {
		return CodexCredential{}, fmt.Errorf("decode imported credential: %w", err)
	}
	// 先沿用原校验检查类型、时间及禁用的配置字段；占位值只用于校验。
	if strings.TrimSpace(original.AccessToken) == "" {
		fields["access_token"] = json.RawMessage(`"import-validation"`)
	}
	if strings.TrimSpace(original.AccountID) == "" {
		fields["account_id"] = json.RawMessage(`"import-validation"`)
	}
	probe, err := json.Marshal(fields)
	if err != nil {
		return CodexCredential{}, err
	}
	defer clear(probe)
	current, err := ParseCodexCredentialJSON(probe)
	if err != nil {
		return CodexCredential{}, err
	}
	current.AccessToken = strings.TrimSpace(original.AccessToken)
	current.AccountID = strings.TrimSpace(original.AccountID)
	if err := completeCodexImportClaims(&current); err != nil {
		return CodexCredential{}, err
	}
	if err := validateCredential(current); err == nil {
		return current, nil
	}
	// 只有不完整凭据需要在导入时刷新；完整凭据的刷新仍由调用方生命周期管理。
	refreshed, err := exchangeToken(ctx, url.Values{
		"client_id": {codexauth.ClientID}, "grant_type": {"refresh_token"},
		"refresh_token": {current.RefreshToken}, "scope": {"openid profile email"},
	}, options)
	if err != nil {
		return CodexCredential{}, fmt.Errorf("complete imported Codex credential: %w", err)
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = current.RefreshToken
	}
	if err := completeCodexImportClaims(&refreshed); err != nil {
		return CodexCredential{}, err
	}
	if current.AccountID != "" && refreshed.AccountID != "" && current.AccountID != refreshed.AccountID {
		return CodexCredential{}, ErrCredentialIdentityChanged
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = current.AccountID
	}
	if refreshed.Email == "" {
		refreshed.Email = current.Email
	}
	if refreshed.IDToken == "" {
		refreshed.IDToken = current.IDToken
	}
	if err := validateCredential(refreshed); err != nil {
		return CodexCredential{}, err
	}
	return refreshed, nil
}

// ImportClaudeCredential 将导入字段补全为已有的持久凭据。
func ImportClaudeCredential(ctx context.Context, raw []byte, options ClaudeOptions) (ClaudeCredential, error) {
	fields, err := credentialImportFields(raw, claudeauth.ClientID)
	if err != nil {
		return ClaudeCredential{}, err
	}
	clean, err := json.Marshal(fields)
	if err != nil {
		return ClaudeCredential{}, err
	}
	defer clear(clean)
	if current, err := ParseClaudeCredentialJSON(clean); err == nil {
		return current, nil
	}
	var current ClaudeCredential
	if err := json.Unmarshal(clean, &current); err != nil {
		return ClaudeCredential{}, fmt.Errorf("decode imported credential: %w", err)
	}
	normalizeClaudeCredential(&current)
	// 仅为缺少的必需字段生成校验占位，其他原有字段继续使用严格解析。
	for key, value := range map[string]string{
		"access_token": current.AccessToken, "account_uuid": current.AccountUUID,
		"expired": current.Expire,
	} {
		if value == "" {
			placeholder := "import-validation"
			if key == "expired" {
				placeholder = "1970-01-01T00:00:00Z"
			}
			fields[key], _ = json.Marshal(placeholder)
		}
	}
	probe, err := json.Marshal(fields)
	if err != nil {
		return ClaudeCredential{}, err
	}
	defer clear(probe)
	validated, err := ParseClaudeCredentialJSON(probe)
	if err != nil {
		return ClaudeCredential{}, err
	}
	current.DeviceIDs = validated.DeviceIDs
	needsProfile := current.AccountUUID == ""
	now := claudeNow(options)
	refreshed := false
	refresh := func() error {
		token, err := requestClaudeTokens(ctx, options, map[string]any{
			"client_id": claudeauth.ClientID, "grant_type": "refresh_token",
			"refresh_token": current.RefreshToken, "scope": claudeauth.ClaudeOAuthScope,
		})
		if err != nil {
			return fmt.Errorf("complete imported Claude credential: %w", err)
		}
		if token.ExpiresIn <= 0 || token.ExpiresIn > int64((1<<63-1)/time.Second) {
			return fmt.Errorf("Claude token response has invalid expiration")
		}
		candidate := ClaudeCredential{AccountUUID: token.Account.UUID, OrganizationUUID: token.Organization.UUID}
		needsProfile = candidate.AccountUUID == ""
		if err := checkClaudeImportIdentity(current, candidate); err != nil {
			return err
		}
		current.AccessToken = token.AccessToken
		if token.RefreshToken != "" {
			current.RefreshToken = token.RefreshToken
		}
		if token.IDToken != "" {
			current.IDToken = token.IDToken
		}
		if candidate.AccountUUID != "" {
			current.AccountUUID = candidate.AccountUUID
		}
		if candidate.OrganizationUUID != "" {
			current.OrganizationUUID = candidate.OrganizationUUID
		}
		if token.Account.EmailAddress != "" {
			current.Email = token.Account.EmailAddress
		}
		if token.Organization.Name != "" {
			current.OrganizationName = token.Organization.Name
		}
		current.Expire = now.Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
		current.LastRefresh = now.Format(time.RFC3339)
		refreshed = true
		return nil
	}
	expires, known := ClaudeCredentialExpiresAt(current)
	if current.AccessToken == "" || !known || !expires.After(now) {
		if err := refresh(); err != nil {
			return ClaudeCredential{}, err
		}
	}
	if needsProfile {
		profile, err := fetchClaudeOAuthProfile(ctx, options, current.AccessToken)
		var upstream *ClaudeUpstreamHTTPError
		if !refreshed && errors.As(err, &upstream) && upstream.StatusCode == http.StatusUnauthorized {
			if err := refresh(); err != nil {
				return ClaudeCredential{}, err
			}
			profile, err = fetchClaudeOAuthProfile(ctx, options, current.AccessToken)
		}
		if err != nil {
			return ClaudeCredential{}, fmt.Errorf("complete imported Claude account identity: %w", err)
		}
		candidate := ClaudeCredential{}
		applyClaudeProfile(&candidate, profile)
		if err := checkClaudeImportIdentity(current, candidate); err != nil {
			return ClaudeCredential{}, err
		}
		applyClaudeProfile(&current, profile)
	}
	normalizeClaudeCredential(&current)
	if err := validateClaudeCredential(current); err != nil {
		return ClaudeCredential{}, err
	}
	return current, nil
}

func checkClaudeImportIdentity(current, candidate ClaudeCredential) error {
	if current.AccountUUID != "" && candidate.AccountUUID != "" && current.AccountUUID != candidate.AccountUUID {
		return ErrClaudeCredentialIdentityChanged
	}
	if current.OrganizationUUID != "" && candidate.OrganizationUUID != "" && current.OrganizationUUID != candidate.OrganizationUUID {
		return ErrClaudeOrganizationIdentityChanged
	}
	return nil
}

func credentialImportFields(raw []byte, expectedClientID string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxCredentialBytes {
		return nil, fmt.Errorf("credential JSON size is invalid")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, fmt.Errorf("credential must be one JSON object")
	}
	allowed := make(map[string]struct{}, len(fields))
	for key := range fields {
		allowed[key] = struct{}{}
	}
	if err := validateClaudeCredentialObject(raw, allowed); err != nil {
		return nil, err
	}
	if rawClientID, ok := fields["client_id"]; ok {
		var clientID string
		if json.Unmarshal(rawClientID, &clientID) != nil || strings.TrimSpace(clientID) != expectedClientID {
			return nil, fmt.Errorf("credential OAuth client is not supported")
		}
		delete(fields, "client_id")
	}
	return fields, nil
}

// JWT claims 仅用于取回导入文件中的身份元数据，不作为认证或签名验证。
func completeCodexImportClaims(credential *CodexCredential) error {
	for _, token := range []string{credential.IDToken, credential.AccessToken} {
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			continue
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		var claims struct {
			Email string `json:"email"`
			Auth  struct {
				AccountID string `json:"chatgpt_account_id"`
			} `json:"https://api.openai.com/auth"`
			Profile struct {
				Email string `json:"email"`
			} `json:"https://api.openai.com/profile"`
		}
		err = json.Unmarshal(payload, &claims)
		clear(payload)
		if err != nil {
			continue
		}
		accountID := strings.TrimSpace(claims.Auth.AccountID)
		if accountID != "" {
			if credential.AccountID != "" && credential.AccountID != accountID {
				return ErrCredentialIdentityChanged
			}
			credential.AccountID = accountID
		}
		if credential.Email == "" {
			credential.Email = strings.TrimSpace(claims.Email)
			if credential.Email == "" {
				credential.Email = strings.TrimSpace(claims.Profile.Email)
			}
		}
	}
	return nil
}
