package webui

import (
	"strings"
	"testing"
)

func TestSelfHostedCIKeepsPlatformGatesAndLocalCaches(t *testing.T) {
	ci := readRepositoryFile(t, ".github/workflows/ci.yml")
	for job, runner := range map[string]string{
		"test":                   "[self-hosted, macOS, ARM64]",
		"race-tests":             "[self-hosted, macOS, ARM64]",
		"race-cpa":               "[self-hosted, Linux, X64]",
		"database-contract":      "[self-hosted, Linux, X64]",
		"windows-encryption-acl": "[self-hosted, Windows, X64]",
	} {
		block := workflowJobBlock(t, ci, job)
		if !strings.Contains(block, "runs-on: "+runner) {
			t.Errorf("%s is not assigned to %s", job, runner)
		}
	}
	for _, file := range []string{"ci.yml", "release.yml"} {
		content := readRepositoryFile(t, ".github/workflows/"+file)
		if strings.Contains(content, "cache: true") || strings.Contains(content, ".go-cache-scope") {
			t.Errorf("%s still restores remote Go caches over persistent local caches", file)
		}
		database := workflowJobBlock(t, content, "database-contract")
		for _, fixedPort := range []string{"3306:3306", "5432:5432"} {
			if strings.Contains(database, fixedPort) {
				t.Errorf("%s database gate can collide with local services: %s", file, fixedPort)
			}
		}
		if !strings.Contains(database, "job.services[matrix.driver].ports[format('{0}', matrix.port)]") {
			t.Errorf("%s database DSN does not use the assigned service port", file)
		}
	}
}

func TestSelfHostedReleaseIsolatesDockerCredentials(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	for _, job := range []string{
		"docker-smoke", "prebuilt-image-smoke", "publication-preflight", "publish-images",
		"post-publish-image-smoke", "post-publish-verify", "promote-image-channels", "reconcile-publication",
	} {
		block := workflowJobBlock(t, content, job)
		setup := workflowStepBlock(t, block, "Isolate Docker credentials")
		if !strings.Contains(setup, `mktemp -d "${RUNNER_TEMP}/docker-config.XXXXXX"`) ||
			!strings.Contains(setup, `echo "DOCKER_CONFIG=${docker_config}" >> "${GITHUB_ENV}"`) {
			t.Errorf("%s can overwrite the host Docker credentials", job)
		}
	}
	build := workflowJobBlock(t, content, "build-binaries")
	if !strings.Contains(build, "runs-on: ${{ matrix.runner }}") ||
		!strings.Contains(workflowStepBlock(t, build, "Build release binary"), "shell: bash") {
		t.Fatal("cross-platform binary build must select its runner and use an explicit bash shell")
	}
}
