package bifrost

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

const geminiDecisionsResponse = `{"answers":{"is_damaged":{"type":"noul","noul":0.99,"confidence":0.99}},"usage":{"total_input_tokens":100,"input_tokens_by_modality":[{"modality":"text","tokens":100}],"total_output_tokens":0,"total_tokens":100}}`

func geminiDecisionsSpec(body string) execution.AttemptSpec {
	return freezeTestAttempt(execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "gemini-decisions-request", AttemptID: "gemini-decisions-attempt", Sequence: 1,
		ChannelID: string(channel.Gemini), ClientProtocol: protocol.GeminiDecisions,
		Operation: execution.OperationDecisionsCreate, RouteMode: execution.RouteNative,
		RouteRequirement: execution.RouteRequirementNative,
		ClientModel:      "public", UpstreamModel: "gemini-decision-1",
		Method: http.MethodPost, Path: "/v1beta/decisions",
		RawQuery: "trace=kept&key=client-query",
		Header: http.Header{
			"Authorization":  {"Bearer client"},
			"X-Goog-Api-Key": {"client"},
			"Content-Type":   {"application/json"},
		},
		Body:         []byte(body),
		TargetConfig: json.RawMessage(`{}`),
		Credential:   execution.NewCredentialSnapshot(9, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	}))
}

func readGeminiDecisionsUpstream(t *testing.T, request *http.Request) map[string]json.RawMessage {
	t.Helper()
	if request.Method != http.MethodPost || request.URL.Path != "/v1beta/decisions" || request.URL.Query().Get("key") != "" {
		t.Errorf("upstream target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
	}
	if request.Header.Get("X-Goog-Api-Key") != testAPIKey || request.Header.Get("Authorization") != "" {
		t.Errorf("credential headers = %q / %q", request.Header.Get("X-Goog-Api-Key"), request.Header.Get("Authorization"))
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Errorf("read upstream body: %v", err)
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		t.Errorf("decode upstream body %s: %v", body, err)
		return nil
	}
	if string(object["model"]) != `"gemini-decision-1"` {
		t.Errorf("upstream model = %s", object["model"])
	}
	return object
}

func TestGeminiDecisionsPassthroughOnGeminiChannel(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		object := readGeminiDecisionsUpstream(t, request)
		if request.URL.Query().Get("trace") != "kept" || string(object["input"]) != `"The mug arrived cracked."` ||
			string(object["questions"]) != `{"is_damaged":{"type":"noul","instructions":"Did the item arrive damaged?"}}` {
			t.Errorf("upstream request = %s %#v", request.URL.RawQuery, object)
		}
		if _, exists := object["api_key"]; exists {
			t.Error("control field api_key retained")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, geminiDecisionsResponse)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
	result := runtime.Execute(t.Context(), geminiDecisionsSpec(
		`{"model":"public","input":"The mug arrived cracked.","questions":{"is_damaged":{"type":"noul","instructions":"Did the item arrive damaged?"}},"api_key":"injected"}`,
	))
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, error = %+v, validation = %v", result, result.Error, err)
	}
	if result.UpstreamProtocol != protocol.GeminiDecisions || string(result.Body) != geminiDecisionsResponse {
		t.Fatalf("result protocol = %q body = %s", result.UpstreamProtocol, result.Body)
	}
	assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 100})
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d", calls.Load())
	}
}

func TestGeminiDecisionsProbeUsesMinimalNoulQuestion(t *testing.T) {
	t.Parallel()

	for _, response := range []struct {
		name  string
		body  string
		valid bool
	}{
		{name: "answered", body: `{"answers":{"ready":{"type":"noul","noul":0.99,"confidence":0.99}},"usage":{"total_input_tokens":12,"total_output_tokens":0,"total_tokens":12}}`, valid: true},
		{name: "missing answer", body: `{"answers":{},"usage":{"total_input_tokens":12,"total_output_tokens":0,"total_tokens":12}}`, valid: false},
	} {
		t.Run(response.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				object := readGeminiDecisionsUpstream(t, request)
				if string(object["input"]) != `"ping"` || string(object["questions"]) != `{"ready":{"instructions":"Is the service ready?","type":"noul"}}` {
					t.Errorf("probe = %#v", object)
				}
				if _, exists := object["state"]; exists {
					t.Error("probe used the Jev state field")
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, response.body)
			}))
			defer server.Close()

			spec := geminiDecisionsSpec("")
			spec.Operation = execution.OperationProbe
			spec.Method, spec.Path, spec.RawQuery = "", "", ""
			spec.Body = nil
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
			result := runtime.Execute(t.Context(), freezeTestAttempt(spec))
			if err := result.Validate(); err != nil || (result.Error == nil) != response.valid {
				t.Fatalf("result = %+v error = %+v contract = %v", result, result.Error, err)
			}
		})
	}
}
