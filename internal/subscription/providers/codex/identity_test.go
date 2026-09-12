package codex

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// codexTestIDToken 构造一个 claims 形状与真实 Codex id_token 一致的令牌。
func codexTestIDToken(t *testing.T, accountID, userID string) string {
	t.Helper()
	encode := func(value any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	header := encode(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload := encode(map[string]any{
		"https://api.openai.com/auth": map[string]string{
			"chatgpt_account_id": accountID,
			"chatgpt_user_id":    userID,
		},
	})
	return header + "." + payload + ".signature"
}

func codexTestCredential(t *testing.T, accountID, userID, email, refreshToken string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"type":          "codex",
		"access_token":  "access-" + userID,
		"refresh_token": refreshToken,
		"account_id":    accountID,
		"email":         email,
		"id_token":      codexTestIDToken(t, accountID, userID),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// runtimeIdentity 走完整链路：解析 -> 规范化 -> 从持久化内容恢复 -> 运行时身份。
// 同时返回恢复出的凭据与 canonical 内容，供「canonical 形状未变」的断言使用。
func runtimeIdentity(t *testing.T, raw []byte) (string, Credential, string) {
	t.Helper()
	value, err := ParseCredentialJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := MarshalCredential(value)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟重新从持久化内容恢复（刷新路径就是这么做的）
	restored, err := ParseCredentialJSON(canonical)
	if err != nil {
		t.Fatalf("canonical export is not parseable: %v", err)
	}
	return codexRuntimeCredential(restored, canonical).Identity(), restored, string(canonical)
}

// TestCodexIdentityDistinguishesUsersWithinOneWorkspace 断言同一 workspace 下的不同用户
// 得到不同身份，否则第二份凭据会在导入时被判为重复项。
func TestCodexIdentityDistinguishesUsersWithinOneWorkspace(t *testing.T) {
	const workspace = "770d8f80-b7ad-4062-af19-a0d45a5dc62a"

	firstIdentity, first, firstCanonical := runtimeIdentity(t, codexTestCredential(t, workspace, "user-AAAA", "first@example.com", "refresh-first"))
	secondIdentity, second, secondCanonical := runtimeIdentity(t, codexTestCredential(t, workspace, "user-BBBB", "second@example.com", "refresh-second"))

	if firstIdentity == secondIdentity {
		t.Fatalf("credentials of distinct users collapsed into one identity %q", firstIdentity)
	}
	wantFirst := workspace + "/user-AAAA"
	if firstIdentity != wantFirst {
		t.Fatalf("identity = %q, want %q", firstIdentity, wantFirst)
	}
	if got := second.CredentialIdentity(); got != workspace+"/user-BBBB" {
		t.Fatalf("identity = %q, want %q", got, workspace+"/user-BBBB")
	}
	// canonical 的字节形状是 credentials.fingerprint 的契约：新增身份维度必须不影响它，
	// 否则升级到本版本时既有凭据会全部 fingerprint mismatch、实例无法启动。
	for _, canonical := range []string{firstCanonical, secondCanonical} {
		if strings.Contains(canonical, "user_id") {
			t.Fatalf("identity leaked into the canonical shape: %s", canonical)
		}
	}
	// 身份必须能从 canonical 复原：刷新路径就是「从 canonical 重新解析」。
	if first.CredentialIdentity() != firstIdentity {
		t.Fatalf("identity is not reproducible from canonical: %q -> %q", firstIdentity, first.CredentialIdentity())
	}
}

// TestCodexIdentityCollapsesSameUserAndWorkspace 断言同一用户在同一 workspace 的重复授权
// 仍折叠为同一身份（应当合并而不是并存）。
func TestCodexIdentityCollapsesSameUserAndWorkspace(t *testing.T) {
	const workspace = "70a31b69-2c1a-4469-9e7c-3935a96e3865"

	original, _, _ := runtimeIdentity(t, codexTestCredential(t, workspace, "user-AAAA", "owner@example.com", "refresh-one"))
	reauthorized, _, _ := runtimeIdentity(t, codexTestCredential(t, workspace, "user-AAAA", "owner@example.com", "refresh-two"))

	if original != reauthorized {
		t.Fatalf("re-authorization of one account produced a new identity: %q -> %q", original, reauthorized)
	}
}

// TestCodexIdentityKeepsLegacyWorkspaceIdentity 断言升级前导入的凭据（没有 id_token、
// Canonical 中也没有 user_id）保持旧的 workspace 身份。
func TestCodexIdentityKeepsLegacyWorkspaceIdentity(t *testing.T) {
	legacy := []byte(`{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"legacy-workspace"}`)

	identity, value, canonical := runtimeIdentity(t, legacy)
	if identity != "legacy-workspace" {
		t.Fatalf("legacy identity = %q, want %q", identity, "legacy-workspace")
	}
	if value.CredentialIdentity() != "legacy-workspace" {
		t.Fatalf("legacy credential identity = %q", value.CredentialIdentity())
	}
	if strings.Contains(canonical, "user_id") {
		t.Fatalf("legacy canonical shape changed: %s", canonical)
	}
}

// TestCodexIdentityIgnoresTokensWithoutUserClaim 断言令牌缺 user claim 时身份安全回退
// 到 workspace，不会因为解析失败而报错。
func TestCodexIdentityIgnoresTokensWithoutUserClaim(t *testing.T) {
	raw := []byte(`{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"ws","id_token":"not-a-jwt"}`)

	identity, value, _ := runtimeIdentity(t, raw)
	if identity != "ws" || value.CredentialIdentity() != "ws" {
		t.Fatalf("identity = %q / %q, want %q", identity, value.CredentialIdentity(), "ws")
	}
}
