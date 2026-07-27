package webui

import (
	"os"
	"os/exec"
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

func TestReleaseWorkflowUsesTagOnlyTriggerAndStrictSemverGuard(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	trigger := workflowTopLevelBlock(t, content, "on")
	for _, required := range []string{"push:", "tags:", `- "v2.*"`} {
		if !strings.Contains(trigger, required) {
			t.Fatalf("release trigger does not contain %q:\n%s", required, trigger)
		}
	}
	for _, forbidden := range []string{"branches:", "pull_request:", "workflow_dispatch:"} {
		if strings.Contains(trigger, forbidden) {
			t.Fatalf("release trigger contains forbidden %q:\n%s", forbidden, trigger)
		}
	}

	script := workflowMarkedScript(t, content, "release-tag-validation")
	scriptPath := filepath.Join(t.TempDir(), "validate-release-tag.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\nset -euo pipefail\n"+script), 0o700); err != nil {
		t.Fatalf("write tag validation script: %v", err)
	}
	for _, test := range []struct {
		tag        string
		valid      bool
		wantOutput []string
	}{
		{
			tag:   "v2.0.0",
			valid: true,
			wantOutput: []string{
				"version=v2.0.0", "prerelease=false", "image_exact=v2.0.0",
				"image_minor=2.0", "image_major=2",
			},
		},
		{
			tag:   "v2.0.0-rc.1",
			valid: true,
			wantOutput: []string{
				"version=v2.0.0-rc.1", "prerelease=true", "image_exact=v2.0.0-rc.1",
				"image_minor=2.0", "image_major=2",
			},
		},
		{
			tag:   "v2.10.3-alpha-1.0",
			valid: true,
			wantOutput: []string{
				"version=v2.10.3-alpha-1.0", "prerelease=true",
				"image_exact=v2.10.3-alpha-1.0", "image_minor=2.10", "image_major=2",
			},
		},
		{tag: "v2.0.0.1", valid: false},
		{tag: "v2.01.0", valid: false},
		{tag: "v2.0.01", valid: false},
		{tag: "v2.0.0-", valid: false},
		{tag: "v2.0.0-rc..1", valid: false},
		{tag: "v2.0.0-01", valid: false},
		{tag: "v2.0.0+build.1", valid: false},
		{tag: "v1.4.0", valid: false},
	} {
		t.Run(test.tag, func(t *testing.T) {
			outputPath := filepath.Join(t.TempDir(), "github-output")
			command := exec.Command("bash", scriptPath)
			command.Env = append(
				os.Environ(),
				"GITHUB_REF_NAME="+test.tag,
				"GITHUB_OUTPUT="+outputPath,
			)
			output, err := command.CombinedOutput()
			if test.valid && err != nil {
				t.Fatalf("tag %s rejected: %v\n%s", test.tag, err, output)
			}
			if !test.valid && err == nil {
				t.Fatalf("tag %s accepted, want rejection\n%s", test.tag, output)
			}
			if test.valid {
				githubOutput, readErr := os.ReadFile(outputPath)
				if readErr != nil {
					t.Fatalf("read tag outputs: %v", readErr)
				}
				for _, expected := range test.wantOutput {
					if !strings.Contains(string(githubOutput), expected+"\n") {
						t.Fatalf("tag %s outputs do not contain %q:\n%s", test.tag, expected, githubOutput)
					}
				}
			}
		})
	}
}

func TestReleaseWorkflowDoesNotRequireUntrackedAgentInstructionFiles(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	verifyJob := workflowJobBlock(t, content, "verify-and-build-web")
	if !strings.Contains(verifyJob, "git diff --check") {
		t.Fatal("release verification does not run git diff --check")
	}
	for _, untracked := range []string{"AGENTS.md", "CLAUDE.md"} {
		if strings.Contains(verifyJob, untracked) {
			t.Fatalf("release verification requires untracked %s", untracked)
		}
	}
}

