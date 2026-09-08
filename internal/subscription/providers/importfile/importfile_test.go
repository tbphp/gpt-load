package importfile

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gpt-load/internal/channel"
)

// 测试凭据均为人工占位值；sub2api 结构固定核对到提交
// 772a0382f079676983c06f24b0d41e09139a8462 的 account_data.go、
// useOpenAIOAuth.ts、useGrokOAuth.ts、useAntigravityOAuth.ts 与 useAccountOAuth.ts。
// 历史容器由 37047919abef5ec425882dbd9b449fd9165c0a87 的变更确认。
func TestParseMapsSupportedExports(t *testing.T) {
	tests := []struct {
		name, raw, format string
		channel           channel.ID
		want              map[string]any
	}{
		{
			name: "official codex", format: "codex", channel: channel.Codex,
			raw:  `{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"access_token":"access","refresh_token":"refresh","id_token":"id","account_id":"account"},"last_refresh":"2026-09-08T00:00:00Z"}`,
			want: map[string]any{"type": "codex", "access_token": "access", "refresh_token": "refresh", "account_id": "account", "id_token": "id", "last_refresh": "2026-09-08T00:00:00Z"},
		},
		{
			name: "official claude", format: "claude-code", channel: channel.Claude,
			raw:  `{"claudeAiOauth":{"accessToken":"access","refreshToken":"refresh","expiresAt":1800000000123,"scopes":["user:profile","user:inference"],"subscriptionType":"max"},"metadata":{"rate":0.5}}`,
			want: map[string]any{"type": "claude", "access_token": "access", "refresh_token": "refresh", "expired": "2027-01-15T08:00:00.123Z"},
		},
		{
			name: "sub2api codex", format: "sub2api", channel: channel.Codex,
			raw:  `{"platform":"openai","type":"oauth","name":"example","expires_at":1,"credentials":{"access_token":"access","refresh_token":"refresh","chatgpt_account_id":"account","expires_at":1800000000,"client_id":"app_EMoamEEZ73f0CkXaXp7hrann","base_url":"https://ignored.invalid"},"extra":{"rate":0.75}}`,
			want: map[string]any{"type": "codex", "access_token": "access", "refresh_token": "refresh", "account_id": "account", "expired": "2027-01-15T08:00:00Z"},
		},
		{
			name: "sub2api claude extra", format: "sub2api", channel: channel.Claude,
			raw:  `{"platform":"anthropic","type":"oauth","credentials":{"access_token":"access","refresh_token":"refresh","expires_at":"1800000000"},"extra":{"account_uuid":"account","org_uuid":"org","email_address":"a@example.test"}}`,
			want: map[string]any{"type": "claude", "access_token": "access", "refresh_token": "refresh", "account_uuid": "account", "organization_uuid": "org", "email": "a@example.test", "expired": "2027-01-15T08:00:00Z"},
		},
		{
			name: "sub2api antigravity", format: "sub2api", channel: channel.Antigravity,
			raw:  `{"platform":"antigravity","type":"oauth","credentials":{"access_token":"access","refresh_token":"refresh","email":"a@example.test","project_id":"project","expires_at":"1800000000"}}`,
			want: map[string]any{"type": "antigravity", "access_token": "access", "refresh_token": "refresh", "email": "a@example.test", "project_id": "project", "expired": "2027-01-15T08:00:00Z"},
		},
		{
			name: "sub2api grok", format: "sub2api", channel: channel.Grok,
			raw:  `{"platform":"grok","type":"oauth","credentials":{"access_token":"access","refresh_token":"refresh","sub":"subject","email":"a@example.test","expires_at":"2027-01-15T08:00:00+00:00","client_id":"b1a00492-073a-47ea-816f-4c329264a828"}}`,
			want: map[string]any{"type": "grok", "access_token": "access", "refresh_token": "refresh", "account_id": "subject", "email": "a@example.test", "expired": "2027-01-15T08:00:00Z"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := parseOne(t, test.raw)
			if entry.ErrorCode != "" || entry.Format != test.format || entry.ChannelID != test.channel || entry.Index != 1 {
				t.Fatalf("entry metadata = %#v", entry)
			}
			var got map[string]any
			if err := json.Unmarshal(entry.Credential, &got); err != nil {
				t.Fatal(err)
			}
			for key, want := range test.want {
				if got[key] != want {
					t.Errorf("field %s = %v, want %v", key, got[key], want)
				}
			}
			for _, key := range []string{"base_url", "extra", "subscriptionType", "client_id", "expires_at", "scopes"} {
				if _, ok := got[key]; ok {
					t.Errorf("source metadata %s was retained", key)
				}
			}
		})
	}
}

