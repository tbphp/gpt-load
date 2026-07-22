package dialect

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/state"
)

func TestOpenAIProbeSendsMinimalAuthenticatedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != openAIChatCompletionsPath {
			t.Errorf("request = %s %s, want POST %s", request.Method, request.URL.Path, openAIChatCompletionsPath)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want bearer credential", got)
		}
		assertOpenAIProbePayload(t, request.Body, "gpt-test")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := NewOpenAI(server.Client()).Probe(
		context.Background(), server.URL, "secret", state.HeaderRules{}, "gpt-test",
	); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
}

func TestAnthropicProbeSendsMinimalAuthenticatedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != anthropicMessagesPath {
			t.Errorf("request = %s %s, want POST %s", request.Method, request.URL.Path, anthropicMessagesPath)
		}
		if got := request.Header.Get("X-Api-Key"); got != "secret" {
			t.Errorf("X-Api-Key = %q, want credential", got)
		}
		if got := request.Header.Get("Anthropic-Version"); got != anthropicDefaultVersion {
			t.Errorf("Anthropic-Version = %q, want %q", got, anthropicDefaultVersion)
		}
		assertOpenAIProbePayload(t, request.Body, "claude-test")
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	if err := NewAnthropic(server.Client()).Probe(
		context.Background(), server.URL, "secret", state.HeaderRules{}, "claude-test",
	); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
}

func TestGeminiProbeSendsMinimalAuthenticatedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1beta/models/gemini-test:generateContent" {
			t.Errorf("request = %s %s, want POST Gemini generation path", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("X-Goog-Api-Key"); got != "secret" {
			t.Errorf("X-Goog-Api-Key = %q, want credential", got)
		}
		if got := request.URL.Query().Get("key"); got != "" {
			t.Errorf("key query = %q, want empty", got)
		}
		assertGeminiProbePayload(t, request.Body, "gemini-test")
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := NewGemini(server.Client()).Probe(
		context.Background(), server.URL+"?tenant=one", "secret", state.HeaderRules{}, "gemini-test",
	); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
}

func TestProbeAppliesHeaderRulesAndForcesIdentity(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("X-Probe"); got != "prefix-secret" {
			t.Errorf("X-Probe = %q, want expanded rule", got)
		}
		if got := request.Header.Values("Accept-Encoding"); len(got) != 1 || got[0] != "identity" {
			t.Errorf("Accept-Encoding = %#v, want one identity", got)
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("ignored")),
			Header:     make(http.Header),
		}, nil
	})}

	err := NewOpenAI(client).Probe(context.Background(), "https://api.example.com", "secret", state.HeaderRules{
		Set: map[string]string{
			"X-Probe":         "prefix-${API_KEY}",
			"Accept-Encoding": "gzip",
		},
	}, "gpt-test")
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
}

func TestProbeAcceptsOnly2xxAndRedactsTransportDetails(t *testing.T) {
	t.Run("accepts every 2xx and closes response", func(t *testing.T) {
		body := &trackedReadCloser{Reader: strings.NewReader("ignored")}
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 299, Body: body, Header: make(http.Header)}, nil
		})}
		if err := NewOpenAI(client).Probe(context.Background(), "https://api.example.com", "secret", state.HeaderRules{}, "gpt-test"); err != nil {
			t.Fatalf("Probe() error = %v", err)
		}
		if !body.closed.Load() {
			t.Fatal("response body was not closed")
		}
	})

	t.Run("non 2xx contains only protocol and status", func(t *testing.T) {
		const secret = "distinctive-probe-secret"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`{"error":"` + secret + `"}`))
		}))
		defer server.Close()

		err := NewOpenAI(server.Client()).Probe(context.Background(), server.URL+"?token=query-secret", secret, state.HeaderRules{}, "gpt-test")
		if err == nil || !strings.Contains(err.Error(), "openai") || !strings.Contains(err.Error(), "403") {
			t.Fatalf("Probe() error = %v, want protocol and status", err)
		}
		for _, forbidden := range []string{secret, "query-secret", server.URL} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("Probe() error exposes %q: %v", forbidden, err)
			}
		}
	})

	t.Run("transport error is redacted", func(t *testing.T) {
		const secret = "distinctive-probe-secret"
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport failed for https://bad.example.com?token=" + secret)
		})}
		err := NewOpenAI(client).Probe(context.Background(), "https://api.example.com", secret, state.HeaderRules{}, "gpt-test")
		if err == nil || !strings.Contains(err.Error(), "request openai probe failed") {
			t.Fatalf("Probe() error = %v, want redacted transport failure", err)
		}
		for _, forbidden := range []string{secret, "bad.example.com", "api.example.com"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("Probe() error exposes %q: %v", forbidden, err)
			}
		}
	})

	t.Run("nil client returns a safe error", func(t *testing.T) {
		err := NewOpenAI(nil).Probe(context.Background(), "https://api.example.com", "secret", state.HeaderRules{}, "gpt-test")
		if err == nil || !strings.Contains(err.Error(), "request openai probe failed") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("Probe() error = %v, want redacted nil-client failure", err)
		}
	})
}

func TestProbeHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := NewOpenAI(http.DefaultClient).Probe(ctx, "https://api.example.com", "secret", state.HeaderRules{}, "gpt-test")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe() error = %v, want context cancellation", err)
	}
}

func TestProbeRejectsBlankValidationModelWithoutRequest(t *testing.T) {
	var requests atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected request")
	})}

	for _, value := range []Dialect{NewOpenAI(client), NewAnthropic(client), NewGemini(client)} {
		if err := value.Probe(context.Background(), "https://api.example.com", "secret", state.HeaderRules{}, " \t "); err == nil {
			t.Fatalf("%s Probe() error = nil for blank validation model", value.Protocol())
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want none", got)
	}
}

func TestGeminiProbeDoesNotNormalizeConfiguredModel(t *testing.T) {
	var requests atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected request")
	})}

	err := NewGemini(client).Probe(context.Background(), "https://api.example.com", "secret", state.HeaderRules{}, "models/gemini-test")
	if err == nil {
		t.Fatal("Probe() error = nil for an unnormalized models/ prefix")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want none", got)
	}
}

func assertOpenAIProbePayload(t *testing.T, body io.Reader, wantModel string) {
	t.Helper()
	var payload struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatalf("decode Probe payload: %v", err)
	}
	if payload.Model != wantModel || payload.MaxTokens != 1 || len(payload.Messages) != 1 ||
		payload.Messages[0].Role != "user" || payload.Messages[0].Content != "ping" {
		t.Fatalf("Probe payload = %#v, want model %q and one user ping with max_tokens 1", payload, wantModel)
	}
}

func assertGeminiProbePayload(t *testing.T, body io.Reader, wantModel string) {
	t.Helper()
	var payload struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		GenerationConfig struct {
			MaxOutputTokens int `json:"maxOutputTokens"`
		} `json:"generationConfig"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatalf("decode Probe payload: %v", err)
	}
	if len(payload.Contents) != 1 || payload.Contents[0].Role != "user" ||
		len(payload.Contents[0].Parts) != 1 || payload.Contents[0].Parts[0].Text != "ping" ||
		payload.GenerationConfig.MaxOutputTokens != 1 {
		t.Fatalf("Probe payload = %#v, want one user ping with maxOutputTokens 1", payload)
	}
	_ = wantModel
}