func TestReleaseWorkflowBuildsOneWebDistAndFiveVersionedBinaries(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	if count := strings.Count(content, "uses: ./.github/actions/web-ci"); count != 1 {
		t.Fatalf("release workflow invokes web-ci %d times, want exactly once", count)
	}
	for _, required := range []string{
		"actions/checkout@v7",
		"actions/setup-go@v7",
		"name: verified-web-dist",
		"path: internal/webui/dist",
		"CGO_ENABLED: 0",
		"go build -trimpath",
		`gpt-load/internal/platform/version.Version=${{ github.ref_name }}`,
		"gpt-load-linux-amd64",
		"gpt-load-linux-arm64",
		"gpt-load-macos-amd64",
		"gpt-load-macos-arm64",
		"gpt-load-windows-amd64.exe",
		"SHA256SUMS",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("release workflow does not contain %q", required)
		}
	}
	for _, forbidden := range []string{"actions/checkout@v6", "actions/setup-go@v6"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("release workflow contains unavailable action major %q", forbidden)
		}
	}
	if strings.Count(content, "pnpm --dir web run build") != 0 {
		t.Fatal("release workflow rebuilds web outside the shared web-ci action")
	}
}

func TestReleaseWorkflowGatesSingleReleaseWriterAndImagePublication(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	for _, jobName := range []string{"publish-github", "publish-images"} {
		job := workflowJobBlock(t, content, jobName)
		for _, dependency := range []string{
			"verify-and-build-web",
			"build-binaries",
			"native-artifact-smoke",
			"docker-smoke",
		} {
			if !strings.Contains(job, dependency) {
				t.Fatalf("%s does not need %s:\n%s", jobName, dependency, job)
			}
		}
	}
	if count := strings.Count(content, "softprops/action-gh-release@"); count != 1 {
		t.Fatalf("release writer count = %d, want exactly 1", count)
	}

	releaseJob := workflowJobBlock(t, content, "publish-github")
	if !strings.Contains(releaseJob, "contents: write") || strings.Contains(releaseJob, "packages: write") {
		t.Fatalf("GitHub release permissions are not least privilege:\n%s", releaseJob)
	}
	imageJob := workflowJobBlock(t, content, "publish-images")
	if !strings.Contains(imageJob, "packages: write") || strings.Contains(imageJob, "contents: write") {
		t.Fatalf("image publication permissions are not least privilege:\n%s", imageJob)
	}
}

func TestReleaseWorkflowPublishesStable2xTagsWithoutLatest(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	imageJob := workflowJobBlock(t, content, "publish-images")
	for _, required := range []string{
		"docker/metadata-action@v6",
		"docker/login-action@v4",
		"docker/setup-qemu-action@v4",
		"docker/setup-buildx-action@v4",
		"docker/build-push-action@v7",
		"linux/amd64,linux/arm64",
		`value=${{ needs.validate-tag.outputs.image_exact }}`,
		`value=${{ needs.validate-tag.outputs.image_minor }}`,
		`value=${{ needs.validate-tag.outputs.image_major }}`,
	} {
		if !strings.Contains(imageJob, required) {
			t.Fatalf("image publication job does not contain %q:\n%s", required, imageJob)
		}
	}
	if strings.Contains(strings.ToLower(imageJob), "latest") {
		t.Fatalf("image publication job contains latest:\n%s", imageJob)
	}
}

