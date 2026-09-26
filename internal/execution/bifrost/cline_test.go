package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

const clineCompletionFixture = `{"id":"cline-id","object":"chat.completion","created":1,"model":"deepseek/deepseek-v4.1-flash","choices":[{"index":0,"message":{"role":"assistant","content":"pong","reasoning":"full reasoning","provider_metadata":{"keep":true}},"finish_reason":"stop"}],"usage":{"prompt_tokens":31,"completion_tokens":72,"total_tokens":103},"vendor_number":9007199254740993}`

func TestClineNativeCompletion(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "wrapped"}[wrapped], func(t *testing.T) {
			response := clineCompletionFixture
			if wrapped {
				response = `{"success":true,"data":` + response + `}`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/api/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer cline-test-key" {
					t.Error("wrong Cline endpoint or credential")
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var sent map[string]json.RawMessage
				if err := json.Unmarshal(body, &sent); err != nil || string(sent["stream"]) != "false" {
					t.Errorf("Cline must receive explicit stream=false: %s, %v", body, err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("ETag", `"upstream-body"`)
				_, _ = io.WriteString(w, response)
			}))
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			spec := openAIChatAttempt(t, channel.ID("cline"), server.URL+"/api/v1", "cline-test-key")
			spec.UpstreamModel = "deepseek/deepseek-v4.1-flash"
			result := runtime.Execute(t.Context(), spec)
			if result.Error != nil || result.StatusCode != http.StatusOK {
				t.Fatalf("Cline result = %+v", result)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(result.Body, &body); err != nil {
				t.Fatal(err)
			}
			if string(body["id"]) != `"cline-id"` || string(body["model"]) != `"client-model"` || body["data"] != nil {
				t.Fatalf("Cline response was not normalized: %s", result.Body)
			}
			if string(body["vendor_number"]) != "9007199254740993" {
				t.Fatalf("Cline extension changed: %s", result.Body)
			}
			assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 31, Output: 72})
			if !strings.Contains(string(result.Body), "full reasoning") {
				t.Fatalf("converted reasoning was lost: %s", result.Body)
			}
		})
	}
}

