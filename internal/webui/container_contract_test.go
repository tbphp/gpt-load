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
	t.Setenv("HOST", "")
	t.Setenv("BIND_ADDRESS", "")
	t.Setenv("OAUTH_CALLBACK_BIND_ADDRESS", "")

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
				HostIP    string `json:"host_ip"`
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
	if service.Environment["HOST"] != "0.0.0.0" {
		t.Fatalf("resolved application HOST = %q, want 0.0.0.0", service.Environment["HOST"])
	}
	if len(service.Ports) != 4 ||
		service.Ports[0].Target != 41234 ||
		service.Ports[0].Published != "41234" ||
		service.Ports[0].HostIP != "127.0.0.1" ||
		service.Ports[1].Target != 1455 ||
		service.Ports[1].Published != "1455" ||
		service.Ports[1].HostIP != "127.0.0.1" ||
		service.Ports[2].Target != 54545 ||
		service.Ports[2].Published != "54545" ||
		service.Ports[2].HostIP != "127.0.0.1" ||
		service.Ports[3].Target != 51121 ||
		service.Ports[3].Published != "51121" ||
		service.Ports[3].HostIP != "127.0.0.1" {
		t.Fatalf("resolved ports = %#v, want application 41234 and loopback OAuth callbacks 1455/54545/51121", service.Ports)
	}
	if len(service.Healthcheck.Test) != 2 ||
		!strings.Contains(service.Healthcheck.Test[1], "localhost:41234/health") {
		t.Fatalf("resolved healthcheck = %#v, want container PORT 41234", service.Healthcheck.Test)
	}
}

func TestComposeHostBindingsInheritHostAndAllowIndependentOverrides(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectDir, "docker-compose.yml"),
		[]byte(readRepositoryFile(t, "docker-compose.yml")),
		0o600,
	); err != nil {
		t.Fatalf("write temporary Compose file: %v", err)
	}

	commandEnvironment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "HOST" && key != "BIND_ADDRESS" && key != "OAUTH_CALLBACK_BIND_ADDRESS" {
			commandEnvironment = append(commandEnvironment, entry)
		}
	}

	for _, testCase := range []struct {
		name             string
		bindAddress      string
		oauthBindAddress string
		wantMainHost     string
		wantOAuthHost    string
	}{
		{
			name:          "host_default",
			wantMainHost:  "192.0.2.10",
			wantOAuthHost: "192.0.2.10",
		},
		{
			name:          "main_service_override",
			bindAddress:   "127.0.0.2",
			wantMainHost:  "127.0.0.2",
			wantOAuthHost: "192.0.2.10",
		},
		{
			name:             "oauth_callback_override",
			oauthBindAddress: "127.0.0.3",
			wantMainHost:     "192.0.2.10",
			wantOAuthHost:    "127.0.0.3",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			environmentLines := []string{"HOST=192.0.2.10"}
			if testCase.bindAddress != "" {
				environmentLines = append(environmentLines, "BIND_ADDRESS="+testCase.bindAddress)
			}
			if testCase.oauthBindAddress != "" {
				environmentLines = append(
					environmentLines,
					"OAUTH_CALLBACK_BIND_ADDRESS="+testCase.oauthBindAddress,
				)
			}
			if err := os.WriteFile(
				filepath.Join(projectDir, ".env"),
				[]byte(strings.Join(environmentLines, "\n")+"\n"),
				0o600,
			); err != nil {
				t.Fatalf("write temporary .env: %v", err)
			}

			command := exec.Command(
				"docker", "compose", "config", "--no-env-resolution", "--format", "json",
			)
			command.Dir = projectDir
			command.Env = commandEnvironment
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("docker compose config: %v\n%s", err, output)
			}

			var resolved struct {
				Services map[string]struct {
					Environment map[string]string `json:"environment"`
					Ports       []struct {
						HostIP string `json:"host_ip"`
					} `json:"ports"`
				} `json:"services"`
			}
			if err := json.Unmarshal(output, &resolved); err != nil {
				t.Fatalf("decode docker compose config: %v\n%s", err, output)
			}

			service := resolved.Services["gpt-load"]
			if service.Environment["HOST"] != "0.0.0.0" {
				t.Fatalf("resolved application HOST = %q, want 0.0.0.0", service.Environment["HOST"])
			}
			if len(service.Ports) != 4 {
				t.Fatalf("resolved ports = %#v, want application and three OAuth callback ports", service.Ports)
			}
			if service.Ports[0].HostIP != testCase.wantMainHost {
				t.Fatalf("main service host IP = %q, want %q", service.Ports[0].HostIP, testCase.wantMainHost)
			}
			for index, port := range service.Ports[1:] {
				if port.HostIP != testCase.wantOAuthHost {
					t.Fatalf("OAuth callback %d host IP = %q, want %q", index, port.HostIP, testCase.wantOAuthHost)
				}
			}
		})
	}
}

