package codex

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBridgeDependencyStaysBehindSubscriptionProviderBoundaries(t *testing.T) {
	const bridge = "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
	output, err := exec.Command(
		"go", "list", "-f", `{{.ImportPath}}|{{join .Imports ","}},{{join .TestImports ","}},{{join .XTestImports ","}}`, "gpt-load/internal/...",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("go list Codex dependency boundary: %v\n%s", err, output)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		importer, imports, found := strings.Cut(line, "|")
		if !found || importer == "gpt-load/internal/codex" || strings.HasPrefix(importer, "gpt-load/internal/codex/") ||
			importer == "gpt-load/internal/claude" || strings.HasPrefix(importer, "gpt-load/internal/claude/") {
			continue
		}
		for _, dependency := range strings.Split(imports, ",") {
			if dependency == bridge {
				t.Errorf("%s directly imports CPA bridge; use a provider boundary package instead", importer)
			}
		}
	}
}
