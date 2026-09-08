package claude

import (
	"strings"
	"testing"

	"gpt-load/internal/outboundproxy"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestClaudeImportPreservesCanonicalExport(t *testing.T) {
	driver := newClaudeDriver()
	value, err := driver.ImportCredential(t.Context(), []byte(`{"type":"claude","access_token":"access","refresh_token":"refresh","account_uuid":"account","expired":"2000-01-01T00:00:00Z","client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e"}`))
	if err != nil {
		t.Fatal(err)
	}
	if value.Identity() != "account" || strings.Contains(string(value.Canonical()), "client_id") {
		t.Fatal("import metadata leaked into the canonical credential")
	}
	if _, err := driver.Parse(value.Canonical()); err != nil {
		t.Fatalf("canonical export is not parseable: %v", err)
	}
}

func TestClaudeImportHonorsRuntimeNetworkPolicy(t *testing.T) {
	ctx := subscriptionruntime.WithNetworkContext(t.Context(), subscriptionruntime.NetworkContext{
		Proxy: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "invalid"}},
	})
	if _, err := newClaudeDriver().ImportCredential(ctx, []byte(`{"type":"claude","refresh_token":"refresh"}`)); err == nil {
		t.Fatal("import ignored invalid frozen network policy")
	}
}
