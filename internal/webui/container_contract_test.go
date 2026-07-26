package webui

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerfileFinalStageDeclaresNonRootPersistentRuntime(t *testing.T) {
	content := readRepositoryFile(t, "Dockerfile")
	finalStageIndex := strings.LastIndex(content, "\nFROM ")
	if finalStageIndex < 0 {
		t.Fatal("Dockerfile does not contain a final stage")
	}
	finalStage := content[finalStageIndex:]

	for _, required := range []string{
		"10001",
		"/app/data",
		"ENV DATA_DIR=/app/data",
		"USER 10001:10001",
		`ENTRYPOINT ["/app/gpt-load"]`,
	} {
		if !strings.Contains(finalStage, required) {
			t.Fatalf("Dockerfile final stage does not contain %q", required)
		}
	}
	if strings.Index(finalStage, "USER 10001:10001") >= strings.Index(finalStage, `ENTRYPOINT ["/app/gpt-load"]`) {
		t.Fatal("Dockerfile final stage does not switch users before ENTRYPOINT")
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
