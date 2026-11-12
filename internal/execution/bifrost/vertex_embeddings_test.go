package bifrost

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
)

func vertexEmbeddingsSpec(clientProtocol protocol.Protocol, operation execution.Operation, action, body string) execution.AttemptSpec {
	path := "/v1beta/models/public-embedding:" + action
	if clientProtocol == protocol.OpenAIEmbeddings {
		path = "/v1/embeddings"
	}
	spec := execution.AttemptSpec{
		RequestID: "vertex-embeddings-request", AttemptID: "vertex-embeddings-attempt", Sequence: 1,
		ChannelID: string(channel.GoogleVertex), ClientProtocol: clientProtocol, Operation: operation,
		RouteRequirement: execution.RouteRequirementAny,
		ClientModel:      "public-embedding", UpstreamModel: "gemini-embedding-2",
		Method: http.MethodPost, Path: path, Body: []byte(body),
		RawQuery:     "trace=kept&alt=sse&key=client-query",
		Header:       http.Header{"Content-Type": {"application/json"}, "Authorization": {"Bearer client"}},
		TargetConfig: json.RawMessage(`{"location":"us-central1"}`),
		Credential:   execution.NewCredentialSnapshot(9, 1, 1, []byte(`{"service_account_json":"{\"type\":\"service_account\",\"project_id\":\"test-project\",\"client_email\":\"vertex-test@example.invalid\",\"private_key\":\"synthetic\"}"}`)),
	}
	if operation == execution.OperationProbe {
		spec.Method, spec.Path, spec.RawQuery = "", "", ""
		spec.Body, spec.Header = nil, nil
	}
	return freezeTestAttempt(spec)
}

