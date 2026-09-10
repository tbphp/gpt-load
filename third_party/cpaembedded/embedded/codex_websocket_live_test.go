package embedded

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// 此合同需要单独授权的真实账号和模型；默认跳过，不刷新 token、不输出凭据。
func TestLiveCodexWSSessionContract(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("CPA_LIVE_CODEX_WS_CREDENTIAL_FILE"))
	if path == "" {
		t.Skip("CPA_LIVE_CODEX_WS_CREDENTIAL_FILE is not set")
	}
	model := strings.TrimSpace(os.Getenv("CPA_LIVE_CODEX_WS_MODEL"))
	if model == "" {
		t.Fatal("CPA_LIVE_CODEX_WS_MODEL is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read live WS credential")
	}
	credential, err := ParseCodexCredentialJSON(raw)
	clear(raw)
	if err != nil {
		t.Fatal("parse live WS credential")
	}
	proxy := strings.TrimSpace(os.Getenv("CPA_LIVE_CODEX_WS_PROXY_URL"))
	if proxy == "" {
		proxy = "direct"
	}
	session, err := NewCodexWSSession(CodexWSSessionOptions{
		CredentialID: "live-ws-contract", Credential: credential,
		BaseURL: os.Getenv("CPA_LIVE_CODEX_WS_BASE_URL"), ProxyURL: proxy, TurnTimeout: 90 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	marker := uuid.NewString()
	firstBody, err := json.Marshal(map[string]any{"model": model, "store": false, "input": "Remember this marker for the next turn: " + marker + ". Reply OK."})
	if err != nil {
		t.Fatal(err)
	}
	first, err := session.ExecuteTurn(context.Background(), firstBody, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := json.Marshal(map[string]any{"model": model, "store": false, "previous_response_id": first.ResponseID, "input": "Return only the marker supplied in the previous request."})
	if err != nil {
		t.Fatal(err)
	}
	var answer strings.Builder
	second, err := session.ExecuteTurn(context.Background(), secondBody, func(_ context.Context, event json.RawMessage) error {
		var payload struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal(event, &payload); err != nil {
			return err
		}
		if payload.Type == "response.output_text.delta" {
			answer.WriteString(payload.Delta)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ResponseID == "" || second.ResponseID == first.ResponseID || !strings.Contains(answer.String(), marker) {
		t.Fatal("live WS continuation did not retain the first turn context")
	}
}
