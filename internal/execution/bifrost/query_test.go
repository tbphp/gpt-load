package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestRuntimeNormalizesConvertedProtocolQueries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name           string
		clientProtocol protocol.Protocol
		channelID      channel.ID
		rawQuery       string
		query          url.Values
		wantQuery      string
	}{
		{
			name: "Anthropic to Gemini", clientProtocol: protocol.Anthropic, channelID: channel.Gemini,
			rawQuery:  "beta=true&%62eta=false&trace=%2f&&cursor=next&api_key=removed",
			wantQuery: "trace=%2f&&cursor=next",
		},
		{
			name: "Gemini to Anthropic", clientProtocol: protocol.Gemini, channelID: channel.Anthropic,
			rawQuery:  "alt=json&$alt=proto&%24alt=sse&trace=%2F&trace=%2f&key=removed",
			wantQuery: "trace=%2F&trace=%2f",
		},
		{
			name: "Gemini query values to OpenAI", clientProtocol: protocol.Gemini, channelID: channel.OpenAI,
			query:     url.Values{"alt": {"json"}, "$alt": {"sse"}, "trace": {"kept"}, "key": {"removed"}},
			wantQuery: "trace=kept",
		},
		{
			name: "native Anthropic preserves beta", clientProtocol: protocol.Anthropic, channelID: channel.Anthropic,
			rawQuery: "beta=true&trace=%2f&key=removed", wantQuery: "beta=true&trace=%2f",
		},
		{
			name: "native Gemini retains existing format handling", clientProtocol: protocol.Gemini, channelID: channel.Gemini,
			rawQuery: "alt=json&trace=%2f&key=removed", wantQuery: "trace=%2f",
		},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, stream), func(t *testing.T) {
				wantQuery := test.wantQuery
				if test.channelID == channel.Gemini && stream {
					wantQuery += "&alt=sse"
				}
				wantPath := "/v1/messages"
				responseBody := anthropicResponsesConvertedFixture
				if stream {
					responseBody = anthropicResponsesStreamFixture
				}
				switch test.channelID {
				case channel.Gemini:
					wantPath = "/v1beta/models/upstream-model:generateContent"
					responseBody = geminiResponsesConvertedFixture
					if stream {
						wantPath = "/v1beta/models/upstream-model:streamGenerateContent"
						responseBody = geminiResponsesStreamFixture
					}
				case channel.OpenAI:
					wantPath = "/v1/responses"
					responseBody = openAIResponsesConvertedFixture
					if stream {
						responseBody = openAIResponsesStreamFixture
					}
				}
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if request.URL.Path != wantPath || request.URL.RawQuery != wantQuery {
						t.Errorf("target = %s?%s, want %s?%s", request.URL.Path, request.URL.RawQuery, wantPath, wantQuery)
					}
					if request.Header.Get("Anthropic-Beta") != "test-feature" {
						t.Error("query normalization changed the request header")
					}
					writer.Header().Set("Content-Type", "application/json")
					if stream {
						writer.Header().Set("Content-Type", "text/event-stream")
					}
					_, _ = io.WriteString(writer, responseBody)
				}))
				defer server.Close()

				runtime := newProtocolTestRuntime(t, testRuntimeOptions{
					allowPrivateNetwork: true, openAIBaseURL: server.URL,
					anthropicBaseURL: server.URL, geminiBaseURL: server.URL + "/v1beta",
				})
				path := "/v1/messages"
				body := []byte(`{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`)
				if test.clientProtocol == protocol.Gemini {
					path = "/v1beta/models/client-model:generateContent"
					if stream {
						path = "/v1beta/models/client-model:streamGenerateContent"
					}
					body = []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
				}
				spec := convertedSpec(test.channelID, test.clientProtocol, execution.OperationChatCompletion, path, body)
				spec.RawQuery = test.rawQuery
				spec.Query = test.query
				spec.Header.Set("Anthropic-Beta", "test-feature")
				original := spec.Clone()
				if stream {
					result := runtime.ExecuteStream(context.Background(), spec, func(execution.StreamEvent) error { return nil })
					if result.Error != nil || result.StatusCode != http.StatusOK {
						t.Fatalf("stream result = %+v", result)
					}
				} else {
					result := runtime.Execute(context.Background(), spec)
					if result.Error != nil || result.StatusCode != http.StatusOK {
						t.Fatalf("result = %+v", result)
					}
				}
				if !reflect.DeepEqual(spec, original) {
					t.Error("execution mutated the original attempt")
				}
			})
		}
	}
}

func TestConvertedCountTokensDropsAnthropicBetaQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1beta/models/upstream-model:countTokens" || request.URL.RawQuery != "trace=%2f" {
			t.Errorf("count tokens target = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"totalTokens":7}`)
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
	spec := convertedSpec(channel.Gemini, protocol.Anthropic, execution.OperationCountTokens,
		"/v1/messages/count_tokens", []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`))
	spec.RawQuery = "beta=true&trace=%2f"
	result := runtime.Execute(context.Background(), spec)
	if result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v", result)
	}
}

func TestConvertedProtocolQueriesDoNotBlockProviderPreparation(t *testing.T) {
	t.Parallel()

	for _, target := range []struct {
		channelID channel.ID
		config    json.RawMessage
	}{
		{channel.Groq, json.RawMessage(`{}`)},
		{channel.AzureOpenAI, json.RawMessage(`{"endpoint":"https://resource.openai.azure.com"}`)},
		{channel.AWSBedrock, json.RawMessage(`{"region":"us-east-1"}`)},
	} {
		for _, clientProtocol := range []protocol.Protocol{protocol.Anthropic, protocol.Gemini} {
			t.Run(fmt.Sprintf("%s/%s", target.channelID, clientProtocol), func(t *testing.T) {
				runtime := newProtocolTestRuntime(t, testRuntimeOptions{})
				path := "/v1/messages"
				body := []byte(`{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`)
				query := "beta=true"
				if clientProtocol == protocol.Gemini {
					path = "/v1beta/models/client-model:streamGenerateContent"
					body = []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
					query = "alt=sse&%24alt=json"
				}
				spec := convertedSpec(target.channelID, clientProtocol, execution.OperationChatCompletion, path, body)
				spec.TargetConfig = target.config
				spec = freezeTestAttempt(spec)
				spec.RawQuery = query
				if _, failure := runtime.prepare(spec, true); failure != nil {
					t.Fatalf("prepare() error = %+v", failure.Error)
				}
			})
		}
	}
}
