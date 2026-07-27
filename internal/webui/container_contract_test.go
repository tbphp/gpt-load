package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeShellPortOverridesDotEnvEverywhere(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectDir, "docker-compose.yml"),
		[]byte(readRepositoryFile(t, "docker-compose.yml")),
		0o600,
	); err != nil {
		t.Fatalf("write temporary Compose file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".env"), []byte("PORT=3001\n"), 0o600); err != nil {
		t.Fatalf("write temporary .env: %v", err)
	}

	command := exec.Command(
		"docker", "compose", "config", "--no-env-resolution", "--format", "json",
	)
	command.Dir = projectDir
	command.Env = append(os.Environ(), "PORT=41234")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, output)
	}

	var resolved struct {
		Services map[string]struct {
			Environment map[string]string `json:"environment"`
			Healthcheck struct {
				Test []string `json:"test"`
			} `json:"healthcheck"`
			Ports []struct {
				Target    int    `json:"target"`
				Published string `json:"published"`
			} `json:"ports"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &resolved); err != nil {
		t.Fatalf("decode docker compose config: %v\n%s", err, output)
	}

	service := resolved.Services["gpt-load"]
	if service.Environment["PORT"] != "41234" {
		t.Fatalf("resolved application PORT = %q, want shell override 41234", service.Environment["PORT"])
	}
	if len(service.Ports) != 1 ||
		service.Ports[0].Target != 41234 ||
		service.Ports[0].Published != "41234" {
		t.Fatalf("resolved ports = %#v, want target/published 41234", service.Ports)
	}
	if len(service.Healthcheck.Test) != 2 ||
		!strings.Contains(service.Healthcheck.Test[1], "localhost:41234/health") {
		t.Fatalf("resolved healthcheck = %#v, want container PORT 41234", service.Healthcheck.Test)
	}
}

func TestDockerfileFinalStageDeclaresNonRootPersistentRuntime(t *testing.T) {
	content := readRepositoryFile(t, "Dockerfile")
	finalStageIndex := strings.LastIndex(content, "\nFROM ")
	if finalStageIndex < 0 {
		t.Fatal("Dockerfile does not contain a final stage")
	}
	finalStage := content[finalStageIndex:]

	orderedBeforeUser := []string{
		"addgroup -S -g 10001 gpt-load",
		"adduser -S -D -H -u 10001 -G gpt-load gpt-load",
		"mkdir -p /app/data",
		"chown 10001:10001 /app/data",
		"chmod 0700 /app/data",
		"ENV DATA_DIR=/app/data",
		"USER 10001:10001",
	}
	previousIndex := -1
	for _, required := range orderedBeforeUser {
		index := strings.Index(finalStage, required)
		if index < 0 {
			t.Fatalf("Dockerfile final stage does not contain %q", required)
		}
		if index <= previousIndex {
			t.Fatalf("Dockerfile final stage places %q out of order", required)
		}
		previousIndex = index
	}

	linesByMutation := map[string][]string{
		"USER":  {},
		"chown": {},
		"chmod": {},
	}
	for _, line := range strings.Split(finalStage, "\n") {
		trimmed := strings.TrimSpace(line)
		fields := strings.Fields(trimmed)
		if len(fields) > 0 && strings.EqualFold(fields[0], "USER") {
			linesByMutation["USER"] = append(linesByMutation["USER"], trimmed)
		}

		command := strings.TrimSpace(strings.TrimSuffix(trimmed, `\`))
		command = strings.TrimSpace(strings.TrimPrefix(command, "RUN "))
		command = strings.TrimSpace(strings.TrimPrefix(command, "&& "))
		for _, mutation := range []string{"chown", "chmod"} {
			if strings.Contains(command, mutation+" ") {
				linesByMutation[mutation] = append(linesByMutation[mutation], command)
			}
		}
	}
	for _, expectation := range []struct {
		label string
		line  string
	}{
		{label: "USER", line: "USER 10001:10001"},
		{label: "chown", line: "chown 10001:10001 /app/data"},
		{label: "chmod", line: "chmod 0700 /app/data"},
	} {
		lines := linesByMutation[expectation.label]
		if len(lines) != 1 || lines[0] != expectation.line {
			t.Fatalf(
				"Dockerfile final stage %s mutations = %q, want exactly [%q]",
				expectation.label,
				lines,
				expectation.line,
			)
		}
	}

	entrypoint := `ENTRYPOINT ["/app/gpt-load"]`
	entrypointIndex := strings.Index(finalStage, entrypoint)
	if entrypointIndex < 0 {
		t.Fatalf("Dockerfile final stage does not contain %q", entrypoint)
	}
	if previousIndex >= entrypointIndex {
		t.Fatal("Dockerfile final stage does not switch users before the direct ENTRYPOINT")
	}
	if strings.Count(finalStage, "ENTRYPOINT") != 1 {
		t.Fatal("Dockerfile final stage must declare exactly one direct ENTRYPOINT")
	}

	afterUser := finalStage[strings.Index(finalStage, "USER 10001:10001")+len("USER 10001:10001"):]
	for _, forbidden := range []string{"USER root", "chown"} {
		if strings.Contains(afterUser, forbidden) {
			t.Fatalf("Dockerfile final stage contains %q after the non-root USER", forbidden)
		}
	}
}

func TestComposeResolvesNamedVolumeContainerPathsAndStableImage(t *testing.T) {
	t.Setenv("DATA_DIR", "/host/path/must-not-reach-container")
	t.Setenv("DATABASE_DSN", "/host/database/must-not-reach-container.db")

	repositoryRoot := filepath.Join("..", "..")
	command := exec.Command(
		"docker", "compose", "config", "--no-env-resolution", "--format", "json",
	)
	command.Dir = repositoryRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, output)
	}

	var resolved struct {
		Services map[string]struct {
			Image           string            `json:"image"`
			Environment     map[string]string `json:"environment"`
			Privileged      bool              `json:"privileged"`
			StopGracePeriod string            `json:"stop_grace_period"`
			Healthcheck     map[string]any    `json:"healthcheck"`
			Volumes         []struct {
				Type   string `json:"type"`
				Source string `json:"source"`
				Target string `json:"target"`
			} `json:"volumes"`
		} `json:"services"`
		Volumes map[string]any `json:"volumes"`
	}
	if err := json.Unmarshal(output, &resolved); err != nil {
		t.Fatalf("decode docker compose config: %v\n%s", err, output)
	}

	service, ok := resolved.Services["gpt-load"]
	if !ok {
		t.Fatal("resolved Compose lacks gpt-load service")
	}
	if service.Image != "ghcr.io/tbphp/gpt-load:2" {
		t.Fatalf("resolved image = %q, want ghcr.io/tbphp/gpt-load:2", service.Image)
	}
	if service.Environment["DATA_DIR"] != "/app/data" {
		t.Fatalf("resolved DATA_DIR = %q, want /app/data", service.Environment["DATA_DIR"])
	}
	if service.Environment["DATABASE_DSN"] != "/app/data/gpt-load.db" {
		t.Fatalf(
			"resolved DATABASE_DSN = %q, want /app/data/gpt-load.db",
			service.Environment["DATABASE_DSN"],
		)
	}
	if service.Privileged {
		t.Fatal("resolved Compose enables privileged mode")
	}
	if service.StopGracePeriod != "15s" {
		t.Fatalf("resolved stop_grace_period = %q, want 15s", service.StopGracePeriod)
	}
	if len(service.Healthcheck) == 0 {
		t.Fatal("resolved Compose lacks a healthcheck")
	}
	if len(service.Volumes) != 1 {
		t.Fatalf("resolved volume count = %d, want 1", len(service.Volumes))
	}
	volume := service.Volumes[0]
	if volume.Type != "volume" || volume.Source != "gpt-load-data" || volume.Target != "/app/data" {
		t.Fatalf("resolved volume = %#v, want named gpt-load-data mounted at /app/data", volume)
	}
	if _, ok := resolved.Volumes["gpt-load-data"]; !ok {
		t.Fatal("resolved Compose lacks top-level gpt-load-data volume")
	}
	for _, volume := range service.Volumes {
		if strings.Contains(volume.Source, "docker.sock") ||
			strings.Contains(volume.Target, "docker.sock") {
			t.Fatal("resolved Compose mounts the Docker socket")
		}
	}
}
