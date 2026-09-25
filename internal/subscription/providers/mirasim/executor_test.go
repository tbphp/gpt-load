package mirasim

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildProviderRequestSelectsModelWire(t *testing.T) {
	claudeBody, claudeRoute, err := buildProviderRequest(ExecuteRequest{
		Model: "claude-sonnet-5", Format: "claude",
		Payload: []byte(`{"model":"claude-sonnet-5","messages":[]}`),
	}, false)
	if err != nil || claudeRoute.Path != "/v1/messages" || !strings.Contains(string(claudeBody), `"stream":false`) {
		t.Fatalf("claude route = %#v body=%s err=%v", claudeRoute, claudeBody, err)
	}
	gptBody, gptRoute, err := buildProviderRequest(ExecuteRequest{
		Model: "gpt-5.6", Format: "openai-response",
		Payload: []byte(`{"model":"gpt-5.6","input":"hello"}`),
	}, true)
	if err != nil || gptRoute.Path != "/v1/responses" || !strings.Contains(string(gptBody), `"stream":true`) {
		t.Fatalf("gpt route = %#v body=%s err=%v", gptRoute, gptBody, err)
	}
	if strings.Contains(string(gptBody), "x-anthropic-billing-header") {
		t.Fatalf("gpt body gained a Claude billing header: %s", gptBody)
	}
}

func TestBuildProviderRequestSuppliesClaudeBillingHeaderWhenMissing(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantLen int
	}{
		{name: "no system", payload: `{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`, wantLen: 1},
		{name: "string system", payload: `{"model":"claude-opus-5-5","system":"be brief","messages":[{"role":"user","content":"hi"}]}`, wantLen: 2},
		{name: "array without marker", payload: `{"model":"claude-opus-5-5","system":[{"type":"text","text":"be brief"}],"messages":[{"role":"user","content":"hi"}]}`, wantLen: 2},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body, route, err := buildProviderRequest(ExecuteRequest{
				Model: "claude-opus-5-5", Format: "claude", Payload: []byte(testCase.payload),
			}, false)
			if err != nil {
				t.Fatalf("buildProviderRequest() error = %v", err)
			}
			if route.Path != "/v1/messages" {
				t.Fatalf("path = %s", route.Path)
			}
			blocks := systemBlocks(t, body)
			if len(blocks) != testCase.wantLen {
				t.Fatalf("system blocks = %#v, want %d", blocks, testCase.wantLen)
			}
			if !isClaudeBillingHeader(blocks[0]) {
				t.Fatalf("first system block = %q", blocks[0])
			}
		})
	}
}

func TestBuildProviderRequestKeepsClientClaudeBillingHeader(t *testing.T) {
	original := "x-anthropic-billing-header: cc_version=2.1.281.df4; cc_entrypoint=sdk-cli;"
	payload := `{"model":"claude-opus-5-5","system":[{"type":"text","text":` + jsonQuote(original) + `},{"type":"text","text":"be brief"}],"messages":[{"role":"user","content":"hi"}]}`
	body, _, err := buildProviderRequest(ExecuteRequest{
		Model: "claude-opus-5-5", Format: "claude", Payload: []byte(payload),
	}, false)
	if err != nil {
		t.Fatalf("buildProviderRequest() error = %v", err)
	}
	blocks := systemBlocks(t, body)
	if len(blocks) != 2 || blocks[0] != original || blocks[1] != "be brief" {
		t.Fatalf("system blocks = %#v", blocks)
	}
}

func systemBlocks(t *testing.T, body []byte) []string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	raw, ok := payload["system"].([]any)
	if !ok {
		t.Fatalf("system = %#v", payload["system"])
	}
	texts := make([]string, 0, len(raw))
	for _, block := range raw {
		item, _ := block.(map[string]any)
		text, _ := item["text"].(string)
		texts = append(texts, text)
	}
	return texts
}

func jsonQuote(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