func TestNetworkConfigurationKeepsExampleSimpleAndDocumentsAdvancedOverrides(t *testing.T) {
	environmentExample := readRepositoryFile(t, ".env.example")
	for _, required := range []string{"HOST=127.0.0.1", "PORT=3001"} {
		if !strings.Contains(environmentExample, required) {
			t.Fatalf(".env.example does not contain %q", required)
		}
	}

	readmes := []string{"README.md", "README_CN.md", "README_JP.md"}
	for _, advanced := range []string{"BIND_ADDRESS", "OAUTH_CALLBACK_BIND_ADDRESS"} {
		if strings.Contains(environmentExample, advanced) {
			t.Fatalf(".env.example exposes advanced Compose override %s", advanced)
		}
		for _, readme := range readmes {
			if !strings.Contains(readRepositoryFile(t, readme), advanced) {
				t.Fatalf("%s does not document advanced Compose override %s", readme, advanced)
			}
		}
	}
}

func TestDockerfileFinalStageDeclaresNonRootPersistentRuntime(t *testing.T) {
	content := readRepositoryFile(t, "Dockerfile")
	// runtime 是唯一的 runtime 定义，源码自包含构建与发布用的 prebuilt 都继承它。
	// 每个可发布 stage 必须以它为基，否则某条打包路径会绕开下面全部 runtime 断言。
	runtimeIndex := strings.Index(content, "\nFROM alpine:")
	if runtimeIndex < 0 {
		t.Fatal("Dockerfile does not contain the shared runtime stage")
	}
	if !strings.Contains(content[runtimeIndex:], "AS runtime\n") {
		t.Fatal("Dockerfile runtime stage is not named runtime")
	}
	nextStageIndex := strings.Index(content[runtimeIndex+1:], "\nFROM ")
	if nextStageIndex < 0 {
		t.Fatal("Dockerfile does not derive any stage from the shared runtime stage")
	}
	finalStage := content[runtimeIndex : runtimeIndex+1+nextStageIndex]
	for _, derived := range []string{"FROM runtime AS prebuilt", "FROM runtime AS source-build"} {
		if !strings.Contains(content, derived) {
			t.Fatalf("Dockerfile does not derive a publishable stage via %q", derived)
		}
	}
	// 除 runtime 自身外，不允许出现直接以 alpine 为基的可发布 stage。
	if strings.Count(content, "\nFROM alpine:") != 1 {
		t.Fatal("Dockerfile declares a publishable stage that bypasses the shared runtime stage")
	}
	if !strings.Contains(finalStage, "EXPOSE 3001 1455 54545 51121") {
		t.Fatal("Dockerfile runtime stage does not expose the application and all fixed OAuth callback ports")
	}

	orderedBeforeUser := []string{
		"addgroup -S -g 10001 gpt-load",
		"adduser -S -D -H -u 10001 -G gpt-load gpt-load",
		"mkdir -p /app/data",
		"chown 10001:10001 /app/data",
		"chmod 0700 /app/data",
		"ENV HOST=0.0.0.0",
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

func TestDockerfileCopiesLocalCPAEmbeddedModuleBeforeGoModuleDownload(t *testing.T) {
	content := readRepositoryFile(t, "Dockerfile")
	rootModules := strings.Index(content, "COPY go.mod go.sum ./")
	bridgeModules := strings.Index(content, "COPY third_party/cpaembedded/go.mod third_party/cpaembedded/go.sum ./third_party/cpaembedded/")
	download := strings.Index(content, "RUN go mod download")
	bridgeSource := strings.Index(content, "COPY third_party/cpaembedded ./third_party/cpaembedded")
	build := strings.Index(content, "GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build")
	if rootModules < 0 || bridgeModules < 0 || download < 0 || bridgeSource < 0 || build < 0 {
		t.Fatalf("Dockerfile is missing local CPA embedded module build inputs")
	}
	if rootModules >= bridgeModules || bridgeModules >= download || download >= bridgeSource || bridgeSource >= build {
		t.Fatalf("Dockerfile local CPA module copy order is invalid")
	}
}

func TestDockerfileDistributesDeclaredThirdPartyLicenseTexts(t *testing.T) {
	content := readRepositoryFile(t, "Dockerfile")
	for _, required := range []string{
		"COPY LICENSES/Apache-2.0.txt /app/licenses/Apache-2.0.txt",
		"COPY LICENSES/MIT.txt /app/licenses/MIT.txt",
		"COPY LICENSES/MPL-2.0.txt /app/licenses/MPL-2.0.txt",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("Dockerfile does not distribute declared license text %q", required)
		}
	}
}

func TestComposeBindsLoopbackAndConfiguresContainerAllInterfaces(t *testing.T) {
	t.Setenv("HOST", "")
	t.Setenv("BIND_ADDRESS", "")
	t.Setenv("OAUTH_CALLBACK_BIND_ADDRESS", "")
	t.Setenv("PORT", "")

	projectDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectDir, "docker-compose.yml"),
		[]byte(readRepositoryFile(t, "docker-compose.yml")),
		0o600,
	); err != nil {
		t.Fatalf("write temporary Compose file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".env"), nil, 0o600); err != nil {
		t.Fatalf("write temporary .env: %v", err)
	}

	command := exec.Command(
		"docker", "compose", "config", "--no-env-resolution", "--format", "json",
	)
	command.Dir = projectDir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, output)
	}

	var resolved struct {
		Services map[string]struct {
			Environment map[string]string `json:"environment"`
			Ports       []struct {
				Target    int    `json:"target"`
				Published string `json:"published"`
				HostIP    string `json:"host_ip"`
			} `json:"ports"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &resolved); err != nil {
		t.Fatalf("decode docker compose config: %v\n%s", err, output)
	}

	service := resolved.Services["gpt-load"]
	if service.Environment["HOST"] != "0.0.0.0" {
		t.Fatalf("resolved application HOST = %q, want 0.0.0.0", service.Environment["HOST"])
	}
	if len(service.Ports) != 4 ||
		service.Ports[0].Target != 3001 ||
		service.Ports[0].Published != "3001" ||
		service.Ports[0].HostIP != "127.0.0.1" ||
		service.Ports[1].Target != 1455 ||
		service.Ports[1].Published != "1455" ||
		service.Ports[1].HostIP != "127.0.0.1" ||
		service.Ports[2].Target != 54545 ||
		service.Ports[2].Published != "54545" ||
		service.Ports[2].HostIP != "127.0.0.1" ||
		service.Ports[3].Target != 51121 ||
		service.Ports[3].Published != "51121" ||
		service.Ports[3].HostIP != "127.0.0.1" {
		t.Fatalf("resolved ports = %#v, want application 3001 and loopback OAuth callbacks 1455/54545/51121", service.Ports)
	}
}

func TestComposeProjectsHaveIndependentNamesApplicationPortsAndVolumes(t *testing.T) {
	t.Setenv("HOST", "")
	t.Setenv("BIND_ADDRESS", "")
	t.Setenv("OAUTH_CALLBACK_BIND_ADDRESS", "")
	t.Setenv("PORT", "")

	projectDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectDir, "docker-compose.yml"),
		[]byte(readRepositoryFile(t, "docker-compose.yml")),
		0o600,
	); err != nil {
		t.Fatalf("write temporary Compose file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".env"), nil, 0o600); err != nil {
		t.Fatalf("write temporary .env: %v", err)
	}

	type composeConfig struct {
		Name     string `json:"name"`
		Services map[string]struct {
			ContainerName string `json:"container_name"`
			Ports         []struct {
				Target    int    `json:"target"`
				Published string `json:"published"`
				HostIP    string `json:"host_ip"`
			} `json:"ports"`
		} `json:"services"`
		Volumes map[string]struct {
			Name string `json:"name"`
		} `json:"volumes"`
	}

	render := func(projectName, publishedPort string) composeConfig {
		t.Helper()
		command := exec.Command(
			"docker", "compose", "--project-name", projectName,
			"config", "--no-env-resolution", "--format", "json",
		)
		command.Dir = projectDir
		command.Env = append(os.Environ(), "PORT="+publishedPort)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("docker compose config for %s: %v\n%s", projectName, err, output)
		}

		var resolved composeConfig
		if err := json.Unmarshal(output, &resolved); err != nil {
			t.Fatalf("decode docker compose config for %s: %v\n%s", projectName, err, output)
		}
		return resolved
	}

	first := render("review-one", "41001")
	second := render("review-two", "41002")
	for _, item := range []struct {
		projectName   string
		publishedPort string
		targetPort    int
		config        composeConfig
	}{
		{projectName: "review-one", publishedPort: "41001", targetPort: 41001, config: first},
		{projectName: "review-two", publishedPort: "41002", targetPort: 41002, config: second},
	} {
		if item.config.Name != item.projectName {
			t.Fatalf("resolved project name = %q, want %q", item.config.Name, item.projectName)
		}
		service := item.config.Services["gpt-load"]
		if service.ContainerName != "" {
			t.Fatalf("resolved project %s fixes container_name to %q", item.projectName, service.ContainerName)
		}
		if len(service.Ports) != 4 ||
			service.Ports[0].Target != item.targetPort ||
			service.Ports[0].Published != item.publishedPort ||
			service.Ports[0].HostIP != "127.0.0.1" ||
			service.Ports[1].Target != 1455 ||
			service.Ports[1].Published != "1455" ||
			service.Ports[1].HostIP != "127.0.0.1" ||
			service.Ports[2].Target != 54545 ||
			service.Ports[2].Published != "54545" ||
			service.Ports[2].HostIP != "127.0.0.1" ||
			service.Ports[3].Target != 51121 ||
			service.Ports[3].Published != "51121" ||
			service.Ports[3].HostIP != "127.0.0.1" {
			t.Fatalf("resolved project %s ports = %#v", item.projectName, service.Ports)
		}
		wantVolume := item.projectName + "_gpt-load-data"
		if got := item.config.Volumes["gpt-load-data"].Name; got != wantVolume {
			t.Fatalf("resolved project %s volume = %q, want %q", item.projectName, got, wantVolume)
		}
	}
	if first.Volumes["gpt-load-data"].Name == second.Volumes["gpt-load-data"].Name {
		t.Fatal("different Compose projects resolve the same named volume")
	}
}

func TestComposeResolvesNamedVolumeContainerPathsAndBetaChannelImage(t *testing.T) {
	t.Setenv("DATA_DIR", "/host/path/must-not-reach-container")
	t.Setenv("DATABASE_DSN", "/host/database/must-not-reach-container.db")

	projectDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(projectDir, "docker-compose.yml"),
		[]byte(readRepositoryFile(t, "docker-compose.yml")),
		0o600,
	); err != nil {
		t.Fatalf("write temporary Compose file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".env"), nil, 0o600); err != nil {
		t.Fatalf("write temporary .env: %v", err)
	}

	command := exec.Command(
		"docker", "compose", "config", "--no-env-resolution", "--format", "json",
	)
	command.Dir = projectDir
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
	if service.Image != "ghcr.io/tbphp/gpt-load:v2beta" {
		t.Fatalf("resolved image = %q, want ghcr.io/tbphp/gpt-load:v2beta", service.Image)
	}
	if service.Environment["DATA_DIR"] != "/app/data" {
		t.Fatalf("resolved DATA_DIR = %q, want /app/data", service.Environment["DATA_DIR"])
	}
	if databaseDSN, ok := service.Environment["DATABASE_DSN"]; ok {
		t.Fatalf("resolved DATABASE_DSN = %q, want managed default to remain unset", databaseDSN)
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