func TestReleaseWorkflowIncludesCompleteS5NotesAndCSPWithinE2E(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	releaseJob := workflowJobBlock(t, content, "publish-github")
	for _, required := range []string{
		"Operations Runbook",
		"1.x cutover and rollback",
		"https://app.notion.com/p/3a95e49ce6ae813db7f9c7d6b8d83f02",
		"five raw binaries",
		"SHA256SUMS",
		"missing",
		"partial",
		"unpriced",
		"compatible upstream",
		"encryption key rotation",
		"2026-07-27",
		"https://developers.openai.com/api/docs/pricing",
		"https://platform.claude.com/docs/en/about-claude/pricing",
		"https://ai.google.dev/gemini-api/docs/pricing",
		"unified data/control dual-plane architecture",
		"Groups",
		"AccessKeys",
		"model discovery",
	} {
		if !strings.Contains(releaseJob, required) {
			t.Fatalf("release notes do not contain %q:\n%s", required, releaseJob)
		}
	}
	if count := strings.Count(content, "pnpm --dir web run test:e2e"); count != 1 {
		t.Fatalf("release workflow runs unfiltered test:e2e %d times, want exactly once", count)
	}
	if strings.Contains(content, "test:csp") {
		t.Fatal("release workflow redundantly runs test:csp outside the complete Playwright suite")
	}
}

func TestReleaseWorkflowVerifiesDownloadedNativeChecksumsAndGeneratedKeys(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	nativeJob := workflowJobBlock(t, content, "native-artifact-smoke")
	for _, scriptPath := range []string{
		".github/scripts/release-native-smoke.sh",
		".github/scripts/release-native-smoke.ps1",
	} {
		if !strings.Contains(nativeJob, scriptPath) {
			t.Fatalf("native smoke does not invoke %s:\n%s", scriptPath, nativeJob)
		}
	}
	nativeImplementation := nativeJob +
		readRepositoryFile(t, ".github/scripts/release-native-smoke.sh") +
		readRepositoryFile(t, ".github/scripts/release-native-smoke.ps1")
	for _, required := range []string{
		"name: release-assets",
		"SHA256SUMS",
		"sha256sum",
		"shasum -a 256",
		"Get-FileHash",
		"auth.key",
		"encryption.key",
		"AreAccessRulesProtected",
		"WindowsIdentity]::GetCurrent().User",
		"CreateProcessW",
		"CREATE_NEW_PROCESS_GROUP",
		"GenerateConsoleCtrlEvent",
		"CTRL_BREAK_EVENT",
		"GetConsoleCP",
		"AllocConsole",
		"ERROR_ACCESS_DENIED",
		"$process.WaitForExit(15000)",
		"$process.ExitCode -ne 0",
		"if (-not $process.HasExited)",
	} {
		if !strings.Contains(nativeImplementation, required) {
			t.Fatalf("native smoke does not contain %q", required)
		}
	}
	if strings.Contains(nativeImplementation, "AUTH_KEY=release-native-smoke") ||
		strings.Contains(nativeImplementation, `$env:AUTH_KEY = "release-native-smoke"`) {
		t.Fatal("native smoke bypasses generated auth.key with an explicit AUTH_KEY")
	}
	if strings.Contains(nativeImplementation, "$process = Start-Process") {
		t.Fatal("Windows native smoke uses Start-Process without a new console process group")
	}
	if strings.Contains(nativeImplementation, "CREATE_NEW_CONSOLE") {
		t.Fatal("Windows native smoke gives the child a separate console that cannot receive the targeted CTRL_BREAK")
	}
}

func TestReleaseWorkflowRunsCompleteLocalDockerSmoke(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	dockerJob := workflowJobBlock(t, content, "docker-smoke")
	if !strings.Contains(dockerJob, ".github/scripts/release-docker-smoke.sh") {
		t.Fatalf("Docker smoke job does not invoke the focused script:\n%s", dockerJob)
	}
	script := readRepositoryFile(t, ".github/scripts/release-docker-smoke.sh")
	for _, required := range []string{
		"10001:10001",
		"/app/data",
		"auth.key",
		"encryption.key",
		"gpt-load.db",
		"/api/usage",
		"/api/model-prices",
		"/api/groups",
		"/api/access-keys",
		"/v1/chat/completions",
		"finish_reason",
		"prompt_tokens",
		"completion_tokens",
		"docker stop --time 15",
		"container-first.log",
		"container-second.log",
		"secret_free=true",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("release Docker smoke does not contain %q", required)
		}
	}
}

