package webui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerSmokeCancellationCleansOnlyOwnedResources(t *testing.T) {
	source := readRepositoryFile(t, ".github/scripts/release-docker-smoke.sh")
	preflight, _, found := strings.Cut(source, "cat >\"${task_tmp}/fake-response.json\"")
	if !found {
		t.Fatal("missing Docker smoke work boundary")
	}
	for _, conflict := range []string{"false", "true"} {
		t.Run(conflict, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "cleanup.log")
			script := `docker() {
  if [[ "$2" == inspect ]]; then [[ "$CONFLICT" == true ]]; return; fi
  printf '%s\n' "$*" >> "$CLEANUP_LOG"
}
` + preflight + "\nkill -s TERM $$\n"
			path := filepath.Join(dir, "cancel.sh")
			if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("bash", path)
			command.Env = append(os.Environ(), "CONFLICT="+conflict, "CLEANUP_LOG="+log, "RELEASE_SMOKE_SUFFIX=review-1-2")
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			wantCode := 143
			if conflict == "true" {
				wantCode = 1
			}
			if !errors.As(err, &exit) || exit.ExitCode() != wantCode {
				t.Fatalf("cancellation exit = %v, want %d: %s", err, wantCode, output)
			}
			cleanup, err := os.ReadFile(log)
			if conflict == "true" {
				if !os.IsNotExist(err) {
					t.Fatalf("removed pre-existing Docker resources: %s, %v", cleanup, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, owned := range []string{"rm -f gpt-load-release-smoke-review-1-2", "volume rm gpt-load-release-smoke-review-1-2", "network rm gpt-load-release-network-review-1-2", "image rm gpt-load-release-smoke:review-1-2"} {
				if !strings.Contains(string(cleanup), owned) {
					t.Fatalf("cancellation did not clean %s: %s", owned, cleanup)
				}
			}
		})
	}
}

func TestReleaseDockerSmokeSeparatesRerunResources(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release.yml")
	for _, job := range []string{"docker-smoke", "prebuilt-image-smoke", "post-publish-image-smoke"} {
		block := workflowJobBlock(t, workflow, job)
		found := false
		for _, line := range strings.Split(block, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "RELEASE_SMOKE_SUFFIX:") {
				continue
			}
			found = true
			if !strings.Contains(line, "${{ github.run_id }}") || !strings.Contains(line, "${{ github.run_attempt }}") {
				t.Fatalf("%s reuses Docker resource names across attempts: %s", job, line)
			}
		}
		if !found {
			t.Fatalf("%s has no task-specific Docker resource suffix", job)
		}
	}
}

func TestDockerSmokeDiscoversLoopbackPortOnEveryContainerStart(t *testing.T) {
	source := readRepositoryFile(t, ".github/scripts/release-docker-smoke.sh")
	start := strings.Index(source, "start_container() {\n")
	if start < 0 {
		t.Fatal("missing container startup function")
	}
	end := strings.Index(source[start:], "\n}\n")
	if end < 0 {
		t.Fatal("unterminated container startup function")
	}
	var initialization string
	for _, line := range strings.Split(source[:start], "\n") {
		if strings.HasPrefix(line, "app_port=") || strings.HasPrefix(line, "base_url=") {
			initialization += line + "\n"
		}
	}
	for _, test := range []struct{ name, requested, binding, wantFirst, wantSecond string }{
		{"dynamic", "", "", "http://127.0.0.1:47001", "http://127.0.0.1:47002"},
		{"explicit", "40123", "", "http://127.0.0.1:40123", "http://127.0.0.1:40123"},
		{"unexpected binding", "", "0.0.0.0:47001", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := `set -euo pipefail
container=container
network=network
volume=volume
image=image
starts=0
docker() {
  if [[ "$1" == run ]]; then
    starts=$((starts + 1))
    [[ " $* " == *" --publish 127.0.0.1:${RELEASE_SMOKE_APP_PORT:-0}:3001 "* ]]
  elif [[ "$1" == port && "$2" == "$container" && "$3" == 3001/tcp ]]; then
    if [[ -n "$TEST_BINDING" ]]; then
      printf '%s\n' "$TEST_BINDING"
    elif [[ -n "$RELEASE_SMOKE_APP_PORT" ]]; then
      printf '127.0.0.1:%s\n' "$RELEASE_SMOKE_APP_PORT"
    else
      printf '127.0.0.1:%s\n' "$((47000 + starts))"
    fi
  else
    return 1
  fi
}
` + initialization + source[start:start+end+3] + `
start_container
[[ "$base_url" == "$TEST_FIRST_URL" ]]
start_container
[[ "$base_url" == "$TEST_SECOND_URL" ]]
`
			path := filepath.Join(t.TempDir(), "port-test.sh")
			if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("bash", path)
			command.Env = append(os.Environ(), "RELEASE_SMOKE_APP_PORT="+test.requested,
				"TEST_BINDING="+test.binding, "TEST_FIRST_URL="+test.wantFirst, "TEST_SECOND_URL="+test.wantSecond)
			output, err := command.CombinedOutput()
			if (err != nil) != (test.binding != "") {
				t.Fatalf("port discovery failed: %v: %s", err, output)
			}
		})
	}
}
