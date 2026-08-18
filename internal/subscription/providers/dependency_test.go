package providers_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSubscriptionProviderDependenciesStayBehindProviderBoundaries(t *testing.T) {
	const bridge = "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
	output, err := exec.Command(
		"go", "list", "-f", `{{.ImportPath}}|{{join .Imports ","}},{{join .TestImports ","}},{{join .XTestImports ","}}`, "gpt-load/internal/...",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("go list subscription dependency boundary: %v\n%s", err, output)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		importer, imports, found := strings.Cut(line, "|")
		if !found {
			continue
		}
		providerBoundary := importer == "gpt-load/internal/subscription/providers" ||
			strings.HasPrefix(importer, "gpt-load/internal/subscription/providers/")
		for _, dependency := range strings.Split(imports, ",") {
			if dependency == bridge && !providerBoundary {
				t.Errorf("%s directly imports CPA bridge; use a subscription provider boundary instead", importer)
			}
			if importer == "gpt-load/internal/subscription/runtime" &&
				(dependency == "gpt-load/internal/codex" || dependency == "gpt-load/internal/claude" ||
					dependency == "gpt-load/internal/antigravity" || strings.HasPrefix(dependency, "gpt-load/internal/subscription/providers/")) {
				t.Errorf("generic subscription runtime imports provider implementation %s", dependency)
			}
		}
	}
}