func TestClineConvertedCompletion(t *testing.T) {
	for _, value := range []protocol.Protocol{protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
		t.Run(string(value), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
				}
				if request.URL.Path != "/api/v1/chat/completions" || !strings.Contains(string(body), `"messages"`) || !strings.Contains(string(body), `"stream":false`) {
					t.Errorf("invalid converted Cline request: %s %s", request.URL.Path, body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"success":true,"data":`+clineCompletionFixture+`}`)
			}))
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			spec := clineTestSpec(t, server.URL, value)
			result := runtime.Execute(t.Context(), spec)
			if result.Error != nil || result.StatusCode != http.StatusOK || !strings.Contains(string(result.Body), "pong") || !strings.Contains(string(result.Body), "client-model") {
				t.Fatalf("converted Cline response = %+v body=%s", result, result.Body)
			}
			assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 31, Output: 72})
			if !strings.Contains(string(result.Body), "full reasoning") {
				t.Fatalf("reasoning was lost: %s", result.Body)
			}
		})
	}
}

func TestClineRejectsInvalidCompletionsAndNormalizesErrors(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		body       string
		wantStatus int
		message    string
	}{
		{"empty", 200, "", 502, ""},
		{"non-JSON success", 200, "<html>unexpected response</html>", 502, ""},
		{"missing completion", 200, `{"success":true,"data":{"usage":{}}}`, 502, ""},
		{"empty choices", 200, `{"id":"bad","choices":[]}`, 502, ""},
		{"string error", 401, `{"error":"invalid credential"}`, 401, "invalid credential"},
		{"wrapped error", 429, `{"success":false,"error":"quota exhausted","code":"rate_limit"}`, 429, "quota exhausted"},
		{"false success", 200, `{"success":false,"error":{"message":"provider failed","code":"upstream_failure"}}`, 502, "provider failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(t.Context(), clineTestSpec(t, server.URL, protocol.OpenAICompletions))
			if result.Error == nil || result.StatusCode != test.wantStatus {
				t.Fatalf("error result = %+v", result)
			}
			if test.message != "" {
				var body struct {
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(result.Body, &body); err != nil || body.Error.Message != test.message {
					t.Fatalf("normalized error = %s, err=%v", result.Body, err)
				}
			}
		})
	}
}

func TestClinePreservesNonJSONHTTPErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, "invalid credential"},
		{"forbidden", http.StatusForbidden, "<html>access denied</html>"},
		{"rate limited", http.StatusTooManyRequests, "too many requests"},
		{"unavailable", http.StatusServiceUnavailable, "<html>service unavailable</html>"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.Header().Set("Retry-After", "37")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(t.Context(), clineTestSpec(t, server.URL, protocol.OpenAICompletions))
			if result.StatusCode != test.status || result.Error == nil || result.Error.Kind != execution.ErrorKindHTTP || result.Error.StatusCode != test.status {
				t.Fatalf("Cline HTTP error changed: %+v", result)
			}
			if result.Header.Get("Retry-After") != "37" || result.Error.RetryAfter != 37*time.Second {
				t.Fatalf("retry timing lost: headers=%v evidence=%+v", result.Header, result.Error)
			}
			if test.status == http.StatusUnauthorized && (result.Error.Hint != execution.FailureHintInvalidCredential || result.Error.ScopeHint != execution.ErrorScopeCredential) {
				t.Fatalf("credential failure classification lost: %+v", result.Error)
			}
			if test.status == http.StatusTooManyRequests && result.Error.Hint != execution.FailureHintRateLimited {
				t.Fatalf("rate-limit classification lost: %+v", result.Error)
			}
			var body struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(result.Body, &body); err != nil || body.Error.Message == "" || result.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("missing standard error response: %s, err=%v", result.Body, err)
			}
		})
	}
}

func TestClineNativeKeepsGatewayParameters(t *testing.T) {
	for _, model := range []string{"deepseek/deepseek-v4.1-flash", "cline-pass/deepseek-v4.1-flash", "anthropic/claude-sonnet-4.6", "openai/gpt-5.4"} {
		t.Run(model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if string(body["reasoning"]) != `{"enabled":true,"max_tokens":2048}` || !strings.Contains(string(body["messages"]), `"role":"developer"`) || body["thinking"] != nil {
					t.Errorf("Cline parameters changed: %s", body)
				}
				field := "max_tokens"
				if model == "openai/gpt-5.4" {
					field = "max_completion_tokens"
				}
				if string(body[field]) != "4096" {
					t.Errorf("token budget changed: %s", body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, clineCompletionFixture)
			}))
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			spec := clineTestSpec(t, server.URL, protocol.OpenAICompletions)
			spec.UpstreamModel = model
			spec.Body = []byte(`{"model":"client-model","max_tokens":4096,"reasoning":{"enabled":true,"max_tokens":2048},"tool_choice":"required","messages":[{"role":"developer","content":"keep this role"},{"role":"user","content":"ping"}]}`)
			if result := runtime.Execute(t.Context(), spec); result.Error != nil {
				t.Fatalf("Cline result = %+v", result)
			}
		})
	}
}

func TestClineStreamParallelTools(t *testing.T) {
	stream := "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call-a\",\"type\":\"function\",\"function\":{\"name\":\"first_tool\",\"arguments\":\"{}\"}},{\"index\":1,\"id\":\"call-b\",\"type\":\"function\",\"function\":{\"name\":\"second_tool\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer server.Close()
	runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
	var output strings.Builder
	result := runtime.ExecuteStream(t.Context(), clineTestSpec(t, server.URL, protocol.OpenAIResponses), func(event execution.StreamEvent) error { output.Write(event.Data); return nil })
	if result.Error != nil || !strings.Contains(output.String(), "first_tool") || !strings.Contains(output.String(), "second_tool") {
		t.Fatalf("parallel tools lost: error=%+v body=%s", result.Error, output.String())
	}
}

func TestClineStreamReportsChoiceError(t *testing.T) {
	for _, value := range []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses} {
		t.Run(string(value), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"error\",\"error\":{\"code\":\"rate_limit\",\"message\":\"over quota\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			var output strings.Builder
			result := runtime.ExecuteStream(t.Context(), clineTestSpec(t, server.URL, value), func(event execution.StreamEvent) error { output.Write(event.Data); return nil })
			if result.Error == nil || result.Error.Code != "rate_limit" || !strings.Contains(output.String(), "over quota") || strings.Contains(output.String(), "response.completed") {
				t.Fatalf("stream error was lost: %+v, %s", result.Error, output.String())
			}
		})
	}
}

func TestClineConvertedStreamIsProgressiveAndCancelable(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "progressive", true: "canceled"}[cancel], func(t *testing.T) {
			received := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"first token\"}}]}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-received:
				case <-request.Context().Done():
					return
				case <-time.After(2 * time.Second):
					t.Error("Cline buffered the response before emitting text")
					return
				}
				_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			signaled := false
			result := runtime.ExecuteStream(t.Context(), clineTestSpec(t, server.URL, protocol.OpenAIResponses), func(event execution.StreamEvent) error {
				if !signaled && strings.Contains(string(event.Data), "first token") {
					signaled = true
					close(received)
					if cancel {
						return context.Canceled
					}
				}
				return nil
			})
			if !signaled || (!cancel && result.Error != nil) || (cancel && (result.Error == nil || result.Error.Kind != execution.ErrorKindCanceled)) {
				t.Fatalf("Cline progression/cancellation failed: %+v", result)
			}
		})
	}
}

func TestClineResponsesPreservesToolTurnReasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Thinking  json.RawMessage `json:"thinking"`
			Reasoning json.RawMessage `json:"reasoning"`
			Messages  []struct {
				Role      string            `json:"role"`
				Reasoning string            `json:"reasoning_content"`
				ToolCalls []json.RawMessage `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		found := false
		for _, message := range body.Messages {
			if len(message.ToolCalls) > 0 {
				found = true
				if message.Reasoning != "original reasoning" {
					t.Errorf("tool-call reasoning = %q", message.Reasoning)
				}
			}
		}
		if !found || string(body.Thinking) == `{"type":"disabled"}` {
			t.Error("tool history lost or thinking silently disabled")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, clineCompletionFixture)
	}))
	defer server.Close()
	runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := clineTestSpec(t, server.URL, protocol.OpenAIResponses)
	spec.Body = []byte(`{"model":"client-model","reasoning":{"effort":"high"},"input":[{"role":"user","content":"weather"},{"type":"reasoning","content":[{"type":"reasoning_text","text":"original reasoning"}],"summary":[{"type":"summary_text","text":"short summary"}]},{"role":"assistant","content":"Checking."},{"type":"function_call","call_id":"call-1","name":"weather","arguments":"{}"},{"type":"function_call_output","call_id":"call-1","output":"sunny"}]}`)
	result := runtime.Execute(t.Context(), spec)
	if result.Error != nil {
		t.Fatalf("Cline reasoning result = %+v", result)
	}
}

