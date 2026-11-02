package importfile

import (
	"testing"

	"gpt-load/internal/channel"
)

func TestParseMuseCPAExportUsesDCAToken(t *testing.T) {
	document, err := Parse([]byte(`{"type":"meta","auth_kind":"oauth","dca_token":"dca:test","access_token":"minted-key","email":"muse@example.com","base_url":"https://untrusted.example/v1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Entries) != 1 || document.Entries[0].ErrorCode != "" || document.Entries[0].ChannelID != channel.ID("muse") {
		t.Fatalf("Meta OAuth export was not recognized: %#v", document.Entries)
	}
}
