package codex

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func (*codexDriver) MatchesRefreshIdentity(current, refreshed subscriptionruntime.Credential) bool {
	before, err := ParseCredentialJSON(current.Canonical())
	if err != nil {
		return false
	}
	after, err := ParseCredentialJSON(refreshed.Canonical())
	if err != nil || before.AccountID != after.AccountID {
		return false
	}
	beforeUser, afterUser := credentialUserID(before), credentialUserID(after)
	return beforeUser == "" || afterUser == "" || beforeUser == afterUser
}

// 身份只从既有令牌派生，不能改变参与持久化内容指纹计算的 canonical JSON。
func credentialIdentity(value Credential) string {
	accountID := strings.TrimSpace(value.AccountID)
	if userID := credentialUserID(value); userID != "" {
		return accountID + "/" + userID
	}
	return accountID
}

func credentialUserID(value Credential) string {
	for _, token := range []string{value.IDToken, value.AccessToken} {
		if userID := tokenUserID(token); userID != "" {
			return userID
		}
	}
	return ""
}

// JWT 仅用于读取已持有凭据的身份元数据，不作为签名或登录认证。
func tokenUserID(token string) string {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	defer clear(payload)
	var claims struct {
		Auth struct {
			ChatGPTUserID string `json:"chatgpt_user_id"`
			UserID        string `json:"user_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	if userID := strings.TrimSpace(claims.Auth.ChatGPTUserID); userID != "" {
		return userID
	}
	return strings.TrimSpace(claims.Auth.UserID)
}