func TestReleaseDockerSmokeDefersOwnedResourceCleanupUntilAfterConflictChecks(t *testing.T) {
	script := readRepositoryFile(t, ".github/scripts/release-docker-smoke.sh")
	tempTrapIndex := strings.Index(script, "trap cleanup_temp EXIT")
	containerCheckIndex := strings.Index(script, `for target in "${container}" "${probe}"; do`)
	conflictExitIndex := strings.Index(script, "task image or volume already exists")
	fullTrapIndex := strings.Index(script, "trap cleanup EXIT")
	workStartIndex := strings.Index(script, `cat >"${task_tmp}/fake_upstream.py"`)
	preflightExitIndex := -1
	if workStartIndex >= 0 {
		preflightExitIndex = strings.LastIndex(script[:workStartIndex], "exit 1")
	}
	for name, index := range map[string]int{
		"temporary cleanup trap":        tempTrapIndex,
		"container conflict check":      containerCheckIndex,
		"image or volume conflict exit": conflictExitIndex,
		"completed conflict exit":       preflightExitIndex,
		"owned-resource cleanup trap":   fullTrapIndex,
		"owned-resource work":           workStartIndex,
	} {
		if index < 0 {
			t.Fatalf("release Docker smoke is missing %s", name)
		}
	}
	if !(tempTrapIndex < containerCheckIndex &&
		containerCheckIndex < conflictExitIndex &&
		conflictExitIndex < preflightExitIndex &&
		preflightExitIndex < fullTrapIndex &&
		fullTrapIndex < workStartIndex) {
		t.Fatalf(
			"cleanup ownership order is unsafe: temp=%d container=%d conflict=%d exit=%d full=%d work=%d",
			tempTrapIndex,
			containerCheckIndex,
			conflictExitIndex,
			preflightExitIndex,
			fullTrapIndex,
			workStartIndex,
		)
	}
}

func TestReleaseWorkflowPostPublishVerifiesAliasesAndExactDigests(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	job := workflowJobBlock(t, content, "post-publish-verify")
	for _, required := range []string{
		"packages: read",
		"docker/login-action@v4",
		"registry: ghcr.io",
		"image_minor",
		"image_major",
		"exact_digest",
		"alias_digest",
		"ghcr.io/tbphp/gpt-load",
		"tbphp/gpt-load",
	} {
		if !strings.Contains(job, required) {
			t.Fatalf("post-publish verification does not contain %q:\n%s", required, job)
		}
	}
}

func TestReleaseWorkflowPostPublishDownloadsExactAssetsAndRunsFiveNativeSmokes(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	inventoryJob := workflowJobBlock(t, content, "post-publish-verify")
	for _, required := range []string{
		"gh release download",
		"public-release",
		"SHA256SUMS",
		"sha256sum --check",
		"asset_count",
		`test "${asset_count}" = "6"`,
	} {
		if !strings.Contains(inventoryJob, required) {
			t.Fatalf("post-publish asset inventory does not contain %q:\n%s", required, inventoryJob)
		}
	}

	nativeJob := workflowJobBlock(t, content, "post-publish-native-smoke")
	for _, required := range []string{
		"publish-github",
		"publish-images",
		"ubuntu-24.04",
		"ubuntu-24.04-arm",
		"macos-15-intel",
		"macos-15",
		"windows-latest",
		"gpt-load-linux-amd64",
		"gpt-load-linux-arm64",
		"gpt-load-macos-amd64",
		"gpt-load-macos-arm64",
		"gpt-load-windows-amd64.exe",
		"gh release download",
		".github/scripts/release-native-smoke.sh",
		".github/scripts/release-native-smoke.ps1",
	} {
		if !strings.Contains(nativeJob, required) {
			t.Fatalf("post-publish native smoke does not contain %q:\n%s", required, nativeJob)
		}
	}
}

