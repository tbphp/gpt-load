package webui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebCICompositeActionRunsCompleteFrontendGate(t *testing.T) {
	content := readRepositoryFile(t, ".github/actions/web-ci/action.yml")
	for _, required := range []string{
		"pnpm/action-setup@v6",
		"version: 11.15.1",
		"actions/setup-node@v6",
		"node-version: 24.18.0",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("web-ci action does not contain %q", required)
		}
	}

	previousIndex := -1
	for _, command := range []string{
		"pnpm --dir web install --frozen-lockfile",
		"pnpm --dir web run lint",
		"pnpm --dir web run format",
		"pnpm --dir web run type-check",
		"pnpm --dir web run test",
		"pnpm --dir web run build",
	} {
		if count := strings.Count(content, command); count != 1 {
			t.Fatalf("web-ci action contains %q %d times, want exactly once", command, count)
		}
		commandIndex := strings.Index(content, command)
		if commandIndex <= previousIndex {
			t.Fatalf("web-ci action does not run %q in the required order", command)
		}
		previousIndex = commandIndex
	}
}

func TestBinaryWorkflowsBuildWebBeforeGo(t *testing.T) {
	const checkoutReference = "uses: actions/checkout@v7"
	const webAction = "uses: ./.github/actions/web-ci"

	for _, workflow := range []string{
		".github/workflows/ci.yml",
		".github/workflows/release-linux.yml",
		".github/workflows/release-macos.yml",
		".github/workflows/release-windows.yml",
	} {
		content := readRepositoryFile(t, workflow)
		goBuildIndex := strings.Index(content, "go build")
		if goBuildIndex < 0 {
			t.Fatalf("%s does not contain go build", workflow)
		}

		binaryBuildPrefix := content[:goBuildIndex]
		if count := strings.Count(binaryBuildPrefix, checkoutReference); count != 1 {
			t.Fatalf("%s binary build chain contains checkout %d times, want exactly once", workflow, count)
		}
		if count := strings.Count(content, webAction); count != 1 {
			t.Fatalf("%s contains the web-ci action %d times, want exactly once", workflow, count)
		}

		checkoutIndex := strings.Index(binaryBuildPrefix, checkoutReference)
		webIndex := strings.Index(content, webAction)
		if checkoutIndex >= webIndex || webIndex >= goBuildIndex {
			t.Fatalf("%s does not order checkout, web-ci, and go build correctly", workflow)
		}
	}
}

func TestDockerWorkflowKeepsIndependentWebBuild(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/docker-build.yml")
	for _, forbidden := range []string{
		"./.github/actions/web-ci",
		"pnpm --dir web",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("docker workflow unexpectedly contains %q", forbidden)
		}
	}
}

func readRepositoryFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(content)
}
