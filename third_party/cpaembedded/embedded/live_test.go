package embedded

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveCodexContract is opt-in because it consumes a real subscription and
// sends model requests. It never refreshes the credential or logs its contents.
func TestLiveCodexContract(t *testing.T) {
	credentialFile := strings.TrimSpace(os.Getenv("CPA_LIVE_CREDENTIAL_FILE"))
	if credentialFile == "" {
		t.Skip("CPA_LIVE_CREDENTIAL_FILE is not set")
	}
	raw, err := os.ReadFile(credentialFile)
	if err != nil {
		t.Fatalf("read live credential file: %v", err)
	}
	credential, err := ParseCodexCredentialJSON(raw)
	clear(raw)
	if err != nil {
		t.Fatalf("parse live credential: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	models, err := ListCodexModels(ctx, credential, "")
	if err != nil {
		t.Fatalf("list live models: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("live model list is empty")
	}
	model := selectLiveModel(models)

	observation, err := ObserveCodexAccount(ctx, credential, "")
	if err != nil {
		t.Fatalf("observe live account: %v", err)
	}
	if !json.Valid(observation.Payload) {
		t.Fatal("live account observation is not JSON")
	}

	executor := NewCodexHTTPExecutor()
	response, err := executor.ExecuteCanonical(ctx, "live-contract", credential, ExecuteRequest{
		Model:   model,
		Format:  "openai-response",
		Payload: []byte(`{"input":[{"role":"user","content":[{"type":"input_text","text":"Reply exactly OK."}]}],"store":false}`),
	})
	if err != nil {
		t.Fatalf("execute live response: %v", err)
	}
	if !json.Valid(response.Payload) {
		t.Fatal("live non-stream response is not JSON")
	}
	chatResponse, err := executor.ExecuteCanonical(ctx, "live-contract", credential, ExecuteRequest{
		Model:   model,
		Format:  "openai",
		Payload: []byte(`{"messages":[{"role":"user","content":"Reply exactly OK."}],"store":false}`),
	})
	if err != nil {
		t.Fatalf("execute live chat completion: %v", err)
	}
	if !json.Valid(chatResponse.Payload) {
		t.Fatal("live chat completion is not JSON")
	}

	stream, err := executor.ExecuteStreamCanonical(ctx, "live-contract", credential, ExecuteRequest{
		Model:   model,
		Format:  "openai-response",
		Payload: []byte(`{"input":[{"role":"user","content":[{"type":"input_text","text":"Reply exactly OK."}]}],"store":false,"stream":true}`),
	})
	if err != nil {
		t.Fatalf("start live response stream: %v", err)
	}
	chunks := 0
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatalf("read live response stream: %v", chunk.Err)
		}
		if len(chunk.Payload) > 0 {
			chunks++
		}
	}
	if chunks == 0 {
		t.Fatal("live response stream returned no chunks")
	}
}

func selectLiveModel(models []Model) string {
	for _, preferred := range []string{"gpt-5.2", "gpt-5.1-codex-max", "gpt-5.1-codex"} {
		for _, model := range models {
			if model.ID == preferred {
				return model.ID
			}
		}
	}
	return models[0].ID
}