func TestReleaseWorkflowRunsBothPublishedImagesAndPreservesLatest(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	snapshotJob := workflowJobBlock(t, content, "capture-channel-baseline")
	for _, required := range []string{
		"ghcr_latest_digest",
		"dockerhub_latest_digest",
		"ghcr.io/tbphp/gpt-load:latest",
		"tbphp/gpt-load:latest",
		".github/scripts/release-image-digest.sh",
	} {
		if !strings.Contains(snapshotJob, required) {
			t.Fatalf("latest digest snapshot does not contain %q:\n%s", required, snapshotJob)
		}
	}

	imagePublication := workflowJobBlock(t, content, "publish-images")
	if !strings.Contains(imagePublication, "capture-channel-baseline") {
		t.Fatal("image publication is not ordered after the latest digest snapshot")
	}

	postPublish := workflowJobBlock(t, content, "post-publish-verify")
	for _, required := range []string{
		"capture-channel-baseline",
		"RELEASE_SMOKE_SOURCE_IMAGE",
		`ghcr.io/tbphp/gpt-load:${{ needs.validate-tag.outputs.image_exact }}`,
		`tbphp/gpt-load:${{ needs.validate-tag.outputs.image_exact }}`,
		".github/scripts/release-docker-smoke.sh",
		".github/scripts/release-image-digest.sh",
		"ghcr_latest_digest",
		"dockerhub_latest_digest",
	} {
		if !strings.Contains(postPublish, required) {
			t.Fatalf("published image runtime verification does not contain %q:\n%s", required, postPublish)
		}
	}
	for name, block := range map[string]string{
		"capture-channel-baseline": snapshotJob,
		"post-publish-verify":      postPublish,
	} {
		for _, required := range []string{
			"Log in to Docker Hub",
			"username: ${{ secrets.DOCKERHUB_USERNAME }}",
			"password: ${{ secrets.DOCKERHUB_READ_TOKEN }}",
		} {
			if !strings.Contains(block, required) {
				t.Fatalf("%s does not contain authenticated Docker Hub read %q", name, required)
			}
		}
		if strings.Contains(block, "password: ${{ secrets.DOCKERHUB_TOKEN }}") {
			t.Fatalf("%s exposes the Docker Hub write token to a read-only job", name)
		}
	}
	if !strings.Contains(
		imagePublication,
		"password: ${{ secrets.DOCKERHUB_TOKEN }}",
	) {
		t.Fatal("image publication does not use the Docker Hub write token")
	}
	if strings.Contains(imagePublication, "DOCKERHUB_READ_TOKEN") {
		t.Fatal("image publication unexpectedly uses the Docker Hub read-only token")
	}
	if !strings.Contains(
		postPublish,
		`test "${latest_digest}" != "${exact_digest}"`,
	) {
		t.Fatal("post-publication verification does not reject latest pointing at the exact 2.x image")
	}
}

func TestReleaseImageDigestFailsClosedOnOperationalInspectionErrors(t *testing.T) {
	for _, message := range []string{"rate limit exceeded", "docker: command not found"} {
		t.Run(message, func(t *testing.T) {
			script := filepath.Join("..", "..", ".github", "scripts", "release-image-digest.sh")
			fakeBin := t.TempDir()
			fakeDocker := filepath.Join(fakeBin, "docker")
			if err := os.WriteFile(
				fakeDocker,
				[]byte("#!/usr/bin/env sh\nprintf '"+message+"\\n' >&2\nexit 1\n"),
				0o700,
			); err != nil {
				t.Fatalf("write fake docker: %v", err)
			}
			command := exec.Command("bash", script, "example.test/gpt-load:latest")
			command.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("operational inspect failure returned success: %s", output)
			}
			if !strings.Contains(string(output), message) {
				t.Fatalf("operational inspect failure output = %q", output)
			}
		})
	}
}

