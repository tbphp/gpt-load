package bifrost

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/version"
	"gpt-load/internal/protocol"
)

func TestNewAPIModelDiscoveryUserAgentOnWire(t *testing.T) {
	for _, test := range []struct {
		name       string
		header     http.Header
		configured []string
		want       string
	}{
		{name: "system default", want: "GPT-Load/" + version.Version},
		{name: "ignore client", header: http.Header{"User-Agent": {"client/1.0"}}, want: "GPT-Load/" + version.Version},
		{name: "configured override", header: http.Header{"User-Agent": {"operator/2.0"}}, configured: []string{"user-agent"}, want: "operator/2.0"},
		{name: "explicit removal", configured: []string{"User-Agent"}, want: "GPT-Load/" + version.Version},
		{name: "explicit empty", header: http.Header{"User-Agent": {""}}, configured: []string{"User-Agent"}, want: "GPT-Load/" + version.Version},
		{name: "explicit whitespace", header: http.Header{"User-Agent": {" \t "}}, configured: []string{"User-Agent"}, want: "GPT-Load/" + version.Version},
	} {
		t.Run(test.name, func(t *testing.T) {
			seen := make(chan http.Header, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"data":[{"id":"model-one"}]}`))
			}))
			defer server.Close()
			manager, target := newNewAPIManagerForTest(t, server.URL)
			spec := utilitySpec(channel.NewAPI, protocol.OpenAICompletions, execution.OperationListModels, http.MethodGet, "/v1/models", nil)
			spec.TargetConfig = target.TargetConfig
			spec.Header = test.header.Clone()
			spec.ConfiguredHeaders = test.configured
			spec = freezeTestAttempt(spec)
			original := spec.Header.Clone()
			result := manager.Execute(t.Context(), spec)
			if result.Error != nil || result.StatusCode != http.StatusOK {
				t.Fatalf("discovery failed: %+v", result)
			}
			header := <-seen
			if got := header.Get("User-Agent"); got != test.want {
				t.Errorf("wire User-Agent = %q, want %q", got, test.want)
			}
			if !reflect.DeepEqual(spec.Header, original) {
				t.Error("execution mutated input headers")
			}
		})
	}
}

func TestRequestUserAgentOnWire(t *testing.T) {
	for _, route := range []string{"native", "typed"} {
		for _, operation := range []string{"unary", "stream", "probe"} {
			for _, policy := range []string{"default", "configured", "removed", "empty", "whitespace"} {
				name := route + "/" + operation + "/" + policy
				want := "GPT-Load/" + version.Version
				if policy == "configured" {
					want = "operator/2.0"
				}
				t.Run(name, func(t *testing.T) {
					seen := make(chan string, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						seen <- r.Header.Get("User-Agent")
						if operation == "stream" {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = w.Write([]byte("data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
							return
						}
						writeSuccess(w, "ok")
					}))
					defer server.Close()
					runtime := newTestRuntime(t)
					spec := compatibleSpec(server.URL)
					if route == "typed" {
						// 非 /v1 前缀走 SDK typed fallback，覆盖不同的 HTTP 构造路径。
						spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `/tenant/openai"}`)
					}
					spec.Header.Set("User-Agent", "client/1.0")
					if policy != "default" {
						spec.Header.Set("User-Agent", want)
						spec.ConfiguredHeaders = []string{"User-Agent"}
					}
					switch policy {
					case "removed":
						spec.Header.Del("User-Agent")
					case "empty":
						spec.Header.Set("User-Agent", "")
					case "whitespace":
						spec.Header.Set("User-Agent", " \t ")
					}
					if operation == "probe" {
						spec.Operation = execution.OperationProbe
						spec.Method, spec.Path = "", ""
						spec.Body = nil
					}
					spec = freezeTestAttempt(spec)
					original := spec.Header.Clone()
					if operation == "stream" {
						result := runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil })
						if result.Error != nil {
							t.Fatalf("stream failed: %+v", result)
						}
					} else {
						result := runtime.Execute(t.Context(), spec)
						if result.Error != nil || result.StatusCode != http.StatusOK {
							t.Fatalf("request failed: %+v", result)
						}
					}
					if got := <-seen; got != want {
						t.Errorf("wire User-Agent = %q, want %q", got, want)
					}
					if !reflect.DeepEqual(spec.Header, original) {
						t.Error("execution mutated input headers")
					}
				})
			}
		}
	}
}

func TestWebsocketUserAgentOnWire(t *testing.T) {
	for _, policy := range []string{"default", "configured", "removed", "empty", "whitespace"} {
		want := "GPT-Load/" + version.Version
		if policy == "configured" {
			want = "operator/2.0"
		}
		t.Run(policy, func(t *testing.T) {
			seen := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.Header.Get("User-Agent")
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()
			manager, _ := newNewAPIManagerForTest(t, server.URL)
			spec := compatibleSpec(server.URL)
			spec.ChannelID = string(channel.OpenAI)
			spec.ClientProtocol = protocol.OpenAIResponses
			spec.Operation = execution.OperationResponsesCreate
			spec.Path = "/v1/responses"
			spec.Body = []byte(`{"model":"upstream-model","input":"hello"}`)
			spec.Header.Set("User-Agent", "client/1.0")
			if policy != "default" {
				spec.Header.Set("User-Agent", want)
				spec.ConfiguredHeaders = []string{"user-agent"}
			}
			switch policy {
			case "removed":
				spec.Header.Del("User-Agent")
			case "empty":
				spec.Header.Set("User-Agent", "")
			case "whitespace":
				spec.Header.Set("User-Agent", " \t ")
			}
			spec = freezeTestAttempt(spec)
			original := spec.Header.Clone()
			session, result := manager.OpenWebsocket(t.Context(), spec)
			if result.Error != nil || session == nil {
				t.Fatalf("websocket failed: %+v", result)
			}
			defer session.Close()
			if got := <-seen; got != want {
				t.Errorf("wire User-Agent = %q, want %q", got, want)
			}
			if !reflect.DeepEqual(spec.Header, original) {
				t.Error("execution mutated input headers")
			}
		})
	}
}
