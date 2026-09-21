package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func decisionsSpec(id channel.ID, baseURL string) execution.AttemptSpec {
	spec := openAIEmbeddingsSpec(id, baseURL, []byte(`{"model":"public","state":{"task":"hello"},"questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"Low","high":"High"}}},"provider":{"order":["Typesafe"]},"stream":false,"api_key":"injected","fallbacks":["other"],"future":1.2300}`))
	spec.ClientProtocol = protocol.Decisions
	spec.Operation = execution.OperationDecisionsCreate
	spec.Path = "/v1/systemone"
	spec.ClientModel = "public"
	spec.UpstreamModel = "provider-model"
	spec.RawQuery = "tenant=a%2Bb&api_key=injected"
	return freezeTestAttempt(spec)
}

func TestDecisionsRuntimeUsesNativeWireAndProviderSpecificPath(t *testing.T) {
	for _, test := range []struct {
		name     string
		id       channel.ID
		prefix   string
		wantPath string
	}{
		{name: "Jev", id: channel.Jev, prefix: "/v1", wantPath: "/v1/systemone"},
		{name: "OpenRouter", id: channel.OpenRouter, prefix: "/api/v1", wantPath: "/api/alpha/decisions"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != test.wantPath || request.URL.RawQuery != "tenant=a%2Bb" {
					t.Errorf("target = %s %s", request.Method, request.URL.String())
				}
				if request.Header.Get("Authorization") != "Bearer "+testAPIKey || request.Header.Get("X-Api-Key") != "" {
					t.Error("credential headers were not replaced")
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var object map[string]json.RawMessage
				if json.Unmarshal(body, &object) != nil || string(object["model"]) != `"provider-model"` ||
					string(object["state"]) != `{"task":"hello"}` || string(object["future"]) != "1.2300" ||
					string(object["provider"]) != `{"order":["Typesafe"]}` {
					t.Errorf("body = %s", body)
				}
				for _, field := range []string{"stream", "api_key", "fallbacks"} {
					if _, exists := object[field]; exists {
						t.Errorf("control field %s retained", field)
					}
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Request-Id", "decision-request")
				_, _ = io.WriteString(writer, `{"id":"decision-1","model":"provider-model","provider":"Typesafe","answers":{"tier":{"choice":"low","confidence":0.9}},"usage":{"input_tokens":7,"output_tokens":1,"cost":0.000001},"future":1.2300}`)
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(context.Background(), decisionsSpec(test.id, server.URL+test.prefix))
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
				result.UpstreamProtocol != protocol.Decisions || result.UpstreamRequestID != "decision-request" {
				t.Fatalf("result = %+v error = %+v contract = %v", result, result.Error, err)
			}
			for _, value := range []string{`"model":"public"`, `"provider":"Typesafe"`, `"future":1.2300`} {
				if !bytes.Contains(result.Body, []byte(value)) {
					t.Fatalf("missing %s: %s", value, result.Body)
				}
			}
			if result.Usage == nil || result.Usage.Normalized.Tokens.UncachedInput != 7 || result.Usage.Normalized.Tokens.Output != 1 {
				t.Fatalf("usage = %#v", result.Usage)
			}
			if runtime.keyPoolCalls() != 0 {
				t.Fatal("SDK key pool was used")
			}
		})
	}
}

func TestJevListModelsUsesNativeTypeSafeEndpoint(t *testing.T) {
	const responseBody = `{"models":[{"name":"jev-latest","description":"Latest stable Jev","release_date":"2026-09-15"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/models" || request.URL.RawQuery != "tenant=a%2Bb" {
			t.Errorf("target = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if body, err := io.ReadAll(request.Body); err != nil || len(body) != 0 {
			t.Errorf("body = %q, err = %v", body, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-Id", "models-jev")
		_, _ = io.WriteString(writer, responseBody)
	}))
	defer server.Close()

	spec := utilitySpec(channel.Jev, protocol.Decisions, execution.OperationListModels, http.MethodGet, "/v1/models", nil)
	spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `/v1"}`)
	spec.RawQuery = "tenant=a%2Bb&api_key=injected"
	spec.Query = nil
	spec = freezeTestAttempt(spec)
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	if result.StatusCode != http.StatusOK || result.UpstreamProtocol != protocol.Decisions ||
		result.UpstreamRequestID != "models-jev" || string(result.Body) != responseBody {
		t.Fatalf("result = %+v, body = %s", result, result.Body)
	}
}

func TestDecisionsProbeUsesMinimalBodyAndValidatesAnswer(t *testing.T) {
	for _, response := range []struct {
		body  string
		valid bool
	}{
		{body: `{"answers":{"ready":{"noul":0.9}},"usage":{"input_tokens":1,"output_tokens":1}}`, valid: true},
		{body: `{"answers":{"ready":{"noul":1.1}},"usage":{"input_tokens":1,"output_tokens":1}}`, valid: false},
		{body: `{"answers":{"ready":{"noul":true}},"usage":{"input_tokens":1,"output_tokens":1}}`, valid: false},
		{body: `{"answers":{},"usage":{"input_tokens":1,"output_tokens":1}}`, valid: false},
		{body: `{"choices":[]}`, valid: false},
	} {
		t.Run(response.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var object map[string]json.RawMessage
				if json.Unmarshal(body, &object) != nil || string(object["state"]) != `"ping"` || len(object["questions"]) == 0 {
					t.Errorf("probe = %s", body)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, response.body)
			}))
			defer server.Close()
			spec := decisionsSpec(channel.Jev, server.URL+"/v1")
			spec.Operation = execution.OperationProbe
			spec.Method, spec.Path, spec.RawQuery = "", "", ""
			spec.Body = nil
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(context.Background(), freezeTestAttempt(spec))
			if err := result.Validate(); err != nil || (result.Error == nil) != response.valid {
				t.Fatalf("result = %+v error = %+v contract = %v", result, result.Error, err)
			}
		})
	}
}

