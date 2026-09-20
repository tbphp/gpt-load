package gateway

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestCodexModelCatalogResponseContract(t *testing.T) {
	engine := newModelListHandlerEngine(t, state.FilterSet{})
	request := httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.154.0", nil)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Models == nil {
		t.Fatalf("Codex cannot decode model directory: missing field models; body=%s", recorder.Body.String())
	}
	var slugs []string
	for _, model := range response.Models {
		for _, field := range []string{
			"slug", "display_name", "description", "supported_reasoning_levels", "shell_type",
			"visibility", "supported_in_api", "priority", "support_verbosity", "truncation_policy",
			"experimental_supported_tools", "input_modalities", "supports_reasoning_summary_parameter",
		} {
			if _, exists := model[field]; !exists {
				t.Errorf("missing Codex model field %s", field)
			}
		}
		var shellType string
		if err := json.Unmarshal(model["shell_type"], &shellType); err != nil || shellType != "unified_exec" {
			t.Fatalf("shell type must use Codex canonical value: %q, %v", shellType, err)
		}
		var truncationPolicy codexTruncationPolicy
		if err := json.Unmarshal(model["truncation_policy"], &truncationPolicy); err != nil ||
			truncationPolicy != (codexTruncationPolicy{Mode: "tokens", Limit: 10000}) {
			t.Fatalf("truncation policy = %#v, %v", truncationPolicy, err)
		}
		var slug string
		if err := json.Unmarshal(model["slug"], &slug); err != nil {
			t.Fatal(err)
		}
		slugs = append(slugs, slug)
	}
	if !reflect.DeepEqual(slugs, []string{"alpha", "beta", "zeta"}) {
		t.Fatalf("catalog changed visible models: %v", slugs)
	}
}

func TestCodexCatalogNegotiationKeepsOtherFormats(t *testing.T) {
	for _, test := range []struct {
		name   string
		target string
		header string
		field  string
	}{
		{"standard", "/v1/models", "", "data"},
		{"empty version", "/v1/models?client_version=", "", "data"},
		{"codex", "/v1/models?client_version=0.154.0", "", "models"},
		{"anthropic wins", "/v1/models?client_version=0.154.0", "2023-06-01", "data"},
		{"gemini unchanged", "/v1beta/models?client_version=0.154.0", "", "models"},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := newModelListHandlerEngine(t, state.FilterSet{Groups: map[uint]struct{}{99: {}}})
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.Header.Set("Authorization", "Bearer gl-client")
			request.Header.Set("anthropic-version", test.header)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			var response map[string]json.RawMessage
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || string(response[test.field]) != "[]" {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			if test.name == "codex" && recorder.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("access-key scoped catalog must not be shared in HTTP caches")
			}
		})
	}
}

