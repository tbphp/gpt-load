package codex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
)

func identityTestToken(t *testing.T, claims map[string]string, padded bool) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"https://api.openai.com/auth": claims})
	if err != nil {
		t.Fatal(err)
	}
	encoding := base64.RawURLEncoding
	if padded {
		encoding = base64.URLEncoding
	}
	return "e30." + encoding.EncodeToString(raw) + ".signature"
}

func TestCodexIdentityUsesUserClaimsWithoutChangingCanonical(t *testing.T) {
	t.Parallel()
	user := identityTestToken(t, map[string]string{"chatgpt_user_id": "user-one"}, false)
	alias := identityTestToken(t, map[string]string{"user_id": "user-two"}, true)
	for _, test := range []struct{ name, idToken, accessToken, want string }{
		{"id token", user, "access", "workspace/user-one"},
		{"access token", "not-a-jwt", user, "workspace/user-one"},
		{"user id alias", alias, "access", "workspace/user-two"},
		{"access token alias", "", alias, "workspace/user-two"},
		{"claim precedence", identityTestToken(t, map[string]string{"chatgpt_user_id": "user-one", "user_id": "user-two"}, false), "access", "workspace/user-one"},
		{"blank primary claim", identityTestToken(t, map[string]string{"chatgpt_user_id": " ", "user_id": "user-two"}, false), "access", "workspace/user-two"},
		{"missing claims", identityTestToken(t, map[string]string{}, false), "access", "workspace"},
		{"invalid token", "e30.!.signature", "access", "workspace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// 固定旧格式的字段顺序，身份推导不得改变 canonical 字节或持久化派生字段。
			raw := []byte(fmt.Sprintf(`{"type":"codex"%s,"access_token":%q,"refresh_token":"refresh","account_id":"workspace","email":"owner@example.com"}`, identityTestIDTokenField(test.idToken), test.accessToken))
			credential, err := newCodexDriver().Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if string(credential.Canonical()) != string(raw) {
				t.Fatal("identity derivation changed canonical credential bytes")
			}
			if credential.Identity() != test.want {
				t.Fatalf("identity = %q, want %q", credential.Identity(), test.want)
			}
		})
	}
}

func identityTestIDTokenField(token string) string {
	if token == "" {
		return ""
	}
	return fmt.Sprintf(`,"id_token":%q`, token)
}

func TestCodexIdentityRejectsConflictingTokenUsers(t *testing.T) {
	t.Parallel()
	for _, accessClaim := range []string{"chatgpt_user_id", "user_id"} {
		t.Run(accessClaim, func(t *testing.T) {
			value := Credential{Type: "codex", AccountID: "workspace", RefreshToken: "refresh", IDToken: identityTestToken(t, map[string]string{"chatgpt_user_id": "user-one"}, false), AccessToken: identityTestToken(t, map[string]string{accessClaim: "user-two"}, false)}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := newCodexDriver().Parse(raw); err == nil {
				t.Fatal("conflicting token identities were accepted")
			}
			if _, err := MarshalCredential(value); err == nil {
				t.Fatal("conflicting token identities were canonicalized")
			}
		})
	}
}
