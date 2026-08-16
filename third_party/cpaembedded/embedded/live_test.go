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

// TestLiveClaudeContract is opt-in because it consumes a real subscription and
// sends model requests. It never refreshes the credential or logs its contents.
func TestLiveClaudeContract(t *testing.T) {
	credentialFile := strings.TrimSpace(os.Getenv("CPA_LIVE_CLAUDE_CREDENTIAL_FILE"))
	model := strings.TrimSpace(os.Getenv("CPA_LIVE_CLAUDE_MODEL"))
	if credentialFile == "" {
		t.Skip("CPA_LIVE_CLAUDE_CREDENTIAL_FILE is not set")
	}

	raw, err := os.ReadFile(credentialFile)
	if err != nil {
		t.Fatalf("read live Claude credential file: %v", err)
	}
	credential, err := ParseClaudeCredentialJSON(raw)
	clear(raw)
	if err != nil {
		t.Fatalf("parse live Claude credential: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	models, err := DiscoverClaudeModels(ctx, credential, ClaudeOptions{})
	if err != nil {
		t.Fatalf("discover live Claude models: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("live Claude model list is empty")
	}
	if model == "" {
		model = selectLiveClaudeModel(models)
	}
	observation, err := ObserveClaudeAccount(ctx, credential, ClaudeOptions{})
	if err != nil {
		t.Fatalf("observe live Claude account: %v", err)
	}
	if observation.Profile.AccountUUID != credential.AccountUUID {
		t.Fatal("live Claude observation returned another account")
	}

	nativePayload, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 16,
		"messages": []map[string]any{{
			"role": "user", "content": "Reply exactly OK.",
		}},
	})
	if err != nil {
		t.Fatalf("build live Claude request: %v", err)
	}
	responsePayload, err := json.Marshal(map[string]any{
		"model": model,
		"input": "Reply exactly OK.",
		"store": false,
	})
	if err != nil {
		t.Fatalf("build live Claude Responses request: %v", err)
	}

	executor := NewClaudeHTTPExecutor()
	nativeResponse, err := executor.ExecuteCanonical(ctx, "live-claude-contract", credential, ExecuteRequest{
		Model: model, Format: "claude", Payload: nativePayload, OriginalRequest: nativePayload,
	})
	if err != nil {
		t.Fatalf("execute live native Claude request: %v", err)
	}
	if !json.Valid(nativeResponse.Payload) {
		t.Fatal("live native Claude response is not JSON")
	}

	convertedResponse, err := executor.ExecuteCanonical(ctx, "live-claude-contract", credential, ExecuteRequest{
		Model: model, Format: "openai-response", Payload: responsePayload, OriginalRequest: responsePayload,
	})
	if err != nil {
		t.Fatalf("execute live Claude Responses request: %v", err)
	}
	if !json.Valid(convertedResponse.Payload) {
		t.Fatal("live Claude Responses result is not JSON")
	}

	var streamRequest map[string]any
	if err := json.Unmarshal(nativePayload, &streamRequest); err != nil {
		t.Fatalf("prepare live Claude stream request: %v", err)
	}
	streamRequest["stream"] = true
	streamPayload, err := json.Marshal(streamRequest)
	if err != nil {
		t.Fatalf("build live Claude stream request: %v", err)
	}
	stream, err := executor.ExecuteStreamCanonical(ctx, "live-claude-contract", credential, ExecuteRequest{
		Model: model, Format: "claude", Payload: streamPayload, OriginalRequest: streamPayload,
	})
	if err != nil {
		t.Fatalf("start live native Claude stream: %v", err)
	}
	chunks := 0
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatalf("read live native Claude stream: %v", chunk.Err)
		}
		if len(chunk.Payload) > 0 {
			chunks++
		}
	}
	if chunks == 0 {
		t.Fatal("live native Claude stream returned no chunks")
	}
}

func selectLiveClaudeModel(models []ClaudeModel) string {
	for _, preferred := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001"} {
		for _, model := range models {
			if model.ID == preferred {
				return model.ID
			}
		}
	}
	return models[0].ID
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