func TestReleaseImageDigestReturnsAbsentOnlyForMissingManifest(t *testing.T) {
	script := filepath.Join("..", "..", ".github", "scripts", "release-image-digest.sh")
	fakeBin := t.TempDir()
	fakeDocker := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(
		fakeDocker,
		[]byte("#!/usr/bin/env sh\nprintf 'manifest unknown\\n' >&2\nexit 1\n"),
		0o700,
	); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	command := exec.Command("bash", script, "example.test/gpt-load:latest")
	command.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("missing manifest returned error: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "absent" {
		t.Fatalf("missing manifest output = %q, want absent", output)
	}
}

func TestReleaseDockerSmokeCanUsePublishedSourceWithoutDeletingIt(t *testing.T) {
	script := readRepositoryFile(t, ".github/scripts/release-docker-smoke.sh")
	for _, required := range []string{
		"RELEASE_SMOKE_SOURCE_IMAGE",
		`docker pull --platform "${platform}" "${source_image}"`,
		`docker image tag "${source_image}" "${image}"`,
		`docker image rm "${image}"`,
		"prices.data.overrides.some",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("release Docker smoke source-image mode does not contain %q", required)
		}
	}
	if strings.Contains(script, `docker image rm "${source_image}"`) {
		t.Fatal("release Docker smoke deletes the externally owned source image")
	}
}

func TestReleaseWorkflowRemovesObsoletePublishers(t *testing.T) {
	for _, name := range []string{
		".github/workflows/docker-build.yml",
		".github/workflows/release-linux.yml",
		".github/workflows/release-macos.yml",
		".github/workflows/release-windows.yml",
	} {
		if _, err := os.Stat(filepath.Join("..", "..", name)); !os.IsNotExist(err) {
			t.Fatalf("obsolete publisher still exists: %s", name)
		}
	}
}

func TestReadmesDoNotExposeInternalReleaseTaskNames(t *testing.T) {
	for _, name := range []string{"README.md", "README_CN.md", "README_JP.md"} {
		content := readRepositoryFile(t, name)
		if strings.Contains(content, "T18") {
			t.Fatalf("%s exposes the completed internal T18 task name", name)
		}
	}
}

func TestDockerfileCopiesWebInstallPolicyBeforeInstalling(t *testing.T) {
	content := readRepositoryFile(t, "Dockerfile")
	copyInputs := "COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./web/"
	install := "RUN pnpm --dir web install --frozen-lockfile"

	copyIndex := strings.Index(content, copyInputs)
	if copyIndex < 0 {
		t.Fatalf("Dockerfile does not copy the complete web install policy")
	}
	installIndex := strings.Index(content, install)
	if installIndex < 0 {
		t.Fatalf("Dockerfile does not install frozen web dependencies")
	}
	if copyIndex >= installIndex {
		t.Fatal("Dockerfile copies the web install policy after installing dependencies")
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

func workflowTopLevelBlock(t *testing.T, content, key string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	start := -1
	for index, line := range lines {
		if line == key+":" {
			start = index
			break
		}
	}
	if start < 0 {
		t.Fatalf("workflow does not contain top-level %s", key)
	}
	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		if lines[index] != "" && !strings.HasPrefix(lines[index], " ") {
			end = index
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func workflowJobBlock(t *testing.T, content, name string) string {
	t.Helper()
	lines := strings.Split(content, "\n")
	start := -1
	for index, line := range lines {
		if line == "  "+name+":" {
			start = index
			break
		}
	}
	if start < 0 {
		t.Fatalf("workflow does not contain job %s", name)
	}
	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		line := lines[index]
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
			end = index
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func workflowMarkedScript(t *testing.T, content, marker string) string {
	t.Helper()
	startMarker := "# " + marker + ":start"
	endMarker := "# " + marker + ":end"
	start := strings.Index(content, startMarker)
	end := strings.Index(content, endMarker)
	if start < 0 || end <= start {
		t.Fatalf("workflow does not contain marked script %s", marker)
	}
	body := content[start+len(startMarker) : end]
	lines := strings.Split(body, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimPrefix(line, "          ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}
