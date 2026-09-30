package embedded

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

func TestCodexCapabilityPreservesUpdatesAcrossTurns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			t.Error(e)
			return
		}
		defer c.Close()
		for i := 0; i < 5; i++ {
			_, b, e := c.ReadMessage()
			if e != nil {
				t.Error(e)
				return
			}
			wantBaseline := "high"
			if i == 4 {
				wantBaseline = "low"
			}
			if gjson.GetBytes(b, "reasoning.effort").String() != wantBaseline {
				t.Errorf("round %d baseline changed", i)
			}
			if i == 1 && gjson.GetBytes(b, "input.0.reasoning.effort").String() != "max" {
				t.Error("update lost")
			}
			if e = c.WriteMessage(websocket.TextMessage, wsCompleted(fmt.Sprintf("cap_%d", i))); e != nil {
				t.Error(e)
				return
			}
		}
		_, _, _ = c.ReadMessage()
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	for i := 0; i < 5; i++ {
		body := map[string]any{"model": "gpt-6.1-sol", "reasoning": map[string]any{"effort": "high"}, "input": []any{map[string]any{"role": "user", "content": "hello"}}}
		if (i > 0 && i < 3) || i == 4 {
			body["previous_response_id"] = fmt.Sprintf("cap_%d", i-1)
		}
		if i == 1 {
			body["input"] = []any{map[string]any{"type": "configuration_update", "reasoning": map[string]any{"effort": "max"}}, map[string]any{"role": "user", "content": "next"}}
		}
		if i == 4 {
			body["reasoning"] = map[string]any{"effort": "low"}
		}
		raw, _ := json.Marshal(body)
		result, e := session.ExecuteTurn(t.Context(), raw, nil)
		if e != nil {
			t.Fatal(e)
		}
		want := "high"
		if i > 0 && i < 3 {
			want = "max"
		}
		if i == 4 {
			want = "low"
		}
		if result.AppliedReasoningEffort != want {
			t.Errorf("round %d effort %q want %q", i, result.AppliedReasoningEffort, want)
		}
	}
}
func TestCodexCredentialPreservesPlan(t *testing.T) {
	c, e := ParseCodexCredentialJSON([]byte(`{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"account","plan_type":"pro"}`))
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if gjson.GetBytes(b, "plan_type").String() != "pro" {
		t.Fatal("plan was lost")
	}
}

func TestCodexPlanClaimsAndMissingClaims(t *testing.T) {
	token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_plan_type":"plus"}}`)) + ".signature"
	if got := CodexCredentialPlan(CodexCredential{AccessToken: token}); got != "plus" {
		t.Fatalf("claim = %q", got)
	}
	if got := CodexCredentialPlan(CodexCredential{AccessToken: "opaque"}); got != "" {
		t.Fatal("missing claim became free")
	}
	if got := CodexCredentialPlan(CodexCredential{PlanType: "pro", IDToken: token}); got != "pro" {
		t.Fatal("older ID token replaced refreshed plan")
	}
}
func TestCodexHTTPPreservesConfigurationUpdates(t *testing.T) {
	transport := environmentRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		b, e := io.ReadAll(r.Body)
		if e != nil {
			return nil, e
		}
		if gjson.GetBytes(b, "reasoning.effort").String() != "high" || gjson.GetBytes(b, "input.0.reasoning.effort").String() != "max" {
			t.Error("wire update changed")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + string(wsCompleted("http-cap")) + "\n\n")), Request: r}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	response, e := NewCodexHTTPExecutor().ExecuteCanonical(ctx, "cap", CodexCredential{Type: ProviderCodex, AccessToken: "synthetic", RefreshToken: "synthetic", AccountID: "synthetic"}, ExecuteRequest{Model: "gpt-6.1-sol", Format: "openai-response", Payload: []byte(`{"model":"gpt-6.1-sol","reasoning":{"effort":"high"},"input":[{"type":"configuration_update","reasoning":{"effort":"max"}},{"role":"user","content":"hello"}]}`)})
	if e != nil {
		t.Fatal(e)
	}
	if response.AppliedReasoningEffort != "max" {
		t.Fatalf("applied effort %q", response.AppliedReasoningEffort)
	}
}