func TestParsePreservesCPAValidationBoundary(t *testing.T) {
	raw := `{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"account","base_url":"https://rejected.invalid","disabled":"invalid"}`
	entry := parseOne(t, raw)
	if entry.Format != "cpa" || entry.ErrorCode != "" {
		t.Fatalf("entry = %#v", entry)
	}
	var got map[string]any
	if err := json.Unmarshal(entry.Credential, &got); err != nil {
		t.Fatal(err)
	}
	if got["base_url"] != "https://rejected.invalid" || got["disabled"] != "invalid" {
		t.Fatal("CPA validation fields were stripped")
	}
}

func TestParseSub2APIBundlesPreservesPartialFailures(t *testing.T) {
	for _, header := range []string{``, `"type":"sub2api-data","version":1,`, `"type":"sub2api-bundle","version":1,`} {
		raw := `{` + header + `"exported_at":"2026-09-08T00:00:00Z","proxies":[],"accounts":[{"platform":"openai","type":"oauth","credentials":{"refresh_token":"refresh"}},{"platform":"gemini","type":"oauth","credentials":{"refresh_token":"refresh"}},false]}`
		doc, err := Parse([]byte(raw))
		if err != nil || len(doc.Entries) != 3 {
			t.Fatalf("Parse() = %d entries, %v", len(doc.Entries), err)
		}
		for index, entry := range doc.Entries {
			if entry.Index != index+1 || entry.Format != "sub2api" {
				t.Fatalf("entry metadata = %#v", entry)
			}
		}
		if doc.Entries[0].ErrorCode != "" || doc.Entries[1].ErrorCode != "unsupported_channel" || doc.Entries[2].ErrorCode != "invalid_credential" {
			t.Fatalf("unexpected entry results: %#v", doc.Entries)
		}
	}
}

func TestParseRejectsUnsafeOrIncompatibleEntries(t *testing.T) {
	tests := []struct{ raw, code string }{
		{`{"tokens":{"access_token":"access"}}`, "missing_refresh_token"},
		{`{"auth_mode":"chatgptAuthTokens","tokens":{"refresh_token":"refresh"}}`, "unsupported_auth_mode"},
		{`{"OPENAI_API_KEY":"key","tokens":{"refresh_token":"refresh"}}`, "unsupported_auth_mode"},
		{`{"claudeAiOauth":{"refreshToken":"refresh","scopes":"user:inference"}}`, "invalid_credential"},
		{`{"claudeAiOauth":{"refreshToken":"refresh","scopes":["user:profile"]}}`, "unsupported_auth_mode"},
		{`{"platform":"openai","type":"oauth","credentials":{"refresh_token":"refresh","auth_mode":"personalAccessToken"}}`, "unsupported_auth_mode"},
		{`{"platform":"openai","type":"oauth","credentials":{"refresh_token":"refresh","client_id":"mobile-client"}}`, "unsupported_client"},
		{`{"platform":"anthropic","type":"setup-token","credentials":{"access_token":"access"}}`, "unsupported_auth_mode"},
		{`{"platform":"anthropic","type":"oauth","credentials":{"refresh_token":"refresh","account_uuid":"one"},"extra":{"account_uuid":"two"}}`, "identity_conflict"},
		{`{"platform":"openai","type":"oauth","credentials":{"refresh_token":true}}`, "invalid_credential"},
		{`{"platform":"openai","type":"oauth","credentials":{"refresh_token":"refresh","expires_at":1800000000000}}`, "invalid_credential"},
		{`{"platform":"openai","type":"oauth","credentials":{"refresh_token":"refresh","expires_at":1800000000.5}}`, "invalid_credential"},
		{`{"platform":"openai","type":"oauth","credentials":{"refresh_token":"refresh","expires_at":"invalid"}}`, "invalid_credential"},
		{`{"platform":"antigravity","type":"oauth","credentials":{"refresh_token":"refresh"}}`, "incomplete_credential"},
		{`{"platform":"antigravity","type":"oauth","credentials":{"access_token":"access","refresh_token":"refresh","email":"a@example.test"}}`, "incomplete_credential"},
		{`{"platform":"grok","type":"oauth","credentials":{"refresh_token":"refresh","client_id":"custom"}}`, "unsupported_client"},
		{`{"platform":"openai","type":"oauth","credentials":{"refresh_token":"refresh\nother"}}`, "invalid_credential"},
	}
	for _, test := range tests {
		entry := parseOne(t, test.raw)
		if entry.ErrorCode != test.code || len(entry.Credential) != 0 {
			t.Errorf("entry error = %q, want %q", entry.ErrorCode, test.code)
		}
	}
}