func TestClineStreaming(t *testing.T) {
	const stream = "data: {\"id\":\"cline-id\",\"model\":\"deepseek/deepseek-v4.1-flash\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"id\":\"cline-id\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n" +
		"data: {\"id\":\"cline-id\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"pong\"}}]}\n\n" +
		"data: {\"id\":\"cline-id\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"cline-id\",\"choices\":[],\"usage\":{\"prompt_tokens\":31,\"completion_tokens\":72,\"total_tokens\":103}}\n\n" +
		"data: [DONE]\n\n"
	for _, value := range []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
		t.Run(string(value), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, part := range strings.SplitAfter(stream, "\n") {
					_, _ = io.WriteString(w, part)
					w.(http.Flusher).Flush()
				}
			}))
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			spec := clineTestSpec(t, server.URL, value)
			var output strings.Builder
			if value == protocol.Gemini {
				spec.Path = "/v1beta/models/client-model:streamGenerateContent"
			}
			result := runtime.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
				output.Write(event.Data)
				return nil
			})
			if result.Error != nil || !strings.Contains(output.String(), "pong") || !strings.Contains(output.String(), "client-model") {
				t.Fatalf("Cline stream result = %+v, body=%s", result, output.String())
			}
			assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 31, Output: 72})
			if value == protocol.OpenAIResponses && (!strings.Contains(output.String(), "response.completed") || !strings.Contains(output.String(), `"input_tokens":31`)) {
				t.Fatalf("Responses terminal usage is missing: %s", output.String())
			}
			if value == protocol.OpenAIResponses && strings.Contains(output.String(), `"extra_fields"`) {
				t.Fatalf("SDK metadata leaked: %s", output.String())
			}
		})
	}
}

