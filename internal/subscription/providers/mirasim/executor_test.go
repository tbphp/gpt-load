package mirasim

import (
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
}