func TestCodexCatalogUsesExistingVisibilityAndScopedMetadata(t *testing.T) {
	large, small := int64(200000), int64(64000)
	runtime := &catalog.Runtime{}
	runtime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {Models: map[string]catalog.Model{
			"upstream-large": {Metadata: catalog.ModelMetadata{Limits: catalog.ModelLimits{Context: &large}, Modalities: catalog.ModelModalities{Input: []string{"text", "image"}}}},
			"upstream-small": {Metadata: catalog.ModelMetadata{Limits: catalog.ModelLimits{Context: &small}, Modalities: catalog.ModelModalities{Input: []string{"text"}}}},
		}},
	}})
	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ID: 1, Name: "large", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: true,
				Models: []state.ModelConfig{{ID: "upstream-large", Alias: "客户端/模型"}}},
			{ID: 2, Name: "small", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: true,
				Models: []state.ModelConfig{{ID: "upstream-small", Alias: "客户端/模型"}, {ID: "private"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ClientModelOverrides = map[string]catalog.ClientModelOverrides{}
	key := state.AccessKeyView{Filters: state.FilterSet{Groups: map[uint]struct{}{1: {}}}}
	for _, test := range []struct {
		name string
		key  state.AccessKeyView
		want int64
	}{
		{"one group", key, large},
		{"intersection", state.AccessKeyView{Filters: state.FilterSet{Models: map[string]struct{}{"客户端/模型": {}}}}, small},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := buildCodexModelList(snapshot, runtime, test.key, math.MaxInt64)
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				Models []codexModel `json:"models"`
			}
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Models) != 1 || response.Models[0].Slug != "客户端/模型" ||
				response.Models[0].ContextWindow == nil || *response.Models[0].ContextWindow != test.want {
				t.Fatalf("catalog = %s", body)
			}
			if strings.Contains(string(body), "upstream-") || strings.Contains(string(body), "private") {
				t.Fatalf("catalog exposed upstream identity or denied model: %s", body)
			}
			standard := visibleModelIDs(snapshot, test.key, protocol.OpenAICompletions)
			if !reflect.DeepEqual(standard, []string{response.Models[0].Slug}) {
				t.Fatalf("visibility drift: %v", standard)
			}
		})
	}
	var overrides catalog.ClientModelOverrides
	if err := json.Unmarshal([]byte(`{"display_name":"Friendly","context_window":32000,"supported_reasoning_levels":["low","high"],"default_reasoning_level":"low","input_modalities":["text"],"supports_reasoning_summary":true}`), &overrides); err != nil {
		t.Fatal(err)
	}
	snapshot.ClientModelOverrides["客户端/模型"] = overrides
	body, err := buildCodexModelList(snapshot, runtime, key, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Models []codexModel `json:"models"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	model := response.Models[0]
	if model.DisplayName != "Friendly" || *model.ContextWindow != 32000 || model.DefaultReasoningLevel == nil || *model.DefaultReasoningLevel != "low" ||
		len(model.SupportedReasoningLevels) != 2 || model.SupportedReasoningLevels[1].Effort != "high" || !model.SupportsReasoningSummaryParameter {
		t.Fatalf("overrides absent from wire catalog: %s", body)
	}
	if bounded, err := buildCodexModelList(snapshot, runtime, key, int64(len(body))); err != nil || string(bounded) != string(body) {
		t.Fatalf("exact byte boundary failed: %v", err)
	}
	if partial, err := buildCodexModelList(snapshot, runtime, key, int64(len(body)-1)); !errors.Is(err, errModelListTooLarge) || partial != nil {
		t.Fatalf("overflow leaked partial JSON: %s, %v", partial, err)
	}
}

func TestCodexCatalogEmptyAndUnknownValues(t *testing.T) {
	for _, limit := range []int64{-1, 0, 12} {
		if body, err := buildCodexModelList(nil, nil, state.AccessKeyView{}, limit); !errors.Is(err, errModelListTooLarge) || body != nil {
			t.Fatalf("limit %d = %s, %v", limit, body, err)
		}
	}
	body, err := buildCodexModelList(nil, nil, state.AccessKeyView{}, 13)
	if err != nil || string(body) != `{"models":[]}` {
		t.Fatalf("empty catalog = %s, %v", body, err)
	}
	model := newCodexModel("unknown", 0, catalog.DefaultClientModelProfile("unknown"))
	encoded, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "context_window\":") || model.DefaultReasoningLevel != nil || len(model.SupportedReasoningLevels) != 0 || model.SupportsReasoningSummaryParameter {
		t.Fatalf("unknown capabilities fabricated: %s", encoded)
	}
}

func TestCodexCatalogRequiresAccessKeyAndBoundsResponse(t *testing.T) {
	for _, authorized := range []bool{false, true} {
		engine := newModelListHandlerEngineWithLimit(t, state.FilterSet{}, 512)
		request := httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.154.0", nil)
		if authorized {
			request.Header.Set("Authorization", "Bearer gl-client")
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusOK || strings.Contains(recorder.Body.String(), `"slug"`) {
			t.Fatalf("expected bounded local failure: %d %s", recorder.Code, recorder.Body.String())
		}
	}
}
