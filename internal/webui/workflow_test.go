package webui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	checkoutActionRef         = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"
	setupGoActionRef          = "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"
	setupNodeActionRef        = "actions/setup-node@820762786026740c76f36085b0efc47a31fe5020"
	pnpmSetupActionRef        = "pnpm/action-setup@0ebf47130e4866e96fce0953f49152a61190b271"
	uploadArtifactActionRef   = "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
	downloadArtifactActionRef = "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c"
	qemuActionRef             = "docker/setup-qemu-action@96fe6ef7f33517b61c61be40b68a1882f3264fb8"
	buildxActionRef           = "docker/setup-buildx-action@37fe631027851001ddb9b187196cc803df7f5f0e"
	dockerLoginActionRef      = "docker/login-action@dbcb813823bdd20940b903addbd779551569679f"
	dockerMetadataActionRef   = "docker/metadata-action@dc802804100637a589fabce1cb79ff13a1411302"
	dockerBuildActionRef      = "docker/build-push-action@53b7df96c91f9c12dcc8a07bcb9ccacbed38856a"
	githubReleaseActionRef    = "softprops/action-gh-release@3d0d9888cb7fd7b750713d6e236d1fcb99157228"
)

func TestWebCICompositeActionRunsCompleteFrontendGate(t *testing.T) {
	content := readRepositoryFile(t, ".github/actions/web-ci/action.yml")
	for _, required := range []string{
		pnpmSetupActionRef,
		"version: 11.17.0",
		setupNodeActionRef,
		"node-version: 24.18.0",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("web-ci action does not contain %q", required)
		}
	}

	previousIndex := -1
	for _, command := range []string{
		"pnpm --dir web install --frozen-lockfile",
		"pnpm --dir web audit --prod --audit-level high",
		"pnpm --dir web run lint",
		"pnpm --dir web run format",
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
	if strings.Contains(content, "pnpm --dir web run type-check") {
		t.Fatal("web-ci action duplicates the type-check already run by the build script")
	}
	// devDependencies 不进入 internal/webui/dist，审计它们只会让发布被无关 CVE 阻断。
	if strings.Contains(content, "pnpm --dir web audit --audit-level") {
		t.Fatal("web-ci action audits devDependencies that never reach the release artifact")
	}
	packageJSON := readRepositoryFile(t, "web/package.json")
	if !strings.Contains(packageJSON, `"build": "pnpm run type-check && vite build"`) {
		t.Fatal("web build script no longer includes the required type-check")
	}
}

func TestMakeCheckDoesNotDuplicateWebTypeCheck(t *testing.T) {
	content := readRepositoryFile(t, "Makefile")
	if strings.Contains(content, "run type-check") {
		t.Fatal("make check duplicates the type-check already run by the build script")
	}
	if !strings.Contains(content, "run build") {
		t.Fatal("make check does not build the web application")
	}
}

func TestBranchAndReleaseWorkflowsRunRaceInParallelGates(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/ci.yml")
	testJob := workflowJobBlock(t, content, "test")
	workflowGoFormattingScript(t, content, "test")
	for _, test := range []struct {
		name string
		run  string
	}{
		{name: "Check module graph", run: "go mod tidy -diff"},
		{name: "Audit Go dependencies", run: "go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./..."},
		{name: "Run Go vet", run: "go vet ./..."},
		{name: "Check repository invariants", run: "git diff --check"},
	} {
		assertWorkflowGateStep(t, testJob, test.name, test.run)
	}
	if strings.Contains(testJob, "go test -race") {
		t.Fatal("branch static job still runs race tests serially")
	}
	assertWorkflowGateStep(
		t,
		workflowJobBlock(t, content, "race-tests"),
		"Run race-enabled tests",
		"go test -race -count=1 -timeout=15m . ./internal/...",
	)
	branchCPA := workflowStepBlock(
		t,
		workflowJobBlock(t, content, "race-cpa"),
		"Run embedded CPA bridge race-enabled tests",
	)
	for _, required := range []string{
		"working-directory: third_party/cpaembedded",
		"run: go test -race -count=1 -timeout=15m ./...",
	} {
		if !strings.Contains(branchCPA, required) {
			t.Fatalf("branch CPA race gate does not contain %q:\n%s", required, branchCPA)
		}
	}

	releaseContent := readRepositoryFile(t, ".github/workflows/release.yml")
	releaseStaticJob := workflowJobBlock(
		t,
		releaseContent,
		"static-checks",
	)
	if strings.Contains(releaseStaticJob, "go test -race") {
		t.Fatal("release static job still runs race tests serially")
	}
	assertWorkflowGateStep(
		t,
		workflowJobBlock(t, releaseContent, "race-tests"),
		"Run race-enabled tests",
		"go test -race -count=1 -timeout=15m . ./internal/...",
	)
	releaseCPA := workflowStepBlock(
		t,
		workflowJobBlock(t, releaseContent, "race-cpa"),
		"Run embedded CPA bridge race-enabled tests",
	)
	for _, required := range []string{
		"working-directory: third_party/cpaembedded",
		"run: go test -race -count=1 -timeout=15m ./...",
	} {
		if !strings.Contains(releaseCPA, required) {
			t.Fatalf("release CPA race gate does not contain %q:\n%s", required, releaseCPA)
		}
	}
}

func TestWindowsCIExecutesManagedStorageACLTests(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/ci.yml")
	job := workflowJobBlock(t, content, "windows-encryption-acl")
	if count := strings.Count(job, "runs-on: windows-2025"); count != 1 {
		t.Fatalf("Windows ACL job contains runs-on declaration %d times, want exactly once", count)
	}
	assertWorkflowGateStep(
		t,
		job,
		"Test Windows secure file and storage ACLs",
		"go test -v -count=1 ./internal/platform/securefile ./internal/platform/encryption ./internal/storage",
	)
	assertWorkflowGateStep(
		t,
		job,
		"Test Windows service lifecycle",
		"go test -v -count=1 .",
	)
	serviceSmoke := workflowStepBlock(t, job, "Smoke Windows service lifecycle")
	for _, required := range []string{
		"go build",
		".github/scripts/ci-windows-service-smoke.ps1",
	} {
		if !strings.Contains(serviceSmoke, required) {
			t.Fatalf("Windows service smoke does not contain %q:\n%s", required, serviceSmoke)
		}
	}
}

func TestWorkflowsPinExternalActionsAndHostedRunners(t *testing.T) {
	actionRef := regexp.MustCompile(`(?m)^\s*uses:\s+([^#\s]+)`)
	immutableRef := regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`)
	for _, name := range []string{
		".github/actions/web-ci/action.yml",
		".github/workflows/ci.yml",
		".github/workflows/release.yml",
	} {
		content := readRepositoryFile(t, name)
		for _, match := range actionRef.FindAllStringSubmatch(content, -1) {
			ref := match[1]
			if strings.HasPrefix(ref, "./") {
				continue
			}
			if !immutableRef.MatchString(ref) {
				t.Errorf("%s contains mutable or invalid action reference %q", name, ref)
			}
		}
	}

	ci := readRepositoryFile(t, ".github/workflows/ci.yml")
	for _, required := range []string{"runs-on: ubuntu-24.04", "runs-on: windows-2025"} {
		if !strings.Contains(ci, required) {
			t.Errorf("branch CI does not contain %q", required)
		}
	}
	if strings.Contains(ci, "-latest") {
		t.Error("branch CI contains a mutable hosted runner label")
	}
}

func TestBranchWorkflowCancelsSupersededRuns(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/ci.yml")
	concurrency := workflowTopLevelBlock(t, content, "concurrency")
	for _, required := range []string{
		`group: v2-ci-${{ github.event.pull_request.number || github.ref }}`,
		"cancel-in-progress: true",
	} {
		if !strings.Contains(concurrency, required) {
			t.Fatalf("branch CI concurrency does not contain %q:\n%s", required, concurrency)
		}
	}
}

func TestWorkflowsDisableCheckoutCredentialPersistence(t *testing.T) {
	for _, name := range []string{
		".github/workflows/ci.yml",
		".github/workflows/release.yml",
	} {
		content := readRepositoryFile(t, name)
		checkoutCount := strings.Count(content, "uses: "+checkoutActionRef)
		disabledCount := strings.Count(content, "persist-credentials: false")
		if checkoutCount == 0 {
			t.Fatalf("%s does not contain a checkout action", name)
		}
		if disabledCount != checkoutCount {
			t.Fatalf(
				"%s disables checkout credential persistence %d times, want %d",
				name,
				disabledCount,
				checkoutCount,
			)
		}
	}
}

func TestGoFormattingScriptsFailClosed(t *testing.T) {
	for _, workflow := range []struct {
		name string
		file string
		job  string
	}{
		{name: "branch", file: ".github/workflows/ci.yml", job: "test"},
		{name: "release", file: ".github/workflows/release.yml", job: "static-checks"},
	} {
		t.Run(workflow.name, func(t *testing.T) {
			script := workflowGoFormattingScript(
				t,
				readRepositoryFile(t, workflow.file),
				workflow.job,
			)
			scriptPath := filepath.Join(t.TempDir(), "check-go-format.sh")
			if err := os.WriteFile(
				scriptPath,
				[]byte("#!/usr/bin/env bash\nset -e\n"+script),
				0o700,
			); err != nil {
				t.Fatalf("write Go formatting script: %v", err)
			}

			fakeBin := t.TempDir()
			fakeGofmt := filepath.Join(fakeBin, "gofmt")
			if err := os.WriteFile(fakeGofmt, []byte(`#!/usr/bin/env bash
case "${GOFMT_TEST_MODE}" in
  clean) exit 0 ;;
  unformatted) printf 'unformatted.go\n'; exit 0 ;;
  failed) exit 2 ;;
  *) exit 64 ;;
