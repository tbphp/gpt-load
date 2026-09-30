package embedded

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// 套餐声明仅补充展示信息，不参与认证、授权或配额判断。
func codexCredentialPlan(c CodexCredential) string {
	if plan := safeCodexPlan(c.PlanType); plan != "" {
		return plan
	}
	for _, token := range []string{c.IDToken, c.AccessToken} {
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			continue
		}
		b, e := base64.RawURLEncoding.DecodeString(parts[1])
		if e != nil {
			continue
		}
		var claims struct {
			Auth struct {
				Plan string `json:"chatgpt_plan_type"`
			} `json:"https://api.openai.com/auth"`
		}
		e = json.Unmarshal(b, &claims)
		clear(b)
		if e == nil {
			if plan := safeCodexPlan(claims.Auth.Plan); plan != "" {
				return plan
			}
		}
	}
	return safeCodexPlan(c.PlanType)
}
func safeCodexPlan(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 64 {
		return ""
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return ""
		}
	}
	return value
}
