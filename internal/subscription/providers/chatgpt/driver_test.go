package chatgpt

import (
	"testing"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/subscription/providers/codex"
)

func TestChatGPTDriverParsesCodexAuthFile(t *testing.T) {
	t.Parallel()
	driver := newChatGPTDriver()
	if driver.ID() != modules.ChatGPTSubscriptionDriver {
		t.Fatalf("ID() = %q", driver.ID())
	}
	raw := []byte(`{
		"type":"codex",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_id":"acct-one",
		"email":"user@example.com",
		"expired":"2030-01-01T00:00:00Z"
	}`)
	credential, err := driver.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := codex.ParseCredentialJSON(credential.Canonical())
	if err != nil || parsed.AccountID != "acct-one" || parsed.AccessToken != "access-secret" {
		t.Fatalf("parsed = %#v, err=%v", parsed, err)
	}
	if credential.Identity() == "" {
		t.Fatal("identity is empty")
	}
}
