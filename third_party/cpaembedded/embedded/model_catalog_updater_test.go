package embedded

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/sirupsen/logrus"
)

// CPA 的更新器和目录均为进程单例，每个场景用独立进程，避免污染其他测试。
func TestModelCatalogUpdater(t *testing.T) {
	const childEnv = "GPT_LOAD_CPA_CATALOG_TEST"
	if scenario := os.Getenv(childEnv); scenario != "" {
		testModelCatalogUpdater(t, scenario)
		return
	}
	for _, scenario := range []string{"success", "fallback", "unavailable", "invalid", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestModelCatalogUpdater$", "-test.count=1")
			command.Env = append(os.Environ(), childEnv+"="+scenario)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("catalog scenario %s: %v\n%s", scenario, err, output)
			}
		})
	}
}

type catalogFailureHook struct{ done chan struct{} }

func (*catalogFailureHook) Levels() []logrus.Level { return logrus.AllLevels }
func (hook *catalogFailureHook) Fire(entry *logrus.Entry) error {
	if strings.Contains(entry.Message, "keeping current data") {
		select {
		case hook.done <- struct{}{}:
		default:
		}
	}
	return nil
}

func testModelCatalogUpdater(t *testing.T, scenario string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	providers := []string{ProviderClaude, ProviderCodex, ProviderAntigravity, ProviderGrok}
	before := make(map[string][]string)
	for _, provider := range providers {
		before[provider] = MergeModelCatalog(provider, nil)
	}
	ids := map[string]string{
		ProviderClaude:      "claude-haiku-5-5",
		ProviderCodex:       "codex-online-fixture",
		ProviderAntigravity: "claude-sonnet-5-5-high",
		ProviderGrok:        "grok-online-fixture",
	}
	payload := map[string]any{
		"claude":      []map[string]any{{"id": ids[ProviderClaude], "max_completion_tokens": 12345}},
		"codex-pro":   []map[string]any{{"id": ids[ProviderCodex]}},
		"antigravity": []map[string]any{{"id": ids[ProviderAntigravity]}, {"id": "gemini-2.5-pro"}},
		"xai":         []map[string]any{{"id": ids[ProviderGrok]}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	updated := make(chan struct{}, 1)
	registry.SetModelRefreshCallback(func([]string) { updated <- struct{}{} })
	failed := make(chan struct{}, 1)
	logrus.AddHook(&catalogFailureHook{done: failed})
	requests := make(chan string, 4)
	canceled := make(chan struct{}, 2)
	http.DefaultTransport = claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests <- request.URL.String()
		if request.Method != http.MethodGet || request.Header.Get("Authorization") != "" {
			t.Error("public catalog request must be an unauthenticated GET")
		}
		if scenario == "cancel" {
			<-request.Context().Done()
			canceled <- struct{}{}
			return nil, request.Context().Err()
		}
		status, body := http.StatusOK, string(raw)
		switch {
		case scenario == "unavailable", scenario == "fallback" && request.URL.Host == "raw.githubusercontent.com":
			status, body = http.StatusServiceUnavailable, "unavailable"
		case scenario == "invalid":
			body = `{"claude":[{"id":""}]}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	StartModelCatalogUpdater(ctx)
	select {
	case url := <-requests:
		if url != "https://raw.githubusercontent.com/router-for-me/models/refs/heads/main/models.json" {
			t.Fatalf("first URL = %s", url)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("updater did not request the online catalog")
	}
	if scenario == "cancel" {
		cancel()
		waitCatalogSignal(t, canceled)
		return
	}
	if scenario == "invalid" || scenario == "unavailable" || scenario == "fallback" {
		select {
		case url := <-requests:
			if url != "https://models.router-for.me/models.json" {
				t.Fatalf("fallback URL = %s", url)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("updater did not request the fallback URL")
		}
	}
	if scenario == "invalid" || scenario == "unavailable" {
		waitCatalogSignal(t, failed)
		for _, provider := range providers {
			if got := MergeModelCatalog(provider, nil); !reflect.DeepEqual(got, before[provider]) {
				t.Fatalf("failed update changed %s catalog", provider)
			}
		}
		return
	}
	waitCatalogSignal(t, updated)
	StartModelCatalogUpdater(ctx)
	for _, provider := range providers {
		if !containsModelID(MergeModelCatalog(provider, nil), ids[provider]) {
			t.Fatalf("%s is missing the online-only model", provider)
		}
		got := MergeModelCatalog(provider, []string{"upstream-only", ids[provider]})
		if !containsModelID(got, ids[provider]) || !containsModelID(got, "upstream-only") || len(got) != len(uniqueModelIDs(got)) {
			t.Fatalf("%s merged catalog = %v", provider, got)
		}
	}
	if containsModelID(MergeModelCatalog(ProviderAntigravity, nil), "gemini-2.5-pro") {
		t.Fatal("online catalog bypassed Antigravity exclusions")
	}
	if got := registry.GetClaudeModels()[0].MaxCompletionTokens; got != 12345 {
		t.Fatalf("online capability = %d", got)
	}
	// Claude 使用独立的发现入口，在线目录不能绕过账号明确禁用的模型。
	for _, denied := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if denied {
				_, _ = io.WriteString(w, `{"model_access":[{"api_name":"claude-haiku-5-5","entitled":false}]}`)
			} else {
				_, _ = io.WriteString(w, `{}`)
			}
		}))
		models, err := DiscoverClaudeModels(ctx, testClaudeExecutionCredential(), ClaudeOptions{BootstrapURL: server.URL, HTTPClient: &http.Client{Transport: &http.Transport{}}})
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, model := range models {
			if model.ID == ids[ProviderClaude] {
				found = true
			}
		}
		if found == denied {
			t.Fatalf("Claude model presence = %v, denied = %v", found, denied)
		}
	}
}

func waitCatalogSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for catalog update")
	}
}
