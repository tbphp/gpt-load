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
		"race-cpa":               "[self-hosted, Linux, ARM64]",
		"database-contract":      "[self-hosted, Linux, ARM64]",
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
		if !strings.Contains(database, "job.services.database.ports[format('{0}', matrix.port)]") {
			t.Errorf("%s database DSN does not use the assigned service port", file)
		}
	}
}

func TestReleaseIsolatesDockerCredentials(t *testing.T) {
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

func TestReleaseUsesSelfHostedValidationAndHostedPublicationRunners(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	for job, runner := range map[string]string{
		"static-checks":     "[self-hosted, macOS, ARM64]",
		"race-tests":        "[self-hosted, macOS, ARM64]",
		"race-cpa":          "[self-hosted, Linux, ARM64]",
		"database-contract": "[self-hosted, Linux, ARM64]",
	} {
		block := workflowJobBlock(t, content, job)
		if !strings.Contains(block, "runs-on: "+runner) {
			t.Errorf("%s is not assigned to %s", job, runner)
		}
	}
	if count := strings.Count(content, "self-hosted"); count != 4 {
		t.Fatalf("release workflow contains %d self-hosted runner assignments, want 4", count)
	}
	for _, job := range []string{
		"validate-tag", "verify-and-build-web", "package-metadata", "package-checksums", "docker-smoke",
		"publication-preflight", "publish-images", "publish-github", "post-publish-image-smoke",
		"post-publish-verify", "promote-image-channels", "deploy-render", "reconcile-publication",
	} {
		block := workflowJobBlock(t, content, job)
		if !strings.Contains(block, "runs-on: ubuntu-24.04") {
			t.Errorf("%s is not assigned to the GitHub-hosted Ubuntu runner", job)
		}
	}
	for _, job := range []string{"build-windows-setup", "windows-installer-smoke"} {
		block := workflowJobBlock(t, content, job)
		if !strings.Contains(block, "runs-on: windows-2025") {
			t.Errorf("%s is not assigned to the GitHub-hosted Windows runner", job)
		}
	}
	for _, test := range []struct {
		job      string
		required []string
	}{
		{
			job: "build-binaries",
			required: []string{
				"runner: ubuntu-24.04\n            goarch: amd64",
				"runner: ubuntu-24.04\n            goarch: arm64",
				"runner: macos-15\n            goarch: amd64",
				"runner: macos-15\n            goarch: arm64",
				"runner: windows-2025\n            goarch: amd64",
			},
		},
		{
			job: "native-artifact-smoke",
			required: []string{
				"runner: ubuntu-24.04\n            filename: gpt-load-linux-amd64",
				"runner: ubuntu-24.04-arm\n            filename: gpt-load-linux-arm64",
				"runner: macos-15-intel\n            filename: gpt-load-macos-amd64",
				"runner: macos-15\n            filename: gpt-load-macos-arm64",
				"runner: windows-2025\n            filename: gpt-load-windows-amd64.exe",
			},
		},
		{
			job: "prebuilt-image-smoke",
			required: []string{
				"runner: ubuntu-24.04\n          - arch: arm64",
				"runner: ubuntu-24.04-arm",
			},
		},
	} {
		block := workflowJobBlock(t, content, test.job)
		for _, required := range test.required {
			if !strings.Contains(block, required) {
				t.Errorf("%s does not include %s", test.job, required)
			}
		}
	}
	publish := workflowJobBlock(t, content, "publish-images")
	qemu := workflowStepBlock(t, publish, "Set up QEMU")
	if !strings.Contains(qemu, "platforms: arm64") {
		t.Fatal("AMD64 hosted image publisher must enable ARM64 emulation for the other target")
	}
	publishGitHub := workflowStepBlock(t, workflowJobBlock(t, content, "publish-github"), "Create or update GitHub Release draft")
	for _, required := range []string{"preserve_order: true", "overwrite_files: false"} {
		if !strings.Contains(publishGitHub, required) {
			t.Errorf("GitHub Release asset upload does not contain %q", required)
		}
	}
	deployRender := workflowJobBlock(t, content, "deploy-render")
	for _, required := range []string{
		"cli_${RENDER_CLI_VERSION}_linux_amd64.zip",
		"3b3f1f839ef36b81f12d84ac7288f1c96f9f7519b39c53fe6f866612f704e7cd",
	} {
		if !strings.Contains(deployRender, required) {
			t.Errorf("Render deployment does not contain %q", required)
		}
	}
}
