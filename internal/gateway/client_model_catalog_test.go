package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestClientModelCatalogDefaultVisibilityAndVersionOrder(t *testing.T) {
	snapshot := clientModelCatalogSnapshot(
		"gpt-5.9", "gpt-6", "gpt-6.2-sol", "gpt-6.10-sol", "gpt-10-sol",
		"gpt-new-model", "gpt-image-2", "claude-sonnet", "codex-auto-review",
	)
	got := collectCodexVisibleModelIDs(snapshot, state.AccessKeyView{})
	want := []string{"gpt-10-sol", "gpt-6.10-sol", "gpt-6.2-sol", "gpt-6", "gpt-5.9", "gpt-new-model"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default client catalog = %v, want %v", got, want)
	}
	// 目录隐藏不改变普通模型列表或授权路由索引。
	if got := visibleModelIDs(snapshot, state.AccessKeyView{}, protocol.OpenAICompletions); len(got) != 9 {
		t.Fatalf("ordinary model visibility changed: %v", got)
	}
}

func TestClientModelCatalogManualSelectionOrderAndAccessFilters(t *testing.T) {
	snapshot := clientModelCatalogSnapshot("gpt-8", "gpt-6", "gpt-5", "gpt-image-2", "claude-sonnet")
	for model, raw := range map[string]string{
		"gpt-6":         `{"catalog_enabled":false}`,
		"gpt-5":         `{"catalog_order":3}`,
		"gpt-image-2":   `{"catalog_enabled":true,"catalog_order":1}`,
		"claude-sonnet": `{"catalog_enabled":true,"catalog_order":0}`,
	} {
		var overrides catalog.ClientModelOverrides
		if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
			t.Fatal(err)
		}
		snapshot.ClientModelOverrides[model] = overrides
	}
	got := collectCodexVisibleModelIDs(snapshot, state.AccessKeyView{})
	want := []string{"claude-sonnet", "gpt-image-2", "gpt-5", "gpt-8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manual client catalog = %v, want %v", got, want)
	}
	key := state.AccessKeyView{Filters: state.FilterSet{Models: map[string]struct{}{"gpt-image-2": {}}}}
	if got := collectCodexVisibleModelIDs(snapshot, key); !reflect.DeepEqual(got, []string{"gpt-image-2"}) {
		t.Fatalf("model access filter ignored: %v", got)
	}
	key.Filters.Groups = map[uint]struct{}{2: {}}
	if got := collectCodexVisibleModelIDs(snapshot, key); len(got) != 0 {
		t.Fatalf("catalog enabled a denied group: %v", got)
	}
}

func TestClientModelCatalogCurrentClientsOmitDuplicateInstructions(t *testing.T) {
	for _, version := range []string{"0.154.0", "0.159.2", "0.160.0"} {
		t.Run(version, func(t *testing.T) {
			engine := newModelListHandlerEngine(t, state.FilterSet{})
			request := httptest.NewRequest(http.MethodGet, "/v1/models?client_version="+version, nil)
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			var response struct {
				Models []map[string]json.RawMessage `json:"models"`
			}
			if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &response) != nil || len(response.Models) != 3 {
				t.Fatalf("model catalog response = %d, models=%d", recorder.Code, len(response.Models))
			}
			for _, model := range response.Models {
				_, hasLegacy := model["base_instructions"]
				if hasLegacy != (version == "0.154.0") {
					t.Fatalf("legacy instructions present=%v for client %s", hasLegacy, version)
				}
				var messages struct {
					InstructionsTemplate string `json:"instructions_template"`
				}
				if err := json.Unmarshal(model["model_messages"], &messages); err != nil || messages.InstructionsTemplate == "" {
					t.Fatalf("canonical model instructions missing: %v", err)
				}
			}
		})
	}
}

func TestClientModelCatalogCurrentResponseFitsClientLimit(t *testing.T) {
	handler, manager, _ := newHandlerForTest(t, &scriptedForwarder{})
	models := make([]state.ModelConfig, 30)
	for index := range models {
		models[index] = state.ModelConfig{ID: fmt.Sprintf("gpt-test-%02d", index)}
	}
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 1, Name: "catalog", ChannelID: channel.OpenAI, ConnectionType: "api_key",
			Params: json.RawMessage(`{}`), Models: models, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.159.2", nil)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.Len() > 1<<20 {
		t.Fatalf("client catalog = HTTP %d, %d bytes; Codex accepts at most 1048576 bytes", recorder.Code, recorder.Body.Len())
	}
	var response struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.Models) != len(models) {
		t.Fatalf("catalog dropped enabled models: count=%d, error=%v", len(response.Models), err)
	}
}

func TestClientModelCatalogHiddenModelRemainsCallable(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"id":"cmpl-test","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`),
	}}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "test-upstream-key")
	enabled := false
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 1, Name: "hidden", ChannelID: channel.OpenAI, ConnectionType: "api_key",
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{{
			ID: 1, GroupID: 1, Status: state.CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1",
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
		ClientModelOverrides: map[string]catalog.ClientModelOverrides{"gpt-4o": {CatalogEnabled: &enabled}},
	}); err != nil {
		t.Fatal(err)
	}
	if ids := collectCodexVisibleModelIDs(manager.Current(), state.AccessKeyView{}); len(ids) != 0 {
		t.Fatalf("hidden model is still in the client catalog: %v", ids)
	}
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 1 {
		t.Fatalf("hidden model call = HTTP %d, upstream attempts=%d", recorder.Code, len(forwarder.inputs))
	}
}

func clientModelCatalogSnapshot(ids ...string) *state.ConfigSnapshot {
	byModel := make(map[string][]state.RouteTarget, len(ids))
	for _, id := range ids {
		byModel[id] = []state.RouteTarget{{GroupID: 1}}
	}
	return &state.ConfigSnapshot{
		ExecutionCandidates: state.ExecutionCandidateIndex{
			protocol.OpenAIResponses: {execution.OperationResponsesCreate: byModel},
		},
		ClientModelOverrides: map[string]catalog.ClientModelOverrides{},
	}
}
