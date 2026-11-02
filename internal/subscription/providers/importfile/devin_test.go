package importfile

import (
	"gpt-load/internal/channel"
	"strings"
	"testing"
)

func TestParseDevinCPAExportPreservesCanonicalAuth(t *testing.T) {
	document, err := Parse([]byte(`{"type":"devin","api_key":"devin-session-token$secret","session_token":"devin-session-token$secret","user_id":"account","auth_kind":"oauth","base_url":"https://server.codeium.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Entries) != 1 || document.Entries[0].ErrorCode != "" || document.Entries[0].ChannelID != channel.Devin {
		t.Fatalf("Devin CPA export was not recognized: %#v", document.Entries)
	}
	if strings.Contains(document.Entries[0].ErrorCode, "secret") {
		t.Fatal("secret in import error")
	}
}
