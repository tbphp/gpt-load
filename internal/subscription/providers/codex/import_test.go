package codex

import (
	"strings"
	"testing"

	"gpt-load/internal/outboundproxy"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestCodexImportPreservesCanonicalExport(t *testing.T) {
	driver := newCodexDriver()
	value, err := driver.ImportCredential(t.Context(), []byte(`{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"account","client_id":"app_EMoamEEZ73f0CkXaXp7hrann"}`))
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

func TestCodexImportHonorsRuntimeNetworkPolicy(t *testing.T) {
	ctx := subscriptionruntime.WithNetworkContext(t.Context(), subscriptionruntime.NetworkContext{
		Proxy: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "invalid"}},
	})
	if _, err := newCodexDriver().ImportCredential(ctx, []byte(`{"type":"codex","refresh_token":"refresh"}`)); err == nil {
		t.Fatal("import ignored invalid frozen network policy")
	}
}