esac
`), 0o700); err != nil {
				t.Fatalf("write fake gofmt: %v", err)
			}

			for _, test := range []struct {
				name    string
				mode    string
				wantErr bool
			}{
				{name: "clean", mode: "clean"},
				{name: "unformatted", mode: "unformatted", wantErr: true},
				{name: "formatter failure", mode: "failed", wantErr: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					command := exec.Command("bash", scriptPath)
					command.Env = append(
						os.Environ(),
						"GOFMT_TEST_MODE="+test.mode,
						"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
					)
					output, err := command.CombinedOutput()
					if (err != nil) != test.wantErr {
						t.Fatalf("Go formatting check error = %v, want error %t\n%s", err, test.wantErr, output)
					}
				})
			}
		})
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
				"version=v2.0.0", "prerelease=false", "image_exact=2.0.0",
				"image_beta=", "image_major=2", "release_kind=stable",
				"channel_beta=false", "promote_major=true",
			},
		},
		{
			tag:   "v2.0.0-rc.1",
			valid: true,
			wantOutput: []string{
				"version=v2.0.0-rc.1", "prerelease=true", "image_exact=2.0.0-rc.1",
				"image_beta=", "image_major=2", "release_kind=rc",
				"channel_beta=false", "promote_major=true",
			},
		},
		{
			tag:   "v2.0.0-beta.1",
			valid: true,
			wantOutput: []string{
				"version=v2.0.0-beta.1", "prerelease=true", "image_exact=2.0.0-beta.1",
				"image_beta=2.0-beta", "image_major=2", "release_kind=beta",
				"channel_beta=true", "promote_major=true",
			},
		},
		{
			tag:   "v2.0.0-beta.0",
			valid: true,
			wantOutput: []string{
				"image_exact=2.0.0-beta.0", "image_beta=2.0-beta",
				"release_kind=beta", "channel_beta=true", "promote_major=true",
			},
		},
		{
			tag:   "v2.0.0-beta",
			valid: true,
			wantOutput: []string{
				"image_exact=2.0.0-beta", "image_beta=", "release_kind=other",
				"channel_beta=false", "promote_major=false",
			},
		},
		{
			tag:   "v2.0.0-beta.test-build-1",
			valid: true,
			wantOutput: []string{
				"version=v2.0.0-beta.test-build-1", "prerelease=true",
				"image_exact=2.0.0-beta.test-build-1", "image_beta=", "image_major=2",
				"release_kind=other", "channel_beta=false", "promote_major=false",
			},
		},
		{
			tag:   "v2.0.0-beta.1.extra",
			valid: true,
			wantOutput: []string{
				"image_exact=2.0.0-beta.1.extra", "image_beta=", "release_kind=other",
				"channel_beta=false", "promote_major=false",
			},
		},
		{
			tag:   "v2.1.0-beta.1",
			valid: true,
			wantOutput: []string{
				"version=v2.1.0-beta.1", "image_exact=2.1.0-beta.1",
				"image_beta=2.1-beta", "release_kind=beta",
				"channel_beta=true", "promote_major=false",
			},
		},
		{
			tag:   "v2.1.0-rc.1",
			valid: true,
			wantOutput: []string{
				"version=v2.1.0-rc.1", "image_exact=2.1.0-rc.1",
				"image_beta=", "release_kind=rc",
				"channel_beta=false", "promote_major=false",
			},
		},
		{
			tag:   "v2.1.0",
			valid: true,
			wantOutput: []string{
				"version=v2.1.0", "image_exact=2.1.0",
				"image_beta=", "release_kind=stable",
				"channel_beta=false", "promote_major=true",
			},
		},
		{
			tag:   "v2.10.3-alpha-1.0",
			valid: true,
			wantOutput: []string{
				"version=v2.10.3-alpha-1.0", "prerelease=true",
				"image_exact=2.10.3-alpha-1.0", "image_beta=", "image_major=2",
				"release_kind=other", "channel_beta=false", "promote_major=false",
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
				gotOutput := make(map[string]string)
				for _, line := range strings.Split(strings.TrimSpace(string(githubOutput)), "\n") {
					key, value, found := strings.Cut(line, "=")
					if !found {
						t.Fatalf("tag %s emitted malformed output %q", test.tag, line)
					}
					gotOutput[key] = value
				}
				for _, expected := range test.wantOutput {
					key, value, found := strings.Cut(expected, "=")
					if !found {
						t.Fatalf("test expectation %q is malformed", expected)
					}
					if gotOutput[key] != value {
						t.Fatalf(
							"tag %s output %s = %q, want %q:\n%s",
							test.tag,
							key,
							gotOutput[key],
							value,
							githubOutput,
						)
					}
				}
			}
		})
	}
}

func TestReleasePublicationStateClassifiesFreshConsistentConflictAndPartial(t *testing.T) {
	script := filepath.Join("..", "..", ".github", "scripts", "release-publication-state.sh")
	expectedSHA := "0123456789abcdef0123456789abcdef01234567"
	base := map[string]string{
		"RELEASE_EXPECTED_SHA":       expectedSHA,
		"RELEASE_GITHUB_STATE":       "absent",
		"RELEASE_GITHUB_TARGET_SHA":  "",
		"RELEASE_GITHUB_ASSETS":      "absent",
		"RELEASE_GHCR_STATE":         "absent",
		"RELEASE_GHCR_DIGEST":        "absent",
		"RELEASE_GHCR_REVISION":      "",
		"RELEASE_DOCKERHUB_STATE":    "absent",
		"RELEASE_DOCKERHUB_DIGEST":   "absent",
		"RELEASE_DOCKERHUB_REVISION": "",
	}
	clone := func(overrides map[string]string) map[string]string {
		values := make(map[string]string, len(base))
		for key, value := range base {
			values[key] = value
		}
		for key, value := range overrides {
			values[key] = value
		}
		return values
	}
	run := func(t *testing.T, values map[string]string) (string, error) {
		t.Helper()
		command := exec.Command("bash", script)
		command.Env = []string{"PATH=" + os.Getenv("PATH")}
		for key, value := range values {
			command.Env = append(command.Env, key+"="+value)
		}
		output, err := command.CombinedOutput()
		return string(output), err
	}
	allPresent := map[string]string{
		"RELEASE_GITHUB_STATE":       "present",
		"RELEASE_GITHUB_TARGET_SHA":  expectedSHA,
		"RELEASE_GITHUB_ASSETS":      "match",
		"RELEASE_GHCR_STATE":         "present",
		"RELEASE_GHCR_DIGEST":        "sha256:aaaaaaaa",
		"RELEASE_GHCR_REVISION":      expectedSHA,
		"RELEASE_DOCKERHUB_STATE":    "present",
		"RELEASE_DOCKERHUB_DIGEST":   "sha256:aaaaaaaa",
		"RELEASE_DOCKERHUB_REVISION": expectedSHA,
	}
	present := func(overrides map[string]string) map[string]string {
		values := clone(allPresent)
		for key, value := range overrides {
			values[key] = value
		}
		return values
	}

	for _, test := range []struct {
		name   string
		values map[string]string
		want   string
	}{
		{
			name:   "fresh",
			values: clone(nil),
			want:   "publication_state=fresh\nwrite_mode=publish\n",
		},
		{
			name:   "consistent",
			values: present(nil),
			want:   "publication_state=consistent\nwrite_mode=verify\n",
		},
		{
			name: "conflict/GitHub target",
			values: present(map[string]string{
				"RELEASE_GITHUB_TARGET_SHA": "fedcba9876543210fedcba9876543210fedcba98",
			}),
			want: "publication_state=conflict\nwrite_mode=blocked\n",
		},
		{
			name: "conflict/GitHub assets or checksum",
			values: present(map[string]string{
				"RELEASE_GITHUB_ASSETS": "mismatch",
			}),
			want: "publication_state=conflict\nwrite_mode=blocked\n",
		},
		{
			name: "conflict/GHCR revision",
			values: present(map[string]string{
				"RELEASE_GHCR_REVISION": "fedcba9876543210fedcba9876543210fedcba98",
			}),
			want: "publication_state=conflict\nwrite_mode=blocked\n",
		},
		{
			name: "conflict/Docker Hub revision",
			values: present(map[string]string{
				"RELEASE_DOCKERHUB_REVISION": "fedcba9876543210fedcba9876543210fedcba98",
			}),
			want: "publication_state=conflict\nwrite_mode=blocked\n",
		},
		{
			name: "conflict/registry digest",
			values: present(map[string]string{
				"RELEASE_DOCKERHUB_DIGEST": "sha256:bbbbbbbb",
			}),
			want: "publication_state=conflict\nwrite_mode=blocked\n",
		},
		{
			name: "partial",
			values: clone(map[string]string{
				"RELEASE_GITHUB_STATE":      "present",
				"RELEASE_GITHUB_TARGET_SHA": expectedSHA,
				"RELEASE_GITHUB_ASSETS":     "match",
			}),
			want: "publication_state=partial\nwrite_mode=blocked\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := run(t, test.values)
			if err != nil {
				t.Fatalf("classifier rejected legal state: %v\n%s", err, output)
			}
			if output != test.want {
				t.Fatalf("classifier output = %q, want %q", output, test.want)
			}
		})
	}

	for _, test := range []struct {
		name   string
		values map[string]string
	}{
		{
			name:   "missing expected sha",
			values: map[string]string{},
		},
		{
			name: "invalid state enum",
			values: clone(map[string]string{
				"RELEASE_GHCR_STATE": "unknown",
			}),
		},
		{
			name: "absent channel with digest",
			values: clone(map[string]string{
				"RELEASE_GHCR_DIGEST": "sha256:aaaaaaaa",
			}),
		},
		{
			name: "present channel without revision",
			values: clone(map[string]string{
				"RELEASE_GHCR_STATE":  "present",
				"RELEASE_GHCR_DIGEST": "sha256:aaaaaaaa",
			}),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := run(t, test.values)
			if err == nil {
				t.Fatalf("classifier accepted invalid input:\n%s", output)
			}
		})
	}
}

func TestReleaseWorkflowDoesNotRequireUntrackedAgentInstructionFiles(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	verifyJob := workflowJobBlock(t, content, "static-checks")
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
		checkoutActionRef,
		setupGoActionRef,
		uploadArtifactActionRef,
		downloadArtifactActionRef,
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
	preflight := workflowJobBlock(t, content, "publication-preflight")
	for _, dependency := range []string{
		"validate-tag",
		"verify-and-build-web",
		"static-checks",
		"race-tests",
		"race-cpa",
		"build-binaries",
		"package-checksums",
		"native-artifact-smoke",
		"docker-smoke",
	} {
		if !strings.Contains(preflight, dependency) {
			t.Fatalf("publication preflight does not need %s:\n%s", dependency, preflight)
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

func TestReleaseWorkflowDraftReadersRequestPushVisibility(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	for _, jobName := range []string{
		"publication-preflight",
		"post-publish-verify",
		"reconcile-publication",
	} {
		job := workflowJobBlock(t, content, jobName)
		if !strings.Contains(job, "contents: write") {
			t.Fatalf("%s cannot discover Draft Releases without push visibility:\n%s", jobName, job)
		}
	}
}

func TestReleaseWorkflowUsesOneSharedPublicationPreflight(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	if count := strings.Count(content, "  publication-preflight:"); count != 1 {
		t.Fatalf("publication preflight job count = %d, want exactly 1", count)
	}
	if strings.Contains(content, "  capture-channel-baseline:") {
		t.Fatal("release workflow keeps a separate channel-baseline path")
	}

	preflight := workflowJobBlock(t, content, "publication-preflight")
	for _, required := range []string{
		"name: release-assets",
		".github/scripts/release-verify-assets.sh release",
		"git merge-base --is-ancestor",
		"DOCKERHUB_READ_TOKEN: ${{ secrets.DOCKERHUB_READ_TOKEN || secrets.DOCKERHUB_TOKEN }}",
		"DOCKERHUB_TOKEN",
		"ghcr.io/tbphp/gpt-load:latest",
		"tbphp/gpt-load:latest",
		".github/scripts/release-publication-state.sh",
		"publication_state:",
		"write_mode:",
		`test "${write_mode}" != "blocked"`,
	} {
		if !strings.Contains(preflight, required) {
			t.Fatalf("publication preflight does not contain %q:\n%s", required, preflight)
		}
	}
	for _, jobName := range []string{"publish-images", "publish-github"} {
		job := workflowJobBlock(t, content, jobName)
		if !strings.Contains(job, "publication-preflight") {
			t.Fatalf("%s does not depend on the shared preflight:\n%s", jobName, job)
		}
	}
}

func TestReleaseWorkflowUsesTrustedCurrentRunChecksumForExistingRelease(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	for _, test := range []struct {
		job  string
		step string
	}{
		{
			job:  "publication-preflight",
			step: "Inventory publication channels",
		},
		{
			job:  "publish-github",
			step: "Verify existing GitHub Release",
		},
		{
			job:  "post-publish-verify",
			step: "Download and verify exact GitHub Release asset inventory",
		},
	} {
		step := workflowStepBlock(
			t,
			workflowJobBlock(t, content, test.job),
			test.step,
		)
		for _, required := range []string{
			".github/scripts/release-verify-assets.sh",
			"release/SHA256SUMS",
		} {
			if !strings.Contains(step, required) {
				t.Fatalf(
					"%s does not verify downloaded assets against the current-run checksum %q:\n%s",
					test.step,
					required,
					step,
				)
			}
		}
		if strings.Contains(step, "sha256sum --check") {
			t.Fatalf("%s executes a checksum file directly:\n%s", test.step, step)
		}
	}

	// 行为验证：远端资产即使与它自带的 SHA256SUMS 完全自洽，只要与当前 run 的
	// 可信副本不一致就必须被拒绝——远端那份校验和永远不会被执行。
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	verifier := filepath.Join(
		repositoryRoot, ".github", "scripts", "release-verify-assets.sh",
	)
	var assets []string
	for _, line := range strings.Split(
		readRepositoryFile(t, ".github/release-assets.txt"), "\n",
	) {
		if name := strings.TrimSpace(line); name != "" && name != "SHA256SUMS" {
			assets = append(assets, name)
		}
	}
	if len(assets) == 0 {
		t.Fatal("release asset manifest is empty")
	}

	build := func(directory string, replacements map[string]string) {
		t.Helper()
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create asset directory: %v", err)
		}
		checksums := &strings.Builder{}
		for _, name := range assets {
			body := "payload-" + name
			if replacement, ok := replacements[name]; ok {
				body = replacement
			}
			path := filepath.Join(directory, name)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write asset: %v", err)
			}
			fmt.Fprintf(checksums, "%x  %s\n", sha256.Sum256([]byte(body)), name)
		}
		path := filepath.Join(directory, "SHA256SUMS")
		if err := os.WriteFile(path, []byte(checksums.String()), 0o600); err != nil {
			t.Fatalf("write checksums: %v", err)
		}
	}

	workspace := t.TempDir()
	trusted := filepath.Join(workspace, "current-run")
	build(trusted, nil)
	forged := filepath.Join(workspace, "forged")
	build(forged, map[string]string{"gpt-load-linux-amd64": "malicious"})

	verify := func(directory string) error {
		command := exec.Command(
			verifier, directory, filepath.Join(trusted, "SHA256SUMS"),
		)
		command.Dir = repositoryRoot
		return command.Run()
	}
	if err := verify(trusted); err != nil {
		t.Fatalf("verifier rejected assets matching the current run: %v", err)
	}
	if err := verify(forged); err == nil {
		t.Fatal("verifier accepted self-consistent forged assets over the trusted checksum")
	}
}

func TestReleaseWorkflowKeepsUntrustedImageRevisionInsideJQComparison(t *testing.T) {
	revisionVerifier := readRepositoryFile(
		t, ".github/scripts/release-verify-image-revision.sh",
	)
	expectedSHA := "0123456789abcdef0123456789abcdef01234567"
	validation := workflowMarkedScript(
		t, revisionVerifier, "release-image-revision-validation",
	)
	scriptPath := filepath.Join(t.TempDir(), "validate-image-revision.sh")
	script := "#!/usr/bin/env bash\nset -euo pipefail\n" +
		"expected=" + expectedSHA + "\n" +
		"inspection=\"${RELEASE_TEST_INSPECTION}\"\n" +
		validation +
		"printf '%s\\n' \"${revision}\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write image revision validation script: %v", err)
	}
	inspection := func(amd64, arm64 any) string {
		value := map[string]any{
			"image": map[string]any{
				"linux/amd64": map[string]any{
					"config": map[string]any{
						"Labels": map[string]any{
							"org.opencontainers.image.revision": amd64,
						},
					},
				},
				"linux/arm64": map[string]any{
					"config": map[string]any{
						"Labels": map[string]any{
							"org.opencontainers.image.revision": arm64,
						},
					},
				},
			},
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal image inspection: %v", err)
		}
		return string(encoded)
	}
	for _, test := range []struct {
		name       string
		inspection string
		want       string
	}{
		{
			name:       "exact",
			inspection: inspection(expectedSHA, expectedSHA),
			want:       expectedSHA,
		},
		{
			name:       "newline",
			inspection: inspection(expectedSHA+"\ninjected", expectedSHA),
			want:       "mismatch",
		},
		{
			name:       "pipe",
			inspection: inspection(expectedSHA+"|injected", expectedSHA),
			want:       "mismatch",
		},
		{
			name:       "NUL",
			inspection: inspection(expectedSHA+"\x00injected", expectedSHA),
			want:       "mismatch",
		},
		{
			name:       "unequal",
			inspection: inspection("other", expectedSHA),
			want:       "mismatch",
		},
		{
			name:       "missing",
			inspection: `{}`,
			want:       "mismatch",
		},
		{
			name:       "non-string",
			inspection: inspection(123, expectedSHA),
			want:       "mismatch",
		},
		{
			name:       "malformed",
			inspection: `{`,
			want:       "mismatch",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash", scriptPath)
			command.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"RELEASE_TEST_INSPECTION=" + test.inspection,
			}
			output, err := command.Output()
			if err != nil {
				t.Fatalf("image revision validation failed: %v", err)
			}
			if got := strings.TrimSpace(string(output)); got != test.want {
				t.Fatalf("revision = %q, want %q", got, test.want)
			}
		})
	}

	// 进入 shell 的只能是受信的 ${expected} 或字面量 mismatch；
	// 实际读到的标签只允许直接写入 stderr，绝不赋给变量或送上 stdout。
	for _, assignment := range regexp.MustCompile(`\brevision=\S*`).
		FindAllString(revisionVerifier, -1) {
		switch assignment {
		case "revision=mismatch", `revision="${expected}"`:
		default:
			t.Fatalf("revision verifier assigns an untrusted value: %s", assignment)
		}
	}

	content := readRepositoryFile(t, ".github/workflows/release.yml")
	inventoryStep := workflowStepBlock(
		t,
		workflowJobBlock(t, content, "publication-preflight"),
		"Inventory publication channels",
	)
	functionStart := strings.Index(inventoryStep, "inventory_image() {")
	functionEnd := strings.Index(inventoryStep, "\n          # 用命令替换而非进程替换")
	if functionStart < 0 || functionEnd <= functionStart {
		t.Fatalf("cannot isolate inventory_image:\n%s", inventoryStep)
	}
	inventoryFunction := inventoryStep[functionStart:functionEnd]
	for _, required := range []string{
		".github/scripts/release-verify-image-revision.sh",
		"revision=mismatch",
	} {
		if !strings.Contains(inventoryFunction, required) {
			t.Fatalf("inventory_image does not contain %q:\n%s", required, inventoryFunction)
		}
	}
	for _, forbidden := range []string{
		"child_revision",
		"imagetools inspect",
		"org.opencontainers.image.revision",
	} {
		if strings.Contains(inventoryFunction, forbidden) {
			t.Fatalf(
				"inventory_image parses the untrusted label itself instead of delegating (%q):\n%s",
				forbidden,
				inventoryFunction,
			)
		}
	}

	for _, test := range []struct {
		job  string
		step string
	}{
		{
			job:  "publish-images",
			step: "Verify exact published images",
		},
		{
			job:  "post-publish-verify",
			step: "Verify published image manifests",
		},
	} {
		step := workflowStepBlock(
			t,
			workflowJobBlock(t, content, test.job),
			test.step,
		)
		if !strings.Contains(step, ".github/scripts/release-verify-image-revision.sh") {
			t.Fatalf("%s does not delegate the revision comparison:\n%s", test.step, step)
		}
		for _, forbidden := range []string{
			"org.opencontainers.image.revision",
			`revision="$(`,
		} {
			if strings.Contains(step, forbidden) {
				t.Fatalf("%s transfers a raw revision into shell (%q):\n%s", test.step, forbidden, step)
			}
		}
	}
}

