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

	"gpt-load/internal/automodel"
	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
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
			"base_instructions", "model_messages",
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
		var baseInstructions string
		if err := json.Unmarshal(model["base_instructions"], &baseInstructions); err != nil || baseInstructions == "" {
			t.Fatalf("base instructions = %q, %v", baseInstructions, err)
		}
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
	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ID: 1, Name: "large", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: true,
				Models: []state.ModelConfig{{ID: "upstream-large", Alias: "gpt-5.6-sol"}}},
			{ID: 2, Name: "small", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: true,
				Models: []state.ModelConfig{{ID: "upstream-small", Alias: "gpt-5.6-sol"}, {ID: "private"}}},
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
	}{
		{"one group", key},
		{"all groups", state.AccessKeyView{Filters: state.FilterSet{Models: map[string]struct{}{"gpt-5.6-sol": {}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := buildCodexModelList(snapshot, test.key, math.MaxInt64)
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				Models []struct {
					Slug                     string   `json:"slug"`
					DisplayName              string   `json:"display_name"`
					ContextWindow            int64    `json:"context_window"`
					InputModalities          []string `json:"input_modalities"`
					BaseInstructions         string   `json:"base_instructions"`
					SupportedReasoningLevels []struct {
						Effort string `json:"effort"`
					} `json:"supported_reasoning_levels"`
				} `json:"models"`
			}
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Models) != 1 || response.Models[0].Slug != "gpt-5.6-sol" ||
				response.Models[0].DisplayName != "GPT-5.6-Sol" || response.Models[0].ContextWindow != 272000 ||
				response.Models[0].BaseInstructions == "" {
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
	if err := json.Unmarshal([]byte(`{"display_name":"Friendly","context_window":32000,"supported_reasoning_levels":["low","high"],"input_modalities":["text"]}`), &overrides); err != nil {
		t.Fatal(err)
	}
	snapshot.ClientModelOverrides["gpt-5.6-sol"] = overrides
	body, err := buildCodexModelList(snapshot, key, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Models []struct {
			DisplayName              string   `json:"display_name"`
			ContextWindow            int64    `json:"context_window"`
			InputModalities          []string `json:"input_modalities"`
			SupportedReasoningLevels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	model := response.Models[0]
	if model.DisplayName != "Friendly" || model.ContextWindow != 32000 ||
		len(model.SupportedReasoningLevels) != 2 || model.SupportedReasoningLevels[1].Effort != "high" ||
		!reflect.DeepEqual(model.InputModalities, []string{"text"}) {
		t.Fatalf("overrides absent from wire catalog: %s", body)
	}
	if bounded, err := buildCodexModelList(snapshot, key, int64(len(body))); err != nil || string(bounded) != string(body) {
		t.Fatalf("exact byte boundary failed: %v", err)
	}
	if partial, err := buildCodexModelList(snapshot, key, int64(len(body)-1)); !errors.Is(err, errModelListTooLarge) || partial != nil {
		t.Fatalf("overflow leaked partial JSON: %s, %v", partial, err)
	}
}

func TestCodexCatalogEmptyAndUnknownValues(t *testing.T) {
	for _, limit := range []int64{-1, 0, 12} {
		if body, err := buildCodexModelList(nil, state.AccessKeyView{}, limit); !errors.Is(err, errModelListTooLarge) || body != nil {
			t.Fatalf("limit %d = %s, %v", limit, body, err)
		}
	}
	body, err := buildCodexModelList(nil, state.AccessKeyView{}, 13)
	if err != nil || string(body) != `{"models":[]}` {
		t.Fatalf("empty catalog = %s, %v", body, err)
	}
}

func TestCodexCatalogOnlyListsResponsesCreateModelsAndUsesGPT55ForAutoModels(t *testing.T) {
	autoModels, err := automodel.Compile(automodel.Config{
		Enabled: true, Model: "decision-model", TimeoutSeconds: 2,
		Models: []automodel.Entry{{
			ID: "auto-id", Name: "gpt-5.4", Fallback: "balanced",
			Presets: []automodel.Preset{{
				ID: "balanced", Name: "Balanced", Description: "General tasks",
				Model: "responses-model", ParameterOverrides: json.RawMessage(`[]`),
			}},
		}},
	}, map[string]struct{}{"responses-model": {}}, map[string]struct{}{"decision-model": {}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &state.ConfigSnapshot{
		ExecutionCandidates: state.ExecutionCandidateIndex{
			protocol.OpenAIResponses: {
				execution.OperationResponsesCreate: {
					"responses-model": {{GroupID: 1, Mode: channel.RouteNative}},
				},
				execution.OperationResponsesInputTokens: {
					"responses-input-only": {{GroupID: 1}},
				},
			},
			protocol.OpenAIEmbeddings: {
				execution.OperationEmbeddingsCreate: {
					"embedding-only": {{GroupID: 2}},
				},
			},
		},
		GroupCatalog: map[uint]state.GroupCatalogView{1: {ID: 1, Enabled: true}, 2: {ID: 2, Enabled: true}},
		AutoModels:   autoModels,
	}
	body, err := buildCodexModelList(snapshot, state.AccessKeyView{}, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Models []struct {
			Slug                     string   `json:"slug"`
			DisplayName              string   `json:"display_name"`
			Description              string   `json:"description"`
			Visibility               string   `json:"visibility"`
			ContextWindow            int64    `json:"context_window"`
			InputModalities          []string `json:"input_modalities"`
			SupportedReasoningLevels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Models) != 2 || response.Models[0].Slug != "gpt-5.4" || response.Models[1].Slug != "responses-model" {
		t.Fatalf("catalog = %s", body)
	}
	auto := response.Models[0]
	if auto.DisplayName != "gpt-5.4" || auto.Description != "gpt-5.4" || auto.Visibility != "list" ||
		auto.ContextWindow != 272000 || !reflect.DeepEqual(auto.InputModalities, []string{"text", "image"}) ||
		len(auto.SupportedReasoningLevels) != 4 || auto.SupportedReasoningLevels[0].Effort != "low" ||
		auto.SupportedReasoningLevels[3].Effort != "xhigh" {
		t.Fatalf("automatic model did not inherit gpt-5.5: %#v", auto)
	}
	body, err = buildCodexModelList(snapshot, state.AccessKeyView{Filters: state.FilterSet{
		Protocols: map[protocol.Protocol]struct{}{protocol.OpenAIEmbeddings: {}},
	}}, math.MaxInt64)
	if err != nil || string(body) != `{"models":[]}` {
		t.Fatalf("protocol-filtered catalog = %s, %v", body, err)
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