func TestVertexPreparesNativeAndConvertedEmbeddings(t *testing.T) {
	t.Parallel()
	runtime := newTestRuntime(t)
	for _, test := range []struct {
		name      string
		protocol  protocol.Protocol
		operation execution.Operation
		action    string
		body      string
		wantBody  string
		model     string
	}{
		{
			name: "native single", protocol: protocol.GeminiEmbeddings, operation: execution.OperationEmbeddingsCreate,
			action:   "embedContent",
			body:     `{"model":"models/client-model","content":{"parts":[{"text":"hello"}]},"outputDimensionality":256,"api_key":"client"}`,
			wantBody: `{"model":"","content":{"parts":[{"text":"hello"}]},"outputDimensionality":256}`,
		},
		{
			name: "native batch", protocol: protocol.GeminiEmbeddings, operation: execution.OperationEmbeddingsCreate,
			action:   "batchEmbedContents",
			body:     `{"requests":[{"model":"models/other-model","content":{"parts":[{"text":"a"}]}},{"content":{"parts":[{"text":"b"}]},"outputDimensionality":256}],"fallbacks":["other"]}`,
			wantBody: `{"requests":[{"model":"","content":{"parts":[{"text":"a"}]}},{"model":"","content":{"parts":[{"text":"b"}]},"outputDimensionality":256}]}`,
		},
		{
			name: "native publisher model and multimodal content", protocol: protocol.GeminiEmbeddings, operation: execution.OperationEmbeddingsCreate,
			action: "embedContent", model: "publishers/google/models/gemini-embedding-2",
			body:     `{"content":{"parts":[{"text":"hello"},{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]},"requests":[null],"extension":{"model":"business-value"}}`,
			wantBody: `{"content":{"parts":[{"text":"hello"},{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]},"requests":[null],"extension":{"model":"business-value"}}`,
		},
		{
			name: "converted single", protocol: protocol.OpenAIEmbeddings, operation: execution.OperationEmbeddingsCreate,
			action:   "batchEmbedContents",
			body:     `{"model":"public-embedding","input":"hello","dimensions":256}`,
			wantBody: `{"requests":[{"model":"","content":{"parts":[{"text":"hello"}]},"outputDimensionality":256}]}`,
		},
		{
			name: "converted batch base64", protocol: protocol.OpenAIEmbeddings, operation: execution.OperationEmbeddingsCreate,
			action:   "batchEmbedContents",
			body:     `{"model":"public-embedding","input":["a","b"],"encoding_format":"base64","dimensions":256}`,
			wantBody: `{"requests":[{"model":"","content":{"parts":[{"text":"a"}]},"outputDimensionality":256},{"model":"","content":{"parts":[{"text":"b"}]},"outputDimensionality":256}]}`,
		},
		{
			name: "native probe", protocol: protocol.GeminiEmbeddings, operation: execution.OperationProbe,
			action: "embedContent", body: `{}`,
			wantBody: `{"content":{"parts":[{"text":"ping"}]}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := vertexEmbeddingsSpec(test.protocol, test.operation, test.action, test.body)
			if test.model != "" {
				spec.UpstreamModel = test.model
				spec = freezeTestAttempt(spec)
			}
			prepared, failure := runtime.prepare(spec, false)
			if failure != nil {
				t.Fatalf("prepare failed: %+v", failure.Error)
			}
			if prepared.passthrough == nil || prepared.upstreamProtocol != protocol.GeminiEmbeddings {
				t.Fatalf("expected Gemini embeddings passthrough, got protocol %q", prepared.upstreamProtocol)
			}
			request := prepared.passthrough
			if request.Path != "/publishers/google/models/gemini-embedding-2:"+test.action || request.Method != http.MethodPost {
				t.Fatalf("target = %s %s", request.Method, request.Path)
			}
			wantQuery := "trace=kept"
			if test.operation == execution.OperationProbe {
				wantQuery = ""
			}
			if request.RawQuery != wantQuery || request.SafeHeaders["Content-Type"] != "application/json" || request.SafeHeaders["Authorization"] != "" {
				t.Fatalf("query/headers = %q / %v", request.RawQuery, request.SafeHeaders)
			}
			var got, want any
			if err := json.Unmarshal(request.Body, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.wantBody), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("body = %s, want %s", request.Body, test.wantBody)
			}
			key := prepared.directKey.VertexKeyConfig
			if key == nil || key.ProjectID.GetValue() != "test-project" || key.Region.GetValue() != "us-central1" {
				t.Fatal("selected service account project or location was lost")
			}
			if test.protocol == protocol.OpenAIEmbeddings {
				conversion := prepared.embeddingConversion
				if conversion == nil || conversion.UpstreamModel != spec.UpstreamModel ||
					conversion.Base64 != (test.name == "converted batch base64") {
					t.Fatalf("conversion = %+v", conversion)
				}
			}
		})
	}
}

func TestVertexEmbeddingsUnsupportedTargetsAllowAnotherCandidate(t *testing.T) {
	t.Parallel()
	runtime := newTestRuntime(t)
	for _, model := range []string{"text-embedding-005", "claude-3-5-sonnet@20240620", "123456789", "google/123456789"} {
		for _, test := range []struct {
			name      string
			protocol  protocol.Protocol
			operation execution.Operation
			action    string
			body      string
		}{
			{"native single", protocol.GeminiEmbeddings, execution.OperationEmbeddingsCreate, "embedContent", `{"content":{"parts":[{"text":"hello"}]}}`},
			{"native batch", protocol.GeminiEmbeddings, execution.OperationEmbeddingsCreate, "batchEmbedContents", `{"requests":[{"content":{"parts":[{"text":"hello"}]}}]}`},
			{"converted", protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate, "", `{"model":"public-embedding","input":"hello"}`},
			{"probe", protocol.GeminiEmbeddings, execution.OperationProbe, "", ""},
		} {
			t.Run(model+"/"+test.name, func(t *testing.T) {
				spec := vertexEmbeddingsSpec(test.protocol, test.operation, test.action, test.body)
				spec.UpstreamModel = model
				spec = execution.NewAttemptSpec(spec)
				_, failure := runtime.prepare(spec, false)
				if failure == nil || failure.Error == nil {
					t.Fatal("unsupported target was prepared for dispatch")
				}
				decision := health.JudgeExecution(health.ExecutionAttempt{
					DispatchState: failure.DispatchState, Evidence: failure.Error,
				}, health.DecisionContext{Operation: test.operation})
				if failure.DispatchState != execution.DispatchNotSent ||
					failure.Error.Kind != execution.ErrorKindConversionUnsupported ||
					decision.Retry != health.RetryNextCandidate || decision.Effect != health.EffectSkipGroup {
					t.Fatalf("unsupported target result = %+v; decision = %+v", failure, decision)
				}
			})
		}
	}
}

func TestVertexNumericEndpointRemainsAvailableForConversation(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"generateContent", "streamGenerateContent"} {
		path, err := vertexNativeGeminiPath("123456789", action)
		if err != nil || path != "/endpoints/123456789:"+action {
			t.Fatalf("%s path = %q, %v", action, path, err)
		}
	}
	for _, action := range []string{"embedContent", "batchEmbedContents"} {
		if path, err := vertexNativeGeminiPath("123456789", action); err == nil {
			t.Fatalf("%s accepted numeric endpoint path %q", action, path)
		}
	}
}