func TestParseDocumentValidation(t *testing.T) {
	tests := []struct {
		raw  []byte
		code string
	}{
		{[]byte(`{"tokens":{"refresh_token":"one","refresh_token":"two"}}`), "invalid_json"},
		{[]byte(`{"metadata":{"x":1,"x":2},"tokens":{"refresh_token":"refresh"}}`), "invalid_json"},
		{[]byte(`{"tokens":{}} {}`), "invalid_json"},
		{[]byte{'{', '"', 0xff, '"', ':', '1', '}'}, "invalid_json"},
		{[]byte(`{"type":"sub2api-data","version":2,"proxies":[],"accounts":[]}`), "unsupported_version"},
		{[]byte(`{"type":"unknown","exported_at":"date","proxies":[],"accounts":[]}`), "unsupported_format"},
		{[]byte(`{"type":42,"exported_at":"date","proxies":[],"accounts":[]}`), "unsupported_format"},
		{[]byte(`{"proxies":[],"accounts":[{}]}`), "unsupported_format"},
		{[]byte(`{"type":"sub2api-data","proxies":[],"accounts":null}`), "invalid_document"},
		{[]byte(`[]`), "empty_document"},
		{[]byte(strings.Repeat(" ", MaxFileBytes+1)), "file_too_large"},
		{[]byte(`[` + strings.Repeat(`{},`, MaxEntries) + `{}` + `]`), "too_many_entries"},
	}
	for _, test := range tests {
		_, err := Parse(test.raw)
		var parseErr *Error
		if !errors.As(err, &parseErr) || parseErr.Code != test.code {
			t.Errorf("Parse() error = %v, want %s", err, test.code)
		}
	}
	doc, err := Parse(append([]byte{0xef, 0xbb, 0xbf}, []byte(`[{"tokens":{"refresh_token":"refresh"}},{"claudeAiOauth":{"refreshToken":"refresh"}}]`)...))
	if err != nil || len(doc.Entries) != 2 {
		t.Fatalf("BOM/mixed array = %d entries, %v", len(doc.Entries), err)
	}
}

func TestParseRejectsAmbiguousAndUnsupportedOfficialFiles(t *testing.T) {
	entry := parseOne(t, `{"tokens":{"refresh_token":"refresh"},"claudeAiOauth":{"refreshToken":"other"}}`)
	if entry.ErrorCode != "unsupported_format" || len(entry.Credential) != 0 {
		t.Fatalf("ambiguous credential result = %#v", entry)
	}
	entry = parseOne(t, `{"auth_mode":"apikey","OPENAI_API_KEY":"key"}`)
	if entry.Format != "codex" || entry.ChannelID != channel.Codex || entry.ErrorCode != "unsupported_auth_mode" {
		t.Fatalf("official API key file result = %#v", entry)
	}
}

func TestParseUsesOnlyTokenExpiryAndSupportsCodexExchangeKey(t *testing.T) {
	entry := parseOne(t, `{"auth_mode":"chatgpt","OPENAI_API_KEY":"exchange-key","tokens":{"refresh_token":"refresh"}}`)
	if entry.ErrorCode != "" || strings.Contains(string(entry.Credential), "exchange-key") {
		t.Fatalf("explicit ChatGPT auth result = %#v", entry)
	}
	entry = parseOne(t, `{"platform":"openai","type":"oauth","expires_at":1800000000,"credentials":{"refresh_token":"refresh","subscription_expires_at":"2027-01-15T08:00:00Z","expires_in":3600}}`)
	var mapped map[string]any
	if err := json.Unmarshal(entry.Credential, &mapped); err != nil {
		t.Fatal(err)
	}
	if _, exists := mapped["expired"]; exists {
		t.Fatal("operator or subscription expiry was used as token expiry")
	}
}

func TestParseLimitsIndividualCredentials(t *testing.T) {
	raw := `[{"tokens":{"refresh_token":"` + strings.Repeat("x", MaxCredentialBytes) + `"}},{"tokens":{"refresh_token":"refresh"}}]`
	doc, err := Parse([]byte(raw))
	if err != nil || len(doc.Entries) != 2 || doc.Entries[0].ErrorCode != "credential_too_large" || doc.Entries[1].ErrorCode != "" {
		t.Fatalf("Parse() = %#v, %v", doc, err)
	}
}

func parseOne(t *testing.T, raw string) Entry {
	t.Helper()
	doc, err := Parse([]byte(raw))
	if err != nil || len(doc.Entries) != 1 {
		t.Fatalf("Parse() = %d entries, %v", len(doc.Entries), err)
	}
	return doc.Entries[0]
}
