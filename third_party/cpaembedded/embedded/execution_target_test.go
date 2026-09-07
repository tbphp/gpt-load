package embedded

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubscriptionExecutionUsesAPIProxyRoot(t *testing.T) {
	codex := NewCodexHTTPExecutor()
	claude := NewClaudeHTTPExecutor()
	grok := NewGrokHTTPExecutor()
	codexCredential := CodexCredential{Type: ProviderCodex, AccessToken: "access-secret", RefreshToken: "refresh-secret", AccountID: "account-123"}
	for _, test := range []struct {
		name, official, path string
		request              ExecuteRequest
		run                  func(context.Context, ExecuteRequest) error
	}{
		{name: "codex unary", official: "https://chatgpt.com", path: "/backend-api/codex/responses", request: ExecuteRequest{Model: "gpt-5.2", Format: "openai-response", Payload: []byte(`{"model":"gpt-5.2","input":"hi"}`)}, run: func(ctx context.Context, request ExecuteRequest) error {
			_, err := codex.ExecuteCanonical(ctx, "probe", codexCredential, request)
			return err
		}},
		{name: "codex stream", official: "https://chatgpt.com", path: "/backend-api/codex/responses", request: ExecuteRequest{Model: "gpt-5.2", Format: "openai-response", Payload: []byte(`{"model":"gpt-5.2","input":"hi"}`)}, run: func(ctx context.Context, request ExecuteRequest) error {
			_, err := codex.ExecuteStreamCanonical(ctx, "probe", codexCredential, request)
			return err
		}},
		{name: "codex images", official: "https://chatgpt.com", path: "/backend-api/codex/images/generations", request: ExecuteRequest{Model: "gpt-image-2", Format: "openai-image", RequestPath: "/v1/images/generations", Payload: []byte(`{"model":"gpt-image-2","prompt":"draw"}`)}, run: func(ctx context.Context, request ExecuteRequest) error {
			_, err := codex.ExecuteCanonical(ctx, "probe", codexCredential, request)
			return err
		}},
		{name: "codex image edits stream", official: "https://chatgpt.com", path: "/backend-api/codex/images/edits", request: ExecuteRequest{Model: "gpt-image-2", Format: "openai-image", RequestPath: "/v1/images/edits", Payload: []byte(`{"model":"gpt-image-2","prompt":"draw","images":[{"image_url":"data:image/png;base64,AAAA"}]}`)}, run: func(ctx context.Context, request ExecuteRequest) error {
			_, err := codex.ExecuteStreamCanonical(ctx, "probe", codexCredential, request)
			return err
		}},
		{name: "claude unary", official: "https://api.anthropic.com", path: "/v1/messages?beta=true", request: ExecuteRequest{Model: "claude-sonnet-4-5", Format: "claude", Payload: []byte(`{"model":"claude-sonnet-4-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)}, run: func(ctx context.Context, request ExecuteRequest) error {
			_, err := claude.ExecuteCanonical(ctx, "probe", testClaudeExecutionCredential(), request)
			return err
		}},
		{name: "claude stream", official: "https://api.anthropic.com", path: "/v1/messages?beta=true", request: ExecuteRequest{Model: "claude-sonnet-4-5", Format: "claude", Payload: []byte(`{"model":"claude-sonnet-4-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)}, run: func(ctx context.Context, request ExecuteRequest) error {
			_, err := claude.ExecuteStreamCanonical(ctx, "probe", testClaudeExecutionCredential(), request)
			return err
		}},
		{name: "claude count", official: "https://api.anthropic.com", path: "/v1/messages/count_tokens?beta=true", request: ExecuteRequest{Model: "claude-sonnet-4-5", Format: "claude", Payload: []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`)}, run: func(ctx context.Context, request ExecuteRequest) error {
			_, err := claude.CountTokensCanonical(ctx, "probe", testClaudeExecutionCredential(), request)
			return err
		}},
		{name: "grok unary", official: "https://cli-chat-proxy.grok.com", path: "/v1/responses", request: ExecuteRequest{Model: "grok-4.3", Format: "openai-response", Payload: []byte(`{"model":"grok-4.3","input":"hi"}`)}, run: func(ctx context.Context, request ExecuteRequest) error {
			_, err := grok.ExecuteCanonical(ctx, "probe", testGrokExecutionCredential(), request)
			return err
		}},
		{name: "grok stream", official: "https://cli-chat-proxy.grok.com", path: "/v1/responses", request: ExecuteRequest{Model: "grok-4.3", Format: "openai-response", Payload: []byte(`{"model":"grok-4.3","input":"hi"}`)}, run: func(ctx context.Context, request ExecuteRequest) error {
			_, err := grok.ExecuteStreamCanonical(ctx, "probe", testGrokExecutionCredential(), request)
			return err
		}},
	} {
		for _, root := range []string{"", "https://relay.example/team-a"} {
			t.Run(test.name+"/"+root, func(t *testing.T) {
				calls := 0
				transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
					calls++
					want := test.official + test.path
					if root != "" {
						want = root + test.path
					}
					if request.Method != http.MethodPost || request.URL.String() != want {
						t.Errorf("request = %s %s, want POST %s", request.Method, request.URL, want)
					}
					if !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
						t.Errorf("missing subscription Bearer header")
					}
					if strings.HasPrefix(test.name, "grok") && (request.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" || request.Header.Get("x-grok-client-version") != grokClientVersion || request.Header.Get("x-grok-client-identifier") != "grok-shell" || request.Header.Get("x-authenticateresponse") != "authenticate-response") {
						t.Errorf("missing Grok CLI identity headers: %#v", request.Header)
					}
					return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"denied"}}`)), Request: request}, nil
				})
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
				request := test.request
				request.BaseURL = root
				request.OriginalRequest = append([]byte(nil), request.Payload...)
				if err := test.run(ctx, request); err == nil {
					t.Error("upstream rejection was lost")
				}
				if calls != 1 {
					t.Fatalf("dispatches = %d, want exactly one", calls)
				}
			})
		}
	}
}