func TestReleaseImageVersionAcceptsOnlyMatchingStrictSemverLabels(t *testing.T) {
	script := filepath.Join("..", "..", ".github", "scripts", "release-image-version.sh")
	fakeBin := t.TempDir()
	fakeDocker := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(
		fakeDocker,
		[]byte("#!/usr/bin/env sh\nprintf '%s' \"${RELEASE_TEST_INSPECTION}\"\n"),
		0o700,
	); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	inspection := func(amd64, arm64 any) string {
		value := map[string]any{
			"image": map[string]any{
				"linux/amd64": map[string]any{
					"config": map[string]any{"Labels": map[string]any{
						"org.opencontainers.image.version": amd64,
					}},
				},
				"linux/arm64": map[string]any{
					"config": map[string]any{"Labels": map[string]any{
						"org.opencontainers.image.version": arm64,
					}},
				},
			},
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal image inspection: %v", err)
		}
		return string(encoded)
	}

	for _, test := range []struct {
		name       string
		inspection string
		want       string
		wantErr    bool
	}{
		{name: "prefixed", inspection: inspection("v2.0.0-beta.25", "v2.0.0-beta.25"), want: "v2.0.0-beta.25"},
		{name: "unprefixed", inspection: inspection("2.1.0", "2.1.0"), want: "2.1.0"},
		{name: "different architectures", inspection: inspection("v2.0.0", "v2.0.1"), wantErr: true},
		{name: "invalid semver", inspection: inspection("v2.01.0", "v2.01.0"), wantErr: true},
		{name: "shell payload", inspection: inspection("$(touch pwned)", "$(touch pwned)"), wantErr: true},
		{name: "missing", inspection: `{}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash", script, "example.test/gpt-load:2")
			command.Env = []string{
				"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"RELEASE_TEST_INSPECTION=" + test.inspection,
			}
			output, err := command.CombinedOutput()
			if (err != nil) != test.wantErr {
				t.Fatalf("version inspection error = %v, want error %t\n%s", err, test.wantErr, output)
			}
			if !test.wantErr && strings.TrimSpace(string(output)) != test.want {
				t.Fatalf("version = %q, want %q", output, test.want)
			}
		})
	}
}

func TestReleaseWorkflowPublishesImagesBeforeGitHubRelease(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	imageJob := workflowJobBlock(t, content, "publish-images")
	releaseJob := workflowJobBlock(t, content, "publish-github")
	if strings.Contains(imageJob, "publish-github") {
		t.Fatalf("image publication depends on GitHub publication:\n%s", imageJob)
	}
	for _, dependency := range []string{"publication-preflight", "publish-images"} {
		if !strings.Contains(releaseJob, dependency) {
			t.Fatalf("GitHub publication does not need %s:\n%s", dependency, releaseJob)
		}
	}
}

func TestReleaseWorkflowPromotesVerifiedImageChannelsMonotonically(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	imageJob := workflowJobBlock(t, content, "publish-images")
	for _, forbidden := range []string{
		"Update beta channel alias",
		"Update stable major and minor aliases",
		"docker buildx imagetools create",
	} {
		if strings.Contains(imageJob, forbidden) {
			t.Fatalf("exact image publication still mutates shared channels via %q:\n%s", forbidden, imageJob)
		}
	}

	promotion := workflowJobBlock(t, content, "promote-image-channels")
	promotionScript := readRepositoryFile(
		t, ".github/scripts/release-promote-image-channels.sh",
	)
	promotionContract := promotion + "\n" + promotionScript
	for _, required := range []string{
		"- validate-tag",
		"- publication-preflight",
		"- post-publish-image-smoke",
		"- post-publish-verify",
		"group: gpt-load-v2-image-channels",
		"cancel-in-progress: false",
		"queue: max",
		"packages: write",
		"major_current: ${{ steps.promote.outputs.major_current }}",
		".github/scripts/release-image-version.sh",
		".github/scripts/release-compare-semver.py",
		"needs.validate-tag.outputs.image_beta",
		"needs.validate-tag.outputs.image_major",
		"needs.validate-tag.outputs.channel_beta",
		"needs.validate-tag.outputs.promote_major",
		".github/scripts/release-promote-image-channels.sh",
		`source="${repository}@${expected_digest}"`,
		"docker buildx imagetools create",
		`test "${promoted_digest}" = "${expected_digest}"`,
		"ghcr.io/tbphp/gpt-load:latest",
		"tbphp/gpt-load:latest",
		"ghcr_latest_digest",
		"dockerhub_latest_digest",
	} {
		if !strings.Contains(promotionContract, required) {
			t.Fatalf("image channel promotion does not contain %q:\n%s", required, promotionContract)
		}
	}
	if count := strings.Count(promotion, "uses: "+dockerLoginActionRef); count != 2 {
		t.Fatalf("image channel promotion uses the pinned Docker login action %d times, want 2:\n%s", count, promotion)
	}

	for _, forbidden := range []string{"v2beta", "image_minor", `:${GITHUB_REF_NAME}`} {
		if strings.Contains(promotionContract, forbidden) {
			t.Fatalf("image channel promotion contains retired contract %q:\n%s", forbidden, promotionContract)
		}
	}

	render := workflowJobBlock(t, content, "deploy-render")
	for _, required := range []string{
		"- promote-image-channels",
		"needs.promote-image-channels.outputs.major_current == 'true'",
		"RELEASE_VERSION: ${{ needs.validate-tag.outputs.version }}",
		"IMAGE: ghcr.io/tbphp/gpt-load:${{ needs.validate-tag.outputs.image_exact }}",
	} {
		if !strings.Contains(render, required) {
			t.Fatalf("Render deployment does not follow the verified major channel via %q:\n%s", required, render)
		}
	}
}

func TestReleaseWorkflowKeepsExactImagePublicationImmutable(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	imageJob := workflowJobBlock(t, content, "publish-images")
	exactStep := workflowStepBlock(t, imageJob, "Verify exact published images")
	for _, required := range []string{
		".github/scripts/release-verify-image-revision.sh",
		"GITHUB_SHA",
		"ghcr_exact_digest",
		"dockerhub_exact_digest",
		`test "${ghcr_exact_digest}" = "${dockerhub_exact_digest}"`,
	} {
		if !strings.Contains(exactStep, required) {
			t.Fatalf("exact image verification does not contain %q:\n%s", required, exactStep)
		}
	}
	metadataStep := workflowStepBlock(t, imageJob, "Generate exact image metadata")
	if !strings.Contains(metadataStep, "needs.validate-tag.outputs.image_exact") {
		t.Fatalf("exact image metadata does not contain the exact tag:\n%s", metadataStep)
	}
	for _, forbidden := range []string{
		"needs.validate-tag.outputs.image_beta",
		"needs.validate-tag.outputs.image_major",
		"imagetools create",
		"latest",
	} {
		if strings.Contains(metadataStep, forbidden) {
			t.Fatalf("exact image metadata contains non-exact tag %q:\n%s", forbidden, metadataStep)
		}
	}
}

func TestReleaseWorkflowMaintainsVersionedBetaChannelAlias(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	validateJob := workflowJobBlock(t, content, "validate-tag")
	for _, required := range []string{
		"image_beta: ${{ steps.tag.outputs.image_beta }}",
		"channel_beta: ${{ steps.tag.outputs.channel_beta }}",
		"promote_major: ${{ steps.tag.outputs.promote_major }}",
	} {
		if !strings.Contains(validateJob, required) {
			t.Fatalf("tag validation does not expose %q:\n%s", required, validateJob)
		}
	}
	promotion := readRepositoryFile(
		t, ".github/scripts/release-promote-image-channels.sh",
	)
	for _, required := range []string{
		`promote_channel "${image_beta}" "${promote_beta}"`,
		`promote_channel "${image_major}" "${promote_major}"`,
		"release-compare-semver.py",
	} {
		if !strings.Contains(promotion, required) {
			t.Fatalf("channel promotion does not contain %q:\n%s", required, promotion)
		}
	}

	reconciliation := workflowStepBlock(
		t,
		workflowJobBlock(t, content, "reconcile-publication"),
		"Summarize exact publication inventory and job results",
	)
	for _, required := range []string{
		"IMAGE_BETA: ${{ needs.validate-tag.outputs.image_beta }}",
		"IMAGE_MAJOR: ${{ needs.validate-tag.outputs.image_major }}",
		`"ghcr.io/tbphp/gpt-load:${IMAGE_BETA}"`,
		`"tbphp/gpt-load:${IMAGE_MAJOR}"`,
	} {
		if !strings.Contains(reconciliation, required) {
			t.Fatalf("publication reconciliation does not contain %q:\n%s", required, reconciliation)
		}
	}
}

func TestReleaseWorkflowDeploysCurrentMajorChannelToRender(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	job := workflowJobBlock(t, content, "deploy-render")

	for _, required := range []string{
		"- validate-tag",
		"- promote-image-channels",
		"needs.promote-image-channels.outputs.major_current == 'true'",
		"group: gpt-load-render",
		"cancel-in-progress: false",
		"name: render",
		"url: ${{ vars.RENDER_SERVICE_URL }}",
		"RENDER_SERVICE_ID: ${{ vars.RENDER_SERVICE_ID }}",
		"RENDER_SERVICE_URL: ${{ vars.RENDER_SERVICE_URL }}",
		"RELEASE_VERSION: ${{ needs.validate-tag.outputs.version }}",
		"IMAGE: ghcr.io/tbphp/gpt-load:${{ needs.validate-tag.outputs.image_exact }}",
		`RENDER_CLI_VERSION: "2.25.0"`,
		`RENDER_CLI_SHA256: "3b3f1f839ef36b81f12d84ac7288f1c96f9f7519b39c53fe6f866612f704e7cd"`,
	} {
		if !strings.Contains(job, required) {
			t.Fatalf("Render deployment job does not contain %q:\n%s", required, job)
		}
	}
	for _, forbidden := range []string{"v2beta", "image_minor", "RENDER_DEPLOY_HOOK_URL"} {
		if strings.Contains(job, forbidden) {
			t.Fatalf("Render deployment job contains forbidden %q:\n%s", forbidden, job)
		}
	}

	install := workflowStepBlock(t, job, "Install pinned Render CLI")
	for _, required := range []string{
		"RENDER_CLI_VERSION",
		"RENDER_CLI_SHA256",
		"sha256sum --check",
		"cli_${RENDER_CLI_VERSION}_linux_amd64.zip",
		"--retry-all-errors",
	} {
		if !strings.Contains(install, required) {
			t.Fatalf("Render CLI installation does not contain %q:\n%s", required, install)
		}
	}

	deploy := workflowStepBlock(t, job, "Deploy exact image and wait until live")
	for _, required := range []string{
		"RENDER_API_KEY: ${{ secrets.RENDER_API_KEY }}",
		"render deploys create",
		`"${RENDER_SERVICE_ID}"`,
		`--image "${IMAGE}"`,
		"--wait",
		"--confirm",
		"--output text",
	} {
		if !strings.Contains(deploy, required) {
			t.Fatalf("Render deployment step does not contain %q:\n%s", required, deploy)
		}
	}

	health := workflowStepBlock(t, job, "Verify public health endpoint")
	for _, required := range []string{
		`"${RENDER_SERVICE_URL%/}/health"`,
		"--fail",
		"deadline=$((SECONDS + 180))",
		"while true",
		"sleep 5",
		"health_response",
		`--arg version "${RELEASE_VERSION}"`,
		`.status == "ok" and .version == $version`,
	} {
		if !strings.Contains(health, required) {
			t.Fatalf("Render health verification does not contain %q:\n%s", required, health)
		}
	}
}

func TestReleaseWorkflowRetriesStaleRenderHealthVersion(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	health := workflowStepBlock(
		t,
		workflowJobBlock(t, content, "deploy-render"),
		"Verify public health endpoint",
	)
	script := workflowMarkedScript(t, health, "render-health-verification")
	scriptPath := filepath.Join(t.TempDir(), "verify-render-health.sh")
	if err := os.WriteFile(
		scriptPath,
		[]byte("#!/usr/bin/env bash\nset -euo pipefail\n"+script),
		0o700,
	); err != nil {
		t.Fatalf("write Render health verification script: %v", err)
	}

	fakeBin := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "curl-count")
	fakeCurl := `#!/usr/bin/env bash
set -euo pipefail
count=0
if [[ -f "${RENDER_HEALTH_TEST_STATE}" ]]; then
  count="$(<"${RENDER_HEALTH_TEST_STATE}")"
fi
count=$((count + 1))
printf '%s\n' "${count}" >"${RENDER_HEALTH_TEST_STATE}"
if ((count == 1)); then
  printf '{"status":"ok","version":"v2.0.0-beta.24"}\n'
else
  printf '{"status":"ok","version":"v2.0.0-beta.25"}\n'
fi
`
	if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(fakeCurl), 0o700); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	fakeJQ := `#!/usr/bin/env bash
set -euo pipefail
expected=
while (($# > 0)); do
  if [[ "$1" == "--arg" && "$2" == "version" ]]; then
    expected="$3"
    shift 3
    continue
  fi
  shift
done
body="$(cat)"
[[ "${body}" == *'"status":"ok"'* ]]
[[ "${body}" == *"\"version\":\"${expected}\""* ]]
`
	if err := os.WriteFile(filepath.Join(fakeBin, "jq"), []byte(fakeJQ), 0o700); err != nil {
		t.Fatalf("write fake jq: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(fakeBin, "sleep"),
		[]byte("#!/usr/bin/env bash\nexit 0\n"),
		0o700,
	); err != nil {
		t.Fatalf("write fake sleep: %v", err)
	}

	command := exec.Command("bash", scriptPath)
	command.Env = append(
		os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RENDER_HEALTH_TEST_STATE="+statePath,
		"RENDER_SERVICE_URL=https://gpt-load-example.onrender.com",
		"RELEASE_VERSION=v2.0.0-beta.25",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Render health verification failed: %v\n%s", err, output)
	}
	count, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read curl attempt count: %v", err)
	}
	if got := strings.TrimSpace(string(count)); got != "2" {
		t.Fatalf("curl attempts = %s, want 2", got)
	}
}

func TestReleaseWorkflowConsistentRerunIsVerifyOnly(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	for _, test := range []struct {
		job  string
		step string
		call string
	}{
		{
			job:  "publish-images",
			step: "Build and publish exact multi-platform images",
			call: dockerBuildActionRef,
		},
		{
			job:  "publish-github",
			step: "Create or update GitHub Release draft",
			call: githubReleaseActionRef,
		},
	} {
		job := workflowJobBlock(t, content, test.job)
		step := workflowStepBlock(t, job, test.step)
		for _, required := range []string{
			"needs.publication-preflight.outputs.write_mode == 'publish'",
			test.call,
		} {
			if !strings.Contains(step, required) {
				t.Fatalf("%s write step does not contain %q:\n%s", test.job, required, step)
			}
		}
	}

	imageVerification := workflowStepBlock(
		t,
		workflowJobBlock(t, content, "publish-images"),
		"Verify exact published images",
	)
	if strings.Contains(imageVerification, "write_mode == 'publish'") {
		t.Fatalf("exact image verification is disabled in consistent mode:\n%s", imageVerification)
	}
	releaseVerification := workflowStepBlock(
		t,
		workflowJobBlock(t, content, "publish-github"),
		"Verify existing GitHub Release",
	)
	if !strings.Contains(releaseVerification, "write_mode == 'verify'") {
		t.Fatalf("consistent GitHub writer does not use verify-only mode:\n%s", releaseVerification)
	}
}

func TestReleaseWorkflowAlwaysReconcilesWriterAndPostPublishResults(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	job := workflowJobBlock(t, content, "reconcile-publication")
	if !strings.Contains(job, "if: ${{ always() }}") {
		t.Fatalf("publication reconciliation does not always run:\n%s", job)
	}
	for _, dependency := range []string{
		"publication-preflight",
		"publish-images",
		"publish-github",
		"post-publish-image-smoke",
		"post-publish-verify",
	} {
		if !strings.Contains(job, dependency) {
			t.Fatalf("publication reconciliation does not need %s:\n%s", dependency, job)
		}
	}
	for _, result := range []string{
		"needs.publication-preflight.result",
		"needs.publish-images.result",
		"needs.publish-github.result",
		"needs.post-publish-image-smoke.result",
		"needs.post-publish-verify.result",
	} {
		if !strings.Contains(job, result) {
			t.Fatalf("publication reconciliation does not summarize %s:\n%s", result, job)
		}
	}
}

func TestReleaseWorkflowReconciliationHasNoWriteOperations(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	job := workflowJobBlock(t, content, "reconcile-publication")
	for _, required := range []string{
		"contents: write",
		"packages: read",
		"DOCKERHUB_READ_TOKEN",
		"manual recovery",
		"gh release view",
		".github/scripts/release-image-digest.sh",
	} {
		if !strings.Contains(job, required) {
			t.Fatalf("publication reconciliation does not contain %q:\n%s", required, job)
		}
	}
	lower := strings.ToLower(job)
	for _, forbidden := range []string{
		"packages: write",
		"softprops/action-gh-release",
		"docker/build-push-action",
		"buildx imagetools create",
		"gh release delete",
		"gh release create",
		"gh release upload",
		"docker push",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("publication reconciliation contains write/delete operation %q:\n%s", forbidden, job)
		}
	}
}

func TestReleaseWorkflowPreparesDraftsWithoutLatest(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	imageJob := workflowJobBlock(t, content, "publish-images")
	for _, required := range []string{
		dockerMetadataActionRef,
		dockerLoginActionRef,
		qemuActionRef,
		buildxActionRef,
		dockerBuildActionRef,
		"linux/amd64,linux/arm64",
		`value=${{ needs.validate-tag.outputs.image_exact }}`,
		`org.opencontainers.image.version=${{ github.ref_name }}`,
		`VERSION=${{ github.ref_name }}`,
	} {
		if !strings.Contains(imageJob, required) {
			t.Fatalf("image publication job does not contain %q:\n%s", required, imageJob)
		}
	}
	if strings.Contains(strings.ToLower(imageJob), "latest") {
		t.Fatalf("image publication job contains latest:\n%s", imageJob)
	}
	for _, forbidden := range []string{"imagetools create", "image_minor", "image_major"} {
		if strings.Contains(imageJob, forbidden) {
			t.Fatalf("exact image publication contains shared channel %q:\n%s", forbidden, imageJob)
		}
	}

	githubJob := workflowJobBlock(t, content, "publish-github")
	draftStep := workflowStepBlock(t, githubJob, "Create or update GitHub Release draft")
	for _, required := range []string{
		"draft: true",
		"prerelease: ${{ needs.validate-tag.outputs.prerelease }}",
		"make_latest: false",
		"generate_release_notes: true",
	} {
		if !strings.Contains(draftStep, required) {
			t.Fatalf("GitHub Release draft does not contain %q:\n%s", required, draftStep)
		}
	}
	for _, forbidden := range []string{
		"draft: false",
		"Publish one GitHub Release",
	} {
		if strings.Contains(githubJob, forbidden) {
			t.Fatalf("GitHub Release draft job contains direct publication contract %q:\n%s", forbidden, githubJob)
		}
	}
	for _, verification := range []struct {
		job  string
		step string
	}{
		{job: "publish-github", step: "Verify existing GitHub Release"},
		{job: "post-publish-verify", step: "Verify GitHub Release channel metadata"},
	} {
		step := workflowStepBlock(
			t,
			workflowJobBlock(t, content, verification.job),
			verification.step,
		)
		for _, required := range []string{
			`test "$(jq -r '.isDraft' <<<"${metadata}")" = "true"`,
			`test "$(jq -r '.isPrerelease' <<<"${metadata}")" = \`,
			`repos/${GITHUB_REPOSITORY}/releases/latest`,
			`test "${latest_tag}" != "${GITHUB_REF_NAME}"`,
		} {
			if !strings.Contains(step, required) {
				t.Fatalf("%s does not contain %q:\n%s", verification.step, required, step)
			}
		}
	}

	inventoryStep := workflowStepBlock(
		t,
		workflowJobBlock(t, content, "publication-preflight"),
		"Inventory publication channels",
	)
	for _, required := range []string{
		"gh release view",
		"--json databaseId,targetCommitish,isDraft,isPrerelease,assets",
		`test "${github_draft}" = "true"`,
		`test "${github_prerelease}" = \`,
	} {
		if !strings.Contains(inventoryStep, required) {
			t.Fatalf("draft-aware publication inventory does not contain %q:\n%s", required, inventoryStep)
		}
	}
	if strings.Contains(inventoryStep, "/releases/tags/") {
		t.Fatalf("publication inventory uses an endpoint that cannot discover drafts:\n%s", inventoryStep)
	}
}

func TestReleaseWorkflowKeepsReleaseNotesConciseAndWarnsAboutDataIncompatibility(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	releaseJob := workflowJobBlock(t, content, "publish-github")
	draftStep := workflowStepBlock(t, releaseJob, "Create or update GitHub Release draft")

	if !strings.Contains(draftStep, "name: ${{ github.ref_name }}") {
		t.Fatalf("release draft title must be the tag version only:\n%s", draftStep)
	}
	if strings.Contains(draftStep, "name: GPT-Load ${{ github.ref_name }}") {
		t.Fatalf("release draft title must not include the product prefix:\n%s", draftStep)
	}

	// generate_release_notes 已经从 commit 历史生成"本次变更"部分，手写 body
	// 只需要保留一次性读不到就可能造成数据损坏的警告，其余标准运维信息
	// （备份、tag 语义、usage 是 estimate 等）都是跨版本不变的事实，交给 README
	// 作单一事实源，不在每个 release 里复述一遍。
	if !strings.Contains(draftStep, "generate_release_notes: true") {
		t.Fatalf("release draft no longer generates notes from commit history:\n%s", draftStep)
	}

	bodyStart := strings.Index(draftStep, "body: |")
	if bodyStart < 0 {
		t.Fatalf("release draft step has no body:\n%s", draftStep)
	}
	body := draftStep[bodyStart:]

	// 面向中文用户众多的社区，body 先给一段英文，再给对应的中文翻译；
	// 两段必须表达同一条警告，不能只翻一半或改变语义。
	paragraphs := strings.SplitN(body, "\n\n", 2)
	if len(paragraphs) != 2 {
		t.Fatalf("release notes are not split into exactly one English and one Chinese paragraph:\n%s", body)
	}
	english, chinese := paragraphs[0], paragraphs[1]

	for _, required := range []string{
		"not compatible with 1.x",
		"start 2.x from an empty database",
		"new `encryption.key`",
	} {
		if !strings.Contains(english, required) {
			t.Fatalf("English release notes do not contain %q:\n%s", required, english)
		}
	}
	for _, required := range []string{
		"1.x 数据不兼容",
		"全新数据库",
		"新的 `encryption.key`",
	} {
		if !strings.Contains(chinese, required) {
			t.Fatalf("Chinese release notes do not contain %q:\n%s", required, chinese)
		}
	}
	if strings.Contains(body, "app.notion.com") {
		t.Fatalf("public release notes depend on a private Notion page:\n%s", body)
	}
	// README 没有 "public operations baseline" 锚点，链接目标必须真实存在。
	if strings.Contains(body, "#public-operations-baseline") {
		t.Fatalf("release notes link to a heading README does not have:\n%s", body)
	}

	// 手写 body 只保留数据不兼容这一条警告（英文 + 中文各一段）；部署、备份、
	// tag 语义、usage 估算等标准信息一律不在这里重复，回归就说明有内容又被
	// 搬回了每次发布都要复述的老路。用 rune 计数而非词数，因为中文没有空格分词。
	if runeCount := len([]rune(body)); runeCount > 700 {
		t.Fatalf("release notes body has %d runes, want at most 700:\n%s", runeCount, body)
	}
}

func TestCommunityTemplatesCaptureVersionCompatibilityAndSecretSafety(t *testing.T) {
	bug := readRepositoryFile(t, ".github/ISSUE_TEMPLATE/bug_report.md")
	for _, required := range []string{
		"当前主版本线的最新补丁版本",
		"GPT-Load 版本",
		"1.x 或 2.x",
		"部署方式",
		"操作系统与架构",
		"数据库",
		"客户端协议",
		"实际结果及脱敏日志",
		"AUTH_KEY",
		"ENCRYPTION_KEY",
		"AccessKey",
	} {
		if !strings.Contains(bug, required) {
			t.Fatalf("bug report template does not contain %q", required)
		}
	}

	feature := readRepositoryFile(t, ".github/ISSUE_TEMPLATE/feature_request.md")
	for _, required := range []string{
		"当前主版本线的最新补丁版本",
		"目标版本线",
		"1.x 维护线或 2.x",
		"应用场景",
		"兼容性、数据与安全影响",
		"不要提交任何凭据或令牌",
	} {
		if !strings.Contains(feature, required) {
			t.Fatalf("feature request template does not contain %q", required)
		}
	}

	pullRequest := readRepositoryFile(t, ".github/pull_request_template.md")
	for _, heading := range []string{
		"### 关联 Issue / Related Issue",
		"### 变更内容 / Change Content",
		"### 自查清单 / Checklist",
	} {
		if count := strings.Count(pullRequest, heading); count != 1 {
			t.Fatalf("pull request template contains heading %q %d times, want once", heading, count)
		}
	}
	for _, required := range []string{
		"make check",
		"未包含无关改动",
		"敏感信息",
		"兼容性或数据迁移",
	} {
		if !strings.Contains(pullRequest, required) {
			t.Fatalf("pull request template does not contain %q", required)
		}
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
	nativeShellImplementation := readRepositoryFile(t, ".github/scripts/release-native-smoke.sh")
	nativePowerShellImplementation := readRepositoryFile(t, ".github/scripts/release-native-smoke.ps1")
	nativeImplementation := nativeJob + nativeShellImplementation + nativePowerShellImplementation
	for _, required := range []string{
		"name: binary-${{ matrix.filename }}",
		"name: release-checksums",
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
		"WaitForSingleObject",
		"GetExitCodeProcess",
		"$process.WaitForExit(15000)",
		"$exitCode = $process.GetExitCode()",
		"if ($exitCode -ne 0)",
		"if (-not $process.HasExited)",
		"$process.Dispose()",
	} {
		if !strings.Contains(nativeImplementation, required) {
			t.Fatalf("native smoke does not contain %q", required)
		}
	}
	for name, implementation := range map[string]string{
		"POSIX":   nativeShellImplementation,
		"Windows": nativePowerShellImplementation,
	} {
		if !strings.Contains(implementation, "Idempotency-Key") {
			t.Fatalf("%s native smoke does not send Idempotency-Key", name)
		}
		if strings.Contains(implementation, "00000000-0000-4000-8000-") {
			t.Fatalf("%s native smoke reuses a fixed Idempotency-Key", name)
		}
	}
	if !strings.Contains(nativeShellImplementation, "uuid.uuid4()") {
		t.Fatal("POSIX native smoke does not generate a UUIDv4 for its write")
	}
	if !strings.Contains(nativePowerShellImplementation, "[guid]::NewGuid()") {
		t.Fatal("Windows native smoke does not generate a GUID for its write")
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
	if strings.Contains(nativePowerShellImplementation, "Process.GetProcessById") {
		t.Fatal("Windows native smoke closes the original process handle and reopens the process by ID")
	}
}

func TestWindowsNativeSmokeMatchesManagedStorageACLContract(t *testing.T) {
	script := readRepositoryFile(t, ".github/scripts/release-native-smoke.ps1")
	for _, required := range []string{
		"Assert-CurrentUserOnlyAcl",
		"[bool]$RequireProtected",
		"if ($RequireProtected -and -not $acl.AreAccessRulesProtected)",
		"$_.IsInherited",
		"managed path DACL is neither protected nor inherited: $Path",
		"@{ Path = $dataDir; RequireProtected = $true }",
		"@{ Path = $authFile; RequireProtected = $true }",
		"@{ Path = $encryptionFile; RequireProtected = $true }",
		"@{ Path = $databaseFile; RequireProtected = $false }",
		"@{ Path = $walFile; RequireProtected = $false }",
		"@{ Path = $shmFile; RequireProtected = $false }",
		"-RequireProtected $target.RequireProtected",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("Windows native smoke does not contain %q", required)
		}
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
		"RELEASE_SMOKE_TRIVY_IMAGE",
		"--severity CRITICAL,HIGH",
		"--ignore-unfixed",
		"--exit-code 1",
		"10001:10001",
		"/app/data",
		"auth.key",
		"encryption.key",
		"gpt-load.db",
		"/api/usage",
		"/api/model-prices",
		"/api/groups",
		"connection_type:\"api_key\"",
		"/api/access-keys",
		"value.data.items.some(item=>item.name===\"Task13 Release Smoke Access\")",
		"/v1/chat/completions",
		"finish_reason",
		"prompt_tokens",
		"completion_tokens",
		"docker stop --time 15",
		"container-first.log",
		"container-second.log",
		"secret_free=true",
		"Idempotency-Key",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("release Docker smoke does not contain %q", required)
		}
	}
	if strings.Contains(script, "00000000-0000-4000-8000-") {
		t.Fatal("release Docker smoke reuses fixed Idempotency-Keys")
	}
	if strings.Count(script, "uuid.uuid4()") < 2 {
		t.Fatal("release Docker smoke does not generate independent UUIDv4 keys for its writes")
	}
}

func TestReleaseDockerSmokeUsesIsolatedFakeUpstreamNetwork(t *testing.T) {
	script := readRepositoryFile(t, ".github/scripts/release-docker-smoke.sh")
	for _, required := range []string{
		`fake_container="gpt-load-release-fake-${suffix}"`,
		`network="gpt-load-release-network-${suffix}"`,
		`fake_alias="fake-upstream"`,
		`docker network create "${network}"`,
		`--network-alias "${fake_alias}"`,
		`exec nc -lk -p 8080 -e /tmp/respond`,
		`"http://${fake_alias}:8080/v1"`,
		`docker network rm "${network}"`,
		"release Docker smoke failed at stage %s; captured output withheld to protect credentials",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("release Docker smoke does not contain %q", required)
		}
	}
	for _, forbidden := range []string{
		"host.docker.internal",
		"fake_upstream.py",
		"RELEASE_SMOKE_FAKE_PORT",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("release Docker smoke retains host-dependent fake upstream contract %q", forbidden)
		}
	}
}

func TestReleaseDockerSmokeUsesCompleteModelPriceReplacement(t *testing.T) {
	script := readRepositoryFile(t, ".github/scripts/release-docker-smoke.sh")
	start := strings.Index(script, `api_write PUT "/api/model-prices/${model_price_id}"`)
	if start < 0 {
		t.Fatal("release Docker smoke does not update the selected model price")
	}
	endOffset := strings.Index(script[start:], `>"${task_tmp}/price-update.json"`)
	if endOffset < 0 {
		t.Fatal("release Docker smoke model price update has no response target")
	}
	request := script[start : start+endOffset]
	for _, required := range []string{
		`"input":"1"`,
		`"output":"2"`,
		`"cache_read":"3"`,
		`"cache_write":"4"`,
		`"context_tiers":[]`,
		`"mode_schedules":{}`,
		`"confirm_unpriced":false`,
	} {
		if !strings.Contains(request, required) {
			t.Fatalf("release Docker smoke model price replacement does not contain %q", required)
		}
	}
}

func TestReleaseDockerSmokeDefersOwnedResourceCleanupUntilAfterConflictChecks(t *testing.T) {
	script := readRepositoryFile(t, ".github/scripts/release-docker-smoke.sh")
	tempTrapIndex := strings.Index(script, "trap cleanup_temp EXIT")
	containerCheckIndex := strings.Index(script, `for target in "${container}" "${probe}" "${fake_container}"; do`)
	conflictExitIndex := strings.Index(script, "task owned Docker resource already exists")
	fullTrapIndex := strings.Index(script, "trap cleanup EXIT")
	workStartIndex := strings.Index(script, `if [[ -n "${source_image}" ]]; then`)
	preflightExitIndex := -1
	if conflictExitIndex >= 0 {
		if offset := strings.Index(script[conflictExitIndex:], "exit 1"); offset >= 0 {
			preflightExitIndex = conflictExitIndex + offset
		}
	}
	for name, index := range map[string]int{
		"temporary cleanup trap":       tempTrapIndex,
		"container conflict check":     containerCheckIndex,
		"owned resource conflict exit": conflictExitIndex,
		"completed conflict exit":      preflightExitIndex,
		"owned-resource cleanup trap":  fullTrapIndex,
		"owned-resource work":          workStartIndex,
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

func TestReleaseWorkflowPostPublishVerifiesExactDigestsBeforePromotion(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	job := workflowJobBlock(t, content, "post-publish-verify")
	for _, required := range []string{
		"packages: read",
		dockerLoginActionRef,
		"registry: ghcr.io",
		"exact_digest",
		"ghcr.io/tbphp/gpt-load",
		"tbphp/gpt-load",
	} {
		if !strings.Contains(job, required) {
			t.Fatalf("post-publish verification does not contain %q:\n%s", required, job)
		}
	}
	for _, forbidden := range []string{"image_minor", "image_major", "alias_digest", "v2beta"} {
		if strings.Contains(job, forbidden) {
			t.Fatalf("exact post-publication verification contains channel %q:\n%s", forbidden, job)
		}
	}
}

func TestReleaseWorkflowPostPublishVerifiesDraftAssetsAgainstCurrentRun(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	inventoryJob := workflowJobBlock(t, content, "post-publish-verify")
	for _, required := range []string{
		"gh release download",
		"draft-release",
		"name: release-assets",
		".github/scripts/release-verify-assets.sh",
		"release/SHA256SUMS",
	} {
		if !strings.Contains(inventoryJob, required) {
			t.Fatalf("post-publish asset inventory does not contain %q:\n%s", required, inventoryJob)
		}
	}

	// 已上传的原生产物不再重复跑第二遍五平台运行时 smoke：它们与发布前
	// native-artifact-smoke 验证过的字节完全相同，这一点由上面对照当前 run
	// 可信 SHA256SUMS 的校验保证，重复运行不会产生新信息。
	if strings.Contains(content, "post-publish-native-smoke") {
		t.Fatal("release workflow reintroduces the duplicated post-publication native smoke")
	}

	// 发布前的五平台原生 smoke 仍然是必须的门禁。
	nativeJob := workflowJobBlock(t, content, "native-artifact-smoke")
	for _, required := range []string{
		"ubuntu-24.04",
		"ubuntu-24.04-arm",
		"macos-15-intel",
		"macos-15",
		"windows-2025",
		"gpt-load-linux-amd64",
		"gpt-load-linux-arm64",
		"gpt-load-macos-amd64",
		"gpt-load-macos-arm64",
		"gpt-load-windows-amd64.exe",
		".github/scripts/release-native-smoke.sh",
		".github/scripts/release-native-smoke.ps1",
	} {
		if !strings.Contains(nativeJob, required) {
			t.Fatalf("pre-publication native smoke does not contain %q:\n%s", required, nativeJob)
		}
	}
}

func TestReleaseWorkflowRunsBothPublishedImagesAndPreservesLatest(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	snapshotJob := workflowJobBlock(t, content, "publication-preflight")
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
	if !strings.Contains(imagePublication, "publication-preflight") {
		t.Fatal("image publication is not ordered after the latest digest snapshot")
	}

	postPublish := workflowJobBlock(t, content, "post-publish-verify")
	for _, required := range []string{
		"publication-preflight",
		".github/scripts/release-image-digest.sh",
		"ghcr_latest_digest",
		"dockerhub_latest_digest",
	} {
		if !strings.Contains(postPublish, required) {
			t.Fatalf("published image runtime verification does not contain %q:\n%s", required, postPublish)
		}
	}
	runtimeSmoke := workflowJobBlock(t, content, "post-publish-image-smoke")
	for _, required := range []string{
		"RELEASE_SMOKE_SOURCE_IMAGE",
		"ghcr.io/tbphp/gpt-load",
		"needs.validate-tag.outputs.image_exact",
		".github/scripts/release-docker-smoke.sh",
	} {
		if !strings.Contains(runtimeSmoke, required) {
			t.Fatalf("published image runtime smoke does not contain %q:\n%s", required, runtimeSmoke)
		}
	}
	// Docker Hub 的 exact tag 与 GHCR 同 digest，只在 post-publish-verify 里以只读凭据
	// 校验 manifest 并拉取一次，确认 layer 可达。
	if !strings.Contains(postPublish, "Verify Docker Hub exact image is pullable") ||
		!strings.Contains(postPublish, "docker pull") {
		t.Fatalf("post-publication verification never pulls the Docker Hub image:\n%s", postPublish)
	}
	for name, block := range map[string]string{
		"publication-preflight": snapshotJob,
		"post-publish-verify":   postPublish,
	} {
		for _, required := range []string{
			"Log in to Docker Hub",
			"username: ${{ secrets.DOCKERHUB_USERNAME }}",
			"password: ${{ secrets.DOCKERHUB_READ_TOKEN || secrets.DOCKERHUB_TOKEN }}",
		} {
			if !strings.Contains(block, required) {
				t.Fatalf("%s does not contain authenticated Docker Hub read %q", name, required)
			}
		}
		if strings.Contains(block, "password: ${{ secrets.DOCKERHUB_TOKEN }}") {
			t.Fatalf("%s bypasses the optional Docker Hub read-only token", name)
		}
	}
	imageReadLogin := workflowStepBlock(
		t,
		imagePublication,
		"Log in to Docker Hub for exact verification",
	)
	for _, required := range []string{
		"write_mode == 'verify'",
		"password: ${{ secrets.DOCKERHUB_READ_TOKEN || secrets.DOCKERHUB_TOKEN }}",
	} {
		if !strings.Contains(imageReadLogin, required) {
			t.Fatalf("consistent image verification login does not contain %q", required)
		}
	}
	if strings.Contains(imageReadLogin, "password: ${{ secrets.DOCKERHUB_TOKEN }}") {
		t.Fatal("consistent image verification bypasses the optional read-only token")
	}
	imageWriteLogin := workflowStepBlock(
		t,
		imagePublication,
		"Log in to Docker Hub for publication",
	)
	for _, required := range []string{
		"write_mode == 'publish'",
		"password: ${{ secrets.DOCKERHUB_TOKEN }}",
	} {
		if !strings.Contains(imageWriteLogin, required) {
			t.Fatalf("fresh image publication login does not contain %q", required)
		}
	}
	if strings.Contains(imageWriteLogin, "DOCKERHUB_READ_TOKEN") {
		t.Fatal("fresh image publication unexpectedly uses the Docker Hub read-only token")
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

func TestReleaseSemverComparatorOrdersChannelCandidates(t *testing.T) {
	script := filepath.Join("..", "..", ".github", "scripts", "release-compare-semver.py")
	for _, test := range []struct {
		name  string
		left  string
		right string
		want  string
	}{
		{name: "beta sequence", left: "v2.0.0-beta.24", right: "v2.0.0-beta.25", want: "-1"},
		{name: "beta before rc", left: "v2.0.0-beta.25", right: "v2.0.0-rc.1", want: "-1"},
		{name: "rc before stable", left: "v2.0.0-rc.2", right: "v2.0.0", want: "-1"},
		{name: "stable after rc", left: "v2.0.0", right: "v2.0.0-rc.99", want: "1"},
		{name: "next minor", left: "v2.0.9", right: "2.1.0", want: "-1"},
		{name: "equal with optional prefix", left: "v2.1.0", right: "2.1.0", want: "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("python3", script, test.left, test.right)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("compare %s and %s: %v\n%s", test.left, test.right, err, output)
			}
			if got := strings.TrimSpace(string(output)); got != test.want {
				t.Fatalf("compare %s and %s = %q, want %q", test.left, test.right, got, test.want)
			}
		})
	}

	for _, invalid := range []string{"2.01.0", "v2.0", "2.0.0-rc.01", "$(touch pwned)"} {
		t.Run("invalid/"+invalid, func(t *testing.T) {
			command := exec.Command("python3", script, invalid, "2.0.0")
			if output, err := command.CombinedOutput(); err == nil {
				t.Fatalf("invalid version %q accepted: %s", invalid, output)
			}
		})
	}
}

func TestReleaseImageChannelPromotionIsMonotonicAndPreservesLatest(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	statePath := filepath.Join(t.TempDir(), "registry.json")
	fakeBin := t.TempDir()
	fakeDocker := filepath.Join(fakeBin, "docker")
	fakeDockerBody := `#!/usr/bin/env python3
import json
import os
import sys

state_path = os.environ["FAKE_REGISTRY_STATE"]
with open(state_path, encoding="utf-8") as source:
    state = json.load(source)

args = sys.argv[1:]
if args[:3] == ["buildx", "imagetools", "inspect"]:
    image = args[3]
    record = state.get(image)
    if record is None:
        print(f"ERROR: {image}: not found", file=sys.stderr)
        raise SystemExit(1)
    output_format = args[args.index("--format") + 1]
    if output_format == "{{.Manifest.Digest}}":
        print(record["digest"], end="")
    else:
        labels = {
            "org.opencontainers.image.revision": record["revision"],
            "org.opencontainers.image.version": record["version"],
        }
        print(json.dumps({"image": {
            "linux/amd64": {"config": {"Labels": labels}},
            "linux/arm64": {"config": {"Labels": labels}},
        }}), end="")
elif args[:3] == ["buildx", "imagetools", "create"]:
    target = args[args.index("--tag") + 1]
    source = args[-1]
    repository, digest = source.rsplit("@", 1)
    matches = [
        record for image, record in state.items()
        if image.startswith(repository + ":") and record["digest"] == digest
    ]
    if not matches:
        print(f"source not found: {source}", file=sys.stderr)
        raise SystemExit(1)
    state[target] = dict(matches[0])
    with open(state_path, "w", encoding="utf-8") as destination:
        json.dump(state, destination, sort_keys=True)
else:
    print(f"unsupported docker invocation: {args}", file=sys.stderr)
    raise SystemExit(2)
`
	if err := os.WriteFile(fakeDocker, []byte(fakeDockerBody), 0o700); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	type imageRecord struct {
		Digest   string `json:"digest"`
		Revision string `json:"revision"`
		Version  string `json:"version"`
	}
	writeState := func(state map[string]imageRecord) {
		t.Helper()
		encoded, marshalErr := json.Marshal(state)
		if marshalErr != nil {
			t.Fatalf("marshal registry state: %v", marshalErr)
		}
		if writeErr := os.WriteFile(statePath, encoded, 0o600); writeErr != nil {
			t.Fatalf("write registry state: %v", writeErr)
		}
	}
	readState := func() map[string]imageRecord {
		t.Helper()
		encoded, readErr := os.ReadFile(statePath)
		if readErr != nil {
			t.Fatalf("read registry state: %v", readErr)
		}
		var state map[string]imageRecord
		if unmarshalErr := json.Unmarshal(encoded, &state); unmarshalErr != nil {
			t.Fatalf("decode registry state: %v", unmarshalErr)
		}
		return state
	}

	candidateDigest := "sha256:" + strings.Repeat("a", 64)
	previousDigest := "sha256:" + strings.Repeat("b", 64)
	latestDigest := "sha256:" + strings.Repeat("c", 64)
	candidateRevision := strings.Repeat("1", 40)
	previousRevision := strings.Repeat("2", 40)
	state := map[string]imageRecord{}
	for _, repository := range []string{"ghcr.io/tbphp/gpt-load", "tbphp/gpt-load"} {
		state[repository+":2.0.0-beta.25"] = imageRecord{
			Digest: candidateDigest, Revision: candidateRevision, Version: "v2.0.0-beta.25",
		}
		state[repository+":2.0-beta"] = imageRecord{
			Digest: previousDigest, Revision: previousRevision, Version: "v2.0.0-beta.24",
		}
		state[repository+":latest"] = imageRecord{
			Digest: latestDigest, Revision: previousRevision, Version: "v1.4.10",
		}
	}
	writeState(state)

	runPromotion := func(version, exact, revision string) map[string]string {
		t.Helper()
		outputPath := filepath.Join(t.TempDir(), "github-output")
		command := exec.Command(
			"bash", ".github/scripts/release-promote-image-channels.sh",
		)
		command.Dir = repositoryRoot
		command.Env = append(os.Environ(),
			"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"FAKE_REGISTRY_STATE="+statePath,
			"RELEASE_VERSION="+version,
			"IMAGE_EXACT="+exact,
			"IMAGE_BETA=2.0-beta",
			"IMAGE_MAJOR=2",
			"PROMOTE_BETA=true",
			"PROMOTE_MAJOR=true",
			"EXPECTED_REVISION="+revision,
			"EXPECTED_GHCR_LATEST="+latestDigest,
			"EXPECTED_DOCKERHUB_LATEST="+latestDigest,
			"GITHUB_OUTPUT="+outputPath,
		)
		if output, runErr := command.CombinedOutput(); runErr != nil {
			t.Fatalf("promote %s: %v\n%s", version, runErr, output)
		}
		encoded, readErr := os.ReadFile(outputPath)
		if readErr != nil {
			t.Fatalf("read promotion outputs: %v", readErr)
		}
		outputs := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(encoded)), "\n") {
			key, value, found := strings.Cut(line, "=")
			if !found {
				t.Fatalf("malformed promotion output %q", line)
			}
			outputs[key] = value
		}
		return outputs
	}

	outputs := runPromotion("v2.0.0-beta.25", "2.0.0-beta.25", candidateRevision)
	if outputs["beta_current"] != "true" || outputs["major_current"] != "true" {
		t.Fatalf("promotion outputs = %#v, want both channels current", outputs)
	}
	state = readState()
	for _, repository := range []string{"ghcr.io/tbphp/gpt-load", "tbphp/gpt-load"} {
		for _, alias := range []string{"2.0-beta", "2"} {
			if got := state[repository+":"+alias].Digest; got != candidateDigest {
				t.Fatalf("%s:%s digest = %q, want %q", repository, alias, got, candidateDigest)
			}
		}
		if got := state[repository+":latest"].Digest; got != latestDigest {
			t.Fatalf("%s:latest digest = %q, want preserved %q", repository, got, latestDigest)
		}
	}

	delete(state, "tbphp/gpt-load:2")
	writeState(state)
	outputs = runPromotion("v2.0.0-beta.25", "2.0.0-beta.25", candidateRevision)
	if outputs["major_current"] != "true" {
		t.Fatalf("repair outputs = %#v, want major channel current", outputs)
	}
	state = readState()
	if got := state["tbphp/gpt-load:2"].Digest; got != candidateDigest {
		t.Fatalf("partial Docker Hub channel repair digest = %q, want %q", got, candidateDigest)
	}

	olderDigest := "sha256:" + strings.Repeat("d", 64)
	olderRevision := strings.Repeat("3", 40)
	for _, repository := range []string{"ghcr.io/tbphp/gpt-load", "tbphp/gpt-load"} {
		state[repository+":2.0.0-beta.24"] = imageRecord{
			Digest: olderDigest, Revision: olderRevision, Version: "v2.0.0-beta.24",
		}
	}
	writeState(state)
	outputs = runPromotion("v2.0.0-beta.24", "2.0.0-beta.24", olderRevision)
	if outputs["beta_current"] != "false" || outputs["major_current"] != "false" {
		t.Fatalf("older promotion outputs = %#v, want both channels skipped", outputs)
	}
	state = readState()
	for _, repository := range []string{"ghcr.io/tbphp/gpt-load", "tbphp/gpt-load"} {
		for _, alias := range []string{"2.0-beta", "2"} {
			if got := state[repository+":"+alias].Digest; got != candidateDigest {
				t.Fatalf("older run rolled back %s:%s to %q", repository, alias, got)
			}
		}
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
		`model_price_list_path="/api/model-prices?usage=in_use&status=all&page=1&page_size=100"`,
		`api_write PUT "/api/model-prices/${model_price_id}"`,
		`item.channel_id==="openai_compatible"&&item.model_id===modelID`,
		`item.model_id===modelID`,
		`!Number.isSafeInteger(matches[0].id)||matches[0].id<=0`,
		`api_get "${model_price_list_path}" >"${task_tmp}/prices-second.json"`,
		`matches[0].id!==priceID`,
		`matches[0].method!=="user_set"||matches[0].pricing_status!=="configured"`,
		`persisted.input!=="1"||persisted.output!=="2"`,
		`persisted.cache_read!=="3"||persisted.cache_write!=="4"`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("release Docker smoke source-image mode does not contain %q", required)
		}
	}
	for _, legacy := range []string{
		"prices.data.overrides",
		`"pattern":"task13-release-model"`,
		`"cache_write_5m"`,
		`"cache_write_1h"`,
	} {
		if strings.Contains(script, legacy) {
			t.Fatalf("release Docker smoke retains legacy model-price contract %q", legacy)
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

func TestDockerfilePinsBuildAndRuntimeImagesByVersionAndDigest(t *testing.T) {
	content := readRepositoryFile(t, "Dockerfile")
	for _, required := range []string{
		"node:24.18.0-alpine3.24@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd",
		"pnpm@11.17.0",
		"golang:1.27.0-alpine3.24@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc",
		"alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("Dockerfile does not contain %q", required)
		}
	}
}

func TestDockerfileRuntimePinsPatchedOpenSSLPackages(t *testing.T) {
	content := readRepositoryFile(t, "Dockerfile")
	runtimeStart := strings.Index(content, "\nFROM alpine:")
	if runtimeStart < 0 {
		t.Fatal("Dockerfile does not contain the shared runtime stage")
	}
	runtimeEnd := strings.Index(content[runtimeStart+1:], "\nFROM ")
	if runtimeEnd < 0 {
		t.Fatal("Dockerfile runtime stage has no following stage")
	}
	runtimeStage := content[runtimeStart : runtimeStart+1+runtimeEnd]

	upgrade := "apk add --no-cache libcrypto3=3.5.8-r0 libssl3=3.5.8-r0"
	upgradeIndex := strings.Index(runtimeStage, upgrade)
	if upgradeIndex < 0 {
		t.Fatalf("Dockerfile runtime stage does not pin patched OpenSSL packages via %q", upgrade)
	}
	packageInstallIndex := strings.Index(runtimeStage, "apk add --no-cache ca-certificates tzdata")
	if packageInstallIndex < 0 {
		t.Fatal("Dockerfile runtime stage does not install certificate and timezone packages")
	}
	if upgradeIndex >= packageInstallIndex {
		t.Fatal("Dockerfile upgrades OpenSSL after installing certificate and timezone packages")
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

func workflowStepBlock(t *testing.T, job, name string) string {
	t.Helper()
	lines := strings.Split(job, "\n")
	start := -1
	for index, line := range lines {
		if line == "      - name: "+name {
			start = index
			break
		}
	}
	if start < 0 {
		t.Fatalf("workflow job does not contain step %s", name)
	}
	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		if strings.HasPrefix(lines[index], "      - name: ") {
			end = index
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func assertWorkflowGateStep(t *testing.T, job, name, wantRun string) {
	t.Helper()
	step := strings.TrimSuffix(workflowStepBlock(t, job, name), "\n")
	want := "      - name: " + name + "\n        run: " + wantRun
	if step != want {
		t.Fatalf("workflow gate %s block does not match strict allowlist:\ngot:\n%s\nwant:\n%s", name, step, want)
	}
}

func workflowGoFormattingScript(t *testing.T, content, job string) string {
	t.Helper()
	const marker = "go-format-check"
	startMarker := "# " + marker + ":start"
	endMarker := "# " + marker + ":end"
	jobBlock := workflowJobBlock(t, content, job)
	step := workflowStepBlock(t, jobBlock, "Check Go formatting")
	for scope, block := range map[string]string{
		"formatting step": step,
		"workflow":        content,
	} {
		if startCount, endCount := strings.Count(block, startMarker), strings.Count(block, endMarker); startCount != 1 || endCount != 1 {
			t.Fatalf("%s contains format markers start=%d end=%d, want exactly one pair", scope, startCount, endCount)
		}
	}
	want := strings.Join([]string{
		"      - name: Check Go formatting",
		"        run: |",
		"          # go-format-check:start",
		`          formatted_files="$(gofmt -l .)"`,
		`          test -z "${formatted_files}"`,
		"          # go-format-check:end",
	}, "\n")
	if got := strings.TrimSuffix(step, "\n"); got != want {
		t.Fatalf("workflow %s formatting step does not match strict allowlist:\ngot:\n%s\nwant:\n%s", job, got, want)
	}
	return workflowMarkedScript(t, step, marker)
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

func TestReleaseWorkflowReusesCIVerdictOnlyOnProvenSuccess(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")
	verdict := workflowMarkedScript(t, content, "release-ci-verdict-reuse")
	scriptPath := filepath.Join(t.TempDir(), "reuse-ci-verdict.sh")
	script := "#!/usr/bin/env bash\nset -euo pipefail\n" +
		"GITHUB_REPOSITORY=owner/repo\n" +
		"GITHUB_SHA=0123456789abcdef0123456789abcdef01234567\n" +
		verdict +
		"printf '%s\\n' \"${ci_verified}\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write ci verdict script: %v", err)
	}

	binDir := t.TempDir()
	fakeGH := "#!/usr/bin/env bash\n" +
		"if [[ -n \"${RELEASE_TEST_GH_OUTPUT}\" ]]; then\n" +
		"  printf '%s\\n' \"${RELEASE_TEST_GH_OUTPUT}\"\n" +
		"fi\n" +
		"exit \"${RELEASE_TEST_GH_EXIT}\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(fakeGH), 0o700); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	for _, test := range []struct {
		name   string
		output string
		exit   string
		want   string
	}{
		{name: "successful runs", output: "3", exit: "0", want: "true"},
		{name: "no successful run", output: "0", exit: "0", want: "false"},
		{name: "query failed", output: "", exit: "1", want: "false"},
		{name: "non-numeric", output: "many", exit: "0", want: "false"},
		{name: "injected count", output: "2\ninjected", exit: "0", want: "false"},
		{name: "negative", output: "-1", exit: "0", want: "false"},
		{name: "empty", output: "", exit: "0", want: "false"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash", scriptPath)
			command.Env = []string{
				"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"RELEASE_TEST_GH_OUTPUT=" + test.output,
				"RELEASE_TEST_GH_EXIT=" + test.exit,
			}
			output, err := command.Output()
			if err != nil {
				t.Fatalf("ci verdict evaluation failed: %v", err)
			}
			if got := strings.TrimSpace(string(output)); got != test.want {
				t.Fatalf("ci_verified = %q, want %q", got, test.want)
			}
		})
	}
}

func TestReleaseWorkflowSkipsOnlyCIProvenGatesAndStillFailsClosed(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")

	// 只有同一 commit 上确定性重跑的 gate 才允许复用 CI 结论。
	reusable := []string{"race-tests", "race-cpa", "database-contract"}
	for _, jobName := range reusable {
		job := workflowJobBlock(t, content, jobName)
		if !strings.Contains(job, "needs.validate-tag.outputs.ci_verified != 'true'") {
			t.Fatalf("%s does not reuse the CI verdict:\n%s", jobName, job)
		}
	}
	// 静态检查依赖会随时间变化的漏洞库，每次发布都必须重跑。
	staticChecks := workflowJobBlock(t, content, "static-checks")
	if strings.Contains(staticChecks, "ci_verified") {
		t.Fatalf("static checks must not reuse a stale vulnerability audit:\n%s", staticChecks)
	}

	preflight := workflowJobBlock(t, content, "publication-preflight")
	if !strings.Contains(preflight, "!contains(needs.*.result, 'failure')") ||
		!strings.Contains(preflight, "!cancelled()") {
		t.Fatalf("publication preflight does not fail closed on gate failures:\n%s", preflight)
	}
	// preflight 的 if 只能看到直接 needs 的 result：上游失败会让中间 job 变成
	// skipped，因此每个 gate 都必须直接列出，漏掉任何一个都会让失败无法察觉。
	needsBlock := preflight[:strings.Index(preflight, "runs-on:")]
	for _, gate := range []string{
		"validate-tag",
		"verify-and-build-web",
		"static-checks",
		"race-tests",
		"race-cpa",
		"build-binaries",
		"package-checksums",
		"native-artifact-smoke",
		"docker-smoke",
		"database-contract",
	} {
		if !strings.Contains(needsBlock, "- "+gate) {
			t.Fatalf("publication preflight does not directly need gate %q:\n%s", gate, needsBlock)
		}
	}
}

func TestReleaseWorkflowJobsDownstreamOfSkippableGatesOverrideDefaultCondition(t *testing.T) {
	content := readRepositoryFile(t, ".github/workflows/release.yml")

	jobNamePattern := regexp.MustCompile(`(?m)^  ([A-Za-z][\w-]*):\n`)
	var jobNames []string
	for _, match := range jobNamePattern.FindAllStringSubmatch(content, -1) {
		jobNames = append(jobNames, match[1])
	}
	if len(jobNames) < 10 {
		t.Fatalf("found only %d jobs, job name pattern likely broken", len(jobNames))
	}

	singleNeeds := regexp.MustCompile(`(?m)^    needs: (\S+)$`)
	listNeedsItem := regexp.MustCompile(`(?m)^      - (\S+)$`)

	needsOf := make(map[string][]string, len(jobNames))
	for _, name := range jobNames {
		job := workflowJobBlock(t, content, name)
		if match := singleNeeds.FindStringSubmatch(job); match != nil {
			needsOf[name] = []string{match[1]}
			continue
		}
		needsHeader := strings.Index(job, "\n    needs:\n")
		if needsHeader < 0 {
			continue
		}
		var needs []string
		for _, line := range strings.Split(job[needsHeader:], "\n") {
			if match := listNeedsItem.FindStringSubmatch(line); match != nil {
				needs = append(needs, match[1])
				continue
			}
			if line != "" && !strings.HasPrefix(line, "      - ") && strings.TrimSpace(line) != "needs:" {
				break
			}
		}
		needsOf[name] = needs
	}

	// GitHub Actions 的默认 job 条件会沿整条依赖链传播 skip：只要某个间接祖先
	// 因 CI 结论复用等机制变成 skipped，默认条件就会让当前 job 也被跳过，即使
	// 它自己列出的直接 needs 全部成功（beta.8 的真实回归：publication-preflight
	// 成功了，但下游 publish-images/publish-github/... 仍被跳过）。凡是这条链上
	// 存在会被跳过的祖先的 job，都必须自己写显式 if，绕开默认条件的传播。
	skippable := map[string]bool{"race-tests": true, "race-cpa": true, "database-contract": true}
	var ancestors func(name string, seen map[string]bool)
	ancestors = func(name string, seen map[string]bool) {
		for _, dep := range needsOf[name] {
			if !seen[dep] {
				seen[dep] = true
				ancestors(dep, seen)
			}
		}
	}

	jobIfPattern := regexp.MustCompile(`(?m)^    if:`)
	for _, name := range jobNames {
		seen := map[string]bool{}
		ancestors(name, seen)
		hasSkippableAncestor := false
		for gate := range skippable {
			if seen[gate] {
				hasSkippableAncestor = true
				break
			}
		}
		if !hasSkippableAncestor {
			continue
		}
		job := workflowJobBlock(t, content, name)
		if !jobIfPattern.MatchString(job) {
			t.Fatalf(
				"%s has a skippable ancestor gate but no job-level if to override "+
					"the default skip-propagation condition:\n%s",
				name,
				job,
			)
		}
	}
}

func TestReleaseAssetManifestIsTheSingleSourceOfTruth(t *testing.T) {
	manifest := readRepositoryFile(t, ".github/release-assets.txt")
	var assets []string
	for _, line := range strings.Split(manifest, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			assets = append(assets, name)
		}
	}
	if len(assets) != 14 {
		t.Fatalf("release asset manifest lists %d assets, want 14", len(assets))
	}
	sorted := append([]string(nil), assets...)
	sort.Strings(sorted)
	for index := range assets {
		if assets[index] != sorted[index] {
			t.Fatalf("release asset manifest is not sorted: %v", assets)
		}
	}

	content := readRepositoryFile(t, ".github/workflows/release.yml")
	// 许可证与 SBOM 资产名只允许出现在生成它们的 package-metadata 里，
	// 清单与校验和一律从 .github/release-assets.txt 派生。
	for _, name := range []string{
		"Apache-2.0.txt",
		"Inno-Setup.txt",
		"MIT.txt",
		"MPL-2.0.txt",
		"THIRD_PARTY_NOTICES.md",
		"bom.cdx.json",
	} {
		metadataJob := workflowJobBlock(t, content, "package-metadata")
		occurrences := strings.Count(content, name)
		inMetadata := strings.Count(metadataJob, name)
		if occurrences != inMetadata {
			t.Fatalf(
				"asset %q is hardcoded outside package-metadata (%d total, %d in metadata)",
				name,
				occurrences,
				inMetadata,
			)
		}
	}
	// 资产数量与校验和行数不得再作为魔数散落在 workflow 中。
	for _, magic := range []string{`= "11"`, `= "12"`, "asset_count"} {
		if strings.Contains(content, magic) {
			t.Fatalf("release workflow still hardcodes the asset count via %q", magic)
		}
	}
}