func TestClineToolRoundTrip(t *testing.T) {
	const toolCompletion = `{"id":"first","object":"chat.completion","created":1,"model":"deepseek/deepseek-v4.1-flash","choices":[{"index":0,"message":{"role":"assistant","content":"Checking.","reasoning":"original reasoning","tool_calls":[{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`
	for _, value := range []protocol.Protocol{protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
		t.Run(string(value), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls++
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				if calls == 1 {
					_, _ = io.WriteString(w, `{"success":true,"data":`+toolCompletion+`}`)
					return
				}
				var sent struct {
					Messages []struct {
						Role      string            `json:"role"`
						Reasoning string            `json:"reasoning_content"`
						ToolCalls []json.RawMessage `json:"tool_calls"`
						Content   json.RawMessage   `json:"content"`
					} `json:"messages"`
				}
				if err := json.Unmarshal(body, &sent); err != nil {
					t.Error(err)
				}
				found := false
				for _, message := range sent.Messages {
					if message.Role == "tool" {
						var text string
						if err := json.Unmarshal(message.Content, &text); err != nil || !strings.Contains(text, "sunny") {
							t.Errorf("tool result must preserve its text in Chat format: %s", body)
						}
					}
					if len(message.ToolCalls) > 0 {
						found = true
						if message.Reasoning != "original reasoning" {
							t.Errorf("tool reasoning lost after %s round trip: %s", value, body)
						}
					}
				}
				if !found {
					t.Errorf("tool call lost after round trip: %s", body)
				}
				_, _ = io.WriteString(w, clineCompletionFixture)
			}))
			defer server.Close()
			runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
			spec := clineTestSpec(t, server.URL, value)
			first := runtime.Execute(t.Context(), spec)
			if first.Error != nil {
				t.Fatalf("first response: %+v", first.Error)
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(first.Body, &response); err != nil {
				t.Fatal(err)
			}
			switch value {
			case protocol.OpenAIResponses:
				var input []json.RawMessage
				if err := json.Unmarshal(response["output"], &input); err != nil {
					t.Fatal(err)
				}
				input = append([]json.RawMessage{json.RawMessage(`{"role":"user","content":"weather"}`)}, input...)
				input = append(input, json.RawMessage(`{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_text","text":"sunny"}]}`))
				spec.Body, _ = json.Marshal(map[string]any{"model": "client-model", "input": input})
			case protocol.Anthropic:
				spec.Body = []byte(`{"model":"client-model","max_tokens":256,"messages":[{"role":"user","content":"weather"},{"role":"assistant","content":` + string(response["content"]) + `},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"sunny"}]}]}`)
			case protocol.Gemini:
				var candidates []struct {
					Content json.RawMessage `json:"content"`
				}
				if err := json.Unmarshal(response["candidates"], &candidates); err != nil || len(candidates) == 0 {
					t.Fatalf("invalid candidates: %s", first.Body)
				}
				spec.Body = []byte(`{"contents":[{"role":"user","parts":[{"text":"weather"}]},` + string(candidates[0].Content) + `,{"role":"user","parts":[{"functionResponse":{"id":"call-1","name":"weather","response":{"result":"sunny"}}}]}]}`)
			}
			second := runtime.Execute(t.Context(), spec)
			if second.Error != nil || calls != 2 {
				t.Fatalf("second response = %+v, calls=%d", second.Error, calls)
			}
		})
	}
}

func TestClineDiscoveryAndProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/v1/models" {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"deepseek/deepseek-v4.1-flash","object":"model"},{"id":"cline-pass/deepseek-v4.1-flash","object":"model"}]}`)
			return
		}
		if request.URL.Path != "/api/v1/chat/completions" {
			t.Errorf("unexpected probe: %s", request.URL.Path)
		}
		_, _ = io.WriteString(w, `{"success":true,"data":`+clineCompletionFixture+`}`)
	}))
	defer server.Close()
	runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := listModelsAttempt(t, channel.ID("cline"), server.URL+"/api/v1")
	result := runtime.Execute(t.Context(), spec)
	if result.Error != nil || !strings.Contains(string(result.Body), `cline-pass/deepseek-v4.1-flash`) {
		t.Fatalf("model discovery = %+v body=%s", result.Error, result.Body)
	}
	for _, value := range []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
		probe := clineTestSpec(t, server.URL, value)
		probe.Operation = execution.OperationProbe
		probe.Method, probe.Path, probe.RawQuery = "", "", ""
		probe.Body, probe.Query = nil, nil
		probe = freezeTestAttempt(probe)
		if result := runtime.Execute(t.Context(), probe); result.Error != nil {
			t.Errorf("%s probe = %+v", value, result.Error)
		}
	}
}

func clineTestSpec(t *testing.T, baseURL string, value protocol.Protocol) execution.AttemptSpec {
	t.Helper()
	spec := openAIChatAttempt(t, channel.ID("cline"), baseURL+"/api/v1", "cline-test-key")
	spec.UpstreamModel = "deepseek/deepseek-v4.1-flash"
	spec.ClientProtocol = value
	switch value {
	case protocol.OpenAIResponses:
		spec.Operation = execution.OperationResponsesCreate
		spec.Path = "/v1/responses"
		spec.Body = []byte(`{"model":"client-model","input":"ping","stream":false}`)
	case protocol.Anthropic:
		spec.Path = "/v1/messages"
		spec.Body = []byte(`{"model":"client-model","max_tokens":256,"messages":[{"role":"user","content":"ping"}]}`)
	case protocol.Gemini:
		spec.Path = "/v1beta/models/client-model:generateContent"
		spec.Body = []byte(`{"contents":[{"role":"user","parts":[{"text":"ping"}]}]}`)
	}
	return freezeTestAttempt(spec)
}