func TestAntigravityExecutionUsesAPIProxyRoot(t *testing.T) {
	for _, operation := range []string{"generateContent", "streamGenerateContent", "countTokens"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls++
				if request.Method != http.MethodPost || request.URL.Path != "/team-a/v1internal:"+operation {
					t.Errorf("request = %s %s", request.Method, request.URL)
				}
				if request.Header.Get("Authorization") != "Bearer access-secret" {
					t.Error("missing Bearer header")
				}
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(writer, `{"error":{"message":"denied"}}`)
			}))
			defer server.Close()
			for _, baseURL := range []string{server.URL, server.URL + "/team-a"} {
				_, err := antigravityExecutionTransports.Get(antigravityExecutionTransportKey{credential: "proxy-probe", baseURL: baseURL}, func() (*http.Transport, error) { return server.Client().Transport.(*http.Transport).Clone(), nil })
				if err != nil {
					t.Fatal(err)
				}
			}
			executor := newAntigravityHTTPExecutor(server.URL)
			credential := AntigravityCredential{Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret", AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one", Expire: "2030-01-01T00:00:00Z"}
			request := ExecuteRequest{Model: "gemini-live", Format: "gemini", BaseURL: server.URL + "/team-a", Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)}
			request.OriginalRequest = request.Payload
			var err error
			switch operation {
			case "generateContent":
				_, err = executor.ExecuteCanonical(t.Context(), "proxy-probe", credential, request)
			case "streamGenerateContent":
				_, err = executor.ExecuteStreamCanonical(t.Context(), "proxy-probe", credential, request)
			case "countTokens":
				_, err = executor.CountTokensCanonical(t.Context(), "proxy-probe", credential, request)
			}
			if err == nil || calls != 1 {
				t.Fatalf("error/dispatches = %v/%d", err, calls)
			}
		})
	}
}

func TestSubscriptionProxyKeepsLocalTokenCountsOffline(t *testing.T) {
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(claudeRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("local token counting sent an upstream request")
		return nil, nil
	})))
	for _, root := range []string{"", "https://relay.example/team-a"} {
		if _, err := NewCodexHTTPExecutor().CountTokensCanonical(ctx, "local", CodexCredential{}, ExecuteRequest{Model: "gpt-5.2", Format: "openai-response", BaseURL: root, Payload: []byte(`{"model":"gpt-5.2","input":"hi"}`)}); err != nil {
			t.Fatal(err)
		}
		if _, err := NewGrokHTTPExecutor().CountTokensCanonical(ctx, ExecuteRequest{Model: "grok-4.3", Format: "openai-response", BaseURL: root, Payload: []byte(`{"model":"grok-4.3","input":"hi"}`)}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGrokConversationChangesWithEffectiveTarget(t *testing.T) {
	var conversations []string
	transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		conversations = append(conversations, request.Header.Get("x-grok-conv-id"))
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"denied"}}`)), Request: request}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	executor := NewGrokHTTPExecutor()
	for _, root := range []string{"", "https://cli-chat-proxy.grok.com", "https://relay.example/team-a", "https://relay.example/team-b", "https://relay.example/team-a"} {
		_, err := executor.ExecuteCanonical(ctx, "credential-one", testGrokExecutionCredential(), ExecuteRequest{
			Model: "grok-4.3", Format: "openai-response", Payload: []byte(`{"model":"grok-4.3","input":"hi"}`),
			BaseURL: root, ContinuityKey: "tenant\x00credential-one\x00grok-4.3",
		})
		if err == nil {
			t.Fatal("upstream rejection was lost")
		}
	}
	if len(conversations) != 5 || conversations[0] == "" || conversations[0] != conversations[1] || conversations[2] == conversations[3] || conversations[2] != conversations[4] {
		t.Fatalf("target-scoped conversation IDs = %#v", conversations)
	}
}

func TestAntigravityConnectionsAndSessionsChangeWithEffectiveTarget(t *testing.T) {
	var peers, sessions []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		peers = append(peers, request.RemoteAddr)
		var payload struct {
			Request struct {
				SessionID string `json:"sessionId"`
			} `json:"request"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		sessions = append(sessions, payload.Request.SessionID)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"response":{"candidates":[{"content":{"parts":[{"text":"ok"}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}}`)
	}))
	defer server.Close()
	for _, suffix := range []string{"/team-a", "/team-b"} {
		_, err := antigravityExecutionTransports.Get(antigravityExecutionTransportKey{credential: "connection-probe", baseURL: server.URL + suffix}, func() (*http.Transport, error) {
			return server.Client().Transport.(*http.Transport).Clone(), nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	executor := NewAntigravityHTTPExecutor()
	credential := AntigravityCredential{Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret", AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one", Expire: "2030-01-01T00:00:00Z"}
	for _, suffix := range []string{"/team-a", "/team-a", "/team-b"} {
		_, err := executor.ExecuteCanonical(t.Context(), "connection-probe", credential, ExecuteRequest{Model: "gemini-live", Format: "gemini", ContinuityKey: "tenant-scope", BaseURL: server.URL + suffix, Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(peers) != 3 || peers[0] != peers[1] || peers[0] == peers[2] {
		t.Fatalf("upstream connections = %#v", peers)
	}
	if len(sessions) != 3 || sessions[0] == "" || sessions[0] != sessions[1] || sessions[0] == sessions[2] {
		t.Fatalf("upstream sessions = %#v", sessions)
	}
}