func TestOpenRouterDecisionsRequiresV1BaseURL(t *testing.T) {
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	result := runtime.Execute(context.Background(), decisionsSpec(channel.OpenRouter, "https://relay.example/custom"))
	if result.Error == nil || result.DispatchState != execution.DispatchNotSent {
		t.Fatalf("result = %+v error = %+v", result, result.Error)
	}
}

func TestDecisionsTargetsUseChannelDefaults(t *testing.T) {
	registry := channel.NewRegistry()
	for _, test := range []struct {
		channelID channel.ID
		baseURL   string
		path      string
	}{
		{channelID: channel.Jev, baseURL: "https://api.typesafe.ai/v1", path: "/systemone"},
		{channelID: channel.OpenRouter, baseURL: "https://openrouter.ai/api/alpha", path: "/decisions"},
	} {
		resolved, err := registry.Resolve(test.channelID, nil)
		if err != nil {
			t.Fatal(err)
		}
		baseURL, path, err := decisionsTarget(resolved)
		if err != nil || baseURL != test.baseURL || path != test.path {
			t.Fatalf("%s target = %q %q, %v", test.channelID, baseURL, path, err)
		}
	}
}

func TestDecisionsKeepsKnownUsageOnProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"error":{"message":"rejected"},"usage":{"input_tokens":9,"output_tokens":1}}`)
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	result := runtime.Execute(context.Background(), decisionsSpec(channel.Jev, server.URL+"/v1"))
	if result.Error == nil || result.Usage == nil || result.Usage.Normalized.State != usage.StateComplete ||
		result.Usage.Normalized.Tokens.UncachedInput != 9 || result.Usage.Normalized.Tokens.Output != 1 {
		t.Fatalf("result = %+v error = %+v usage = %#v", result, result.Error, result.Usage)
	}
}
