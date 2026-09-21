package main

import (
	"runtime/debug"
	"testing"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	"gpt-load/internal/catalog"
)

func TestCodexVersionSetStaysAligned(t *testing.T) {
	if catalog.CodexModelCatalogVersion != cpaembedded.CodexClientVersion {
		t.Fatalf(
			"Codex model catalog version = %q, CPA client version = %q",
			catalog.CodexModelCatalogVersion,
			cpaembedded.CodexClientVersion,
		)
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("build info unavailable")
	}
	for _, dependency := range info.Deps {
		if dependency.Path != "github.com/router-for-me/CLIProxyAPI/v7" {
			continue
		}
		if dependency.Version != catalog.CodexModelCatalogCPASDKVersion {
			t.Fatalf(
				"CPA SDK version = %q, catalog expects %q",
				dependency.Version,
				catalog.CodexModelCatalogCPASDKVersion,
			)
		}
		return
	}
	t.Fatal("CPA SDK dependency missing from build info")
}
