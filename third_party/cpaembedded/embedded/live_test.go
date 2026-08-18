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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	if len(observation.IncompleteSources) > 0 {
		t.Fatalf("live Claude observation is incomplete: %s", strings.Join(observation.IncompleteSources, ","))
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
	nativeCountPayload, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]any{{
			"role": "user", "content": "Reply exactly OK.",
		}},
	})
	if err != nil {
		t.Fatalf("build live Claude CountTokens request: %v", err)
	}
	responsePayload, err := json.Marshal(map[string]any{
		"model": model,
		"input": "Reply exactly OK.",
		"store": false,
	})
	if err != nil {
		t.Fatalf("build live Claude Responses request: %v", err)
	}
	chatPayload, err := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]any{{"role": "user", "content": "Reply exactly OK."}},
	})
	if err != nil {
		t.Fatalf("build live Claude Chat Completions request: %v", err)
	}
	geminiPayload, err := json.Marshal(map[string]any{
		"contents": []map[string]any{{
			"role": "user", "parts": []map[string]any{{"text": "Reply exactly OK."}},
		}},
	})
	if err != nil {
		t.Fatalf("build live Claude Gemini request: %v", err)
	}

	executor := NewClaudeHTTPExecutor()
	requests := []struct {
		name        string
		format      string
		payload     []byte
		streamField bool
	}{
		{name: "Anthropic Messages", format: "claude", payload: nativePayload, streamField: true},
		{name: "OpenAI Chat Completions", format: "openai", payload: chatPayload, streamField: true},
		{name: "OpenAI Responses", format: "openai-response", payload: responsePayload, streamField: true},
		{name: "Gemini GenerateContent", format: "gemini", payload: geminiPayload},
	}
	for _, request := range requests {
		t.Run(request.name+" unary", func(t *testing.T) {
			response, executeErr := executor.ExecuteCanonical(ctx, "live-claude-contract", credential, ExecuteRequest{
				Model: model, Format: request.format, Payload: request.payload, OriginalRequest: request.payload,
			})
			if executeErr != nil {
				t.Fatalf("execute live request: %v", executeErr)
			}
			if !json.Valid(response.Payload) {
				t.Fatal("live response is not JSON")
			}
		})
		t.Run(request.name+" stream", func(t *testing.T) {
			streamPayload := request.payload
			if request.streamField {
				var value map[string]any
				if json.Unmarshal(request.payload, &value) != nil {
					t.Fatal("prepare live stream request")
				}
				value["stream"] = true
				streamPayload, err = json.Marshal(value)
				if err != nil {
					t.Fatalf("build live stream request: %v", err)
				}
			}
			stream, streamErr := executor.ExecuteStreamCanonical(ctx, "live-claude-contract", credential, ExecuteRequest{
				Model: model, Format: request.format, Payload: streamPayload, OriginalRequest: streamPayload,
			})
			if streamErr != nil {
				t.Fatalf("start live stream: %v", streamErr)
			}
			chunks := 0
			for chunk := range stream.Chunks {
				if chunk.Err != nil {
					t.Fatalf("read live stream: %v", chunk.Err)
				}
				if len(chunk.Payload) > 0 {
					chunks++
				}
			}
			if chunks == 0 {
				t.Fatal("live stream returned no chunks")
			}
		})
	}

	for _, request := range []struct {
		name    string
		format  string
		payload []byte
		field   string
	}{
		{name: "Anthropic", format: "claude", payload: nativeCountPayload, field: "input_tokens"},
		{name: "OpenAI Responses", format: "openai-response", payload: responsePayload, field: "input_tokens"},
		{name: "Gemini", format: "gemini", payload: geminiPayload, field: "totalTokens"},
	} {
		t.Run(request.name+" CountTokens", func(t *testing.T) {
			response, countErr := executor.CountTokensCanonical(ctx, "live-claude-contract", credential, ExecuteRequest{
				Model: model, Format: request.format, Payload: request.payload, OriginalRequest: request.payload,
			})
			if countErr != nil {
				t.Fatalf("count live Claude tokens: %v", countErr)
			}
			var count map[string]any
			if json.Unmarshal(response.Payload, &count) != nil {
				t.Fatal("live CountTokens result is not JSON")
			}
			value, ok := count[request.field].(float64)
			if !ok || value <= 0 {
				t.Fatalf("live CountTokens result has no positive %s", request.field)
			}
		})
	}
}

// TestLiveAntigravityContract is opt-in because it consumes a real subscription
// and sends model requests. It uses only a prepared canonical credential and
// never refreshes or logs it.
func TestLiveAntigravityContract(t *testing.T) {
	credentialFile := strings.TrimSpace(os.Getenv("CPA_LIVE_ANTIGRAVITY_CREDENTIAL_FILE"))
	model := strings.TrimSpace(os.Getenv("CPA_LIVE_ANTIGRAVITY_MODEL"))
	if credentialFile == "" {
		t.Skip("CPA_LIVE_ANTIGRAVITY_CREDENTIAL_FILE is not set")
	}

	raw, err := os.ReadFile(credentialFile)
	if err != nil {
		t.Fatalf("read live Antigravity credential file: %v", err)
	}
	credential, err := ParseAntigravityCredentialJSON(raw)
	clear(raw)
	if err != nil {
		t.Fatalf("parse live Antigravity credential: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	models, err := DiscoverAntigravityModels(ctx, credential, AntigravityOptions{})
	if err != nil {
		t.Fatalf("discover live Antigravity models: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("live Antigravity model list is empty")
	}
	if model == "" {
		model = selectLiveAntigravityModel(models)
	}
	observation, err := ObserveAntigravityAccount(ctx, credential, AntigravityOptions{})
	if err != nil {
		t.Fatalf("observe live Antigravity account: %v", err)
	}
	if !observation.AccountObserved || !observation.QuotaObserved || len(observation.IncompleteSources) > 0 {
		t.Fatalf("live Antigravity observation is incomplete: %s", strings.Join(observation.IncompleteSources, ","))
	}

	payloads := []struct {
		name   string
		format string
		body   map[string]any
	}{
		{name: "Gemini GenerateContent", format: "gemini", body: map[string]any{
			"contents": []map[string]any{{"role": "user", "parts": []map[string]any{{"text": "Reply exactly OK."}}}},
		}},
		{name: "Anthropic Messages", format: "claude", body: map[string]any{
			"model": model, "max_tokens": 16,
			"messages": []map[string]any{{"role": "user", "content": "Reply exactly OK."}},
		}},
		{name: "OpenAI Chat Completions", format: "openai", body: map[string]any{
			"model": model, "messages": []map[string]any{{"role": "user", "content": "Reply exactly OK."}},
		}},
		{name: "OpenAI Responses", format: "openai-response", body: map[string]any{
			"model": model, "input": "Reply exactly OK.", "store": false,
		}},
	}
	executor := NewAntigravityHTTPExecutor()
	for _, request := range payloads {
		payload, marshalErr := json.Marshal(request.body)
		if marshalErr != nil {
			t.Fatalf("build live Antigravity request: %v", marshalErr)
		}
		t.Run(request.name+" unary", func(t *testing.T) {
			response, executeErr := executor.ExecuteCanonical(ctx, "live-antigravity-contract", credential, ExecuteRequest{
				Model: model, Format: request.format, Payload: payload, OriginalRequest: payload,
			})
			if executeErr != nil {
				t.Fatalf("execute live request: %v", executeErr)
			}
			if !json.Valid(response.Payload) {
				t.Fatal("live response is not JSON")
			}
		})
		t.Run(request.name+" stream", func(t *testing.T) {
			streamBody := make(map[string]any, len(request.body)+1)
			for key, value := range request.body {
				streamBody[key] = value
			}
			streamBody["stream"] = true
			streamPayload, marshalErr := json.Marshal(streamBody)
			if marshalErr != nil {
				t.Fatalf("build live stream request: %v", marshalErr)
			}
			stream, streamErr := executor.ExecuteStreamCanonical(ctx, "live-antigravity-contract", credential, ExecuteRequest{
				Model: model, Format: request.format, Payload: streamPayload, OriginalRequest: streamPayload,
			})
			if streamErr != nil {
				t.Fatalf("start live stream: %v", streamErr)
			}
			chunks := 0
			for chunk := range stream.Chunks {
				if chunk.Err != nil {
					t.Fatalf("read live stream: %v", chunk.Err)
				}
				if len(chunk.Payload) > 0 {
					chunks++
				}
			}
			if chunks == 0 {
				t.Fatal("live stream returned no chunks")
			}
		})
	}

	for _, request := range []struct {
		name   string
		format string
		body   map[string]any
		field  string
	}{
		{name: "Gemini", format: "gemini", body: payloads[0].body, field: "totalTokens"},
		{name: "Anthropic", format: "claude", body: payloads[1].body, field: "input_tokens"},
		{name: "OpenAI Responses", format: "openai-response", body: payloads[3].body, field: "input_tokens"},
	} {
		t.Run(request.name+" CountTokens", func(t *testing.T) {
			payload, marshalErr := json.Marshal(request.body)
			if marshalErr != nil {
				t.Fatalf("build live CountTokens request: %v", marshalErr)
			}
			response, countErr := executor.CountTokensCanonical(ctx, "live-antigravity-contract", credential, ExecuteRequest{
				Model: model, Format: request.format, Payload: payload, OriginalRequest: payload,
			})
			if countErr != nil {
				t.Fatalf("count live Antigravity tokens: %v", countErr)
			}
			var count map[string]any
			if json.Unmarshal(response.Payload, &count) != nil {
				t.Fatal("live CountTokens result is not JSON")
			}
			value, ok := count[request.field].(float64)
			if !ok || value <= 0 {
				t.Fatalf("live CountTokens result has no positive %s", request.field)
			}
		})
	}
}

func selectLiveAntigravityModel(models []AntigravityModel) string {
	for _, preferred := range []string{"gemini-3-flash", "gemini-3.1-flash", "gemini-3-pro-high"} {
		for _, model := range models {
			if model.ID == preferred {
				return model.ID
			}
		}
	}
	for _, model := range models {
		if !strings.Contains(strings.ToLower(model.ID), "image") {
			return model.ID
		}
	}
	return models[0].ID
}

// TestLiveGrokContract is opt-in because it consumes a real Grok subscription.
// It uses a prepared canonical credential and never refreshes or logs it.
func TestLiveGrokContract(t *testing.T) {
	credentialFile := strings.TrimSpace(os.Getenv("CPA_LIVE_GROK_CREDENTIAL_FILE"))
	model := strings.TrimSpace(os.Getenv("CPA_LIVE_GROK_MODEL"))
	if credentialFile == "" {
		t.Skip("CPA_LIVE_GROK_CREDENTIAL_FILE is not set")
	}
	raw, err := os.ReadFile(credentialFile)
	if err != nil {
		t.Fatalf("read live Grok credential file: %v", err)
	}
	credential, err := ParseGrokCredentialJSON(raw)
	clear(raw)
	if err != nil {
		t.Fatalf("parse live Grok credential: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	models, err := DiscoverGrokModels(ctx, credential, GrokOptions{})
	if err != nil {
		t.Fatalf("discover live Grok models: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("live Grok model list is empty")
	}
	observation, err := ObserveGrokAccount(ctx, credential, GrokOptions{})
	if err != nil {
		t.Fatalf("observe live Grok account: %v", err)
	}
	if !observation.AccountQuotaObserved && !observation.SurfaceQuotaObserved && !observation.CreditQuotaObserved {
		t.Fatal("live Grok observation returned no quota or billing scope")
	}
	if model == "" {
		model = models[0]
	}
	payloads := []struct {
		name   string
		format string
		body   map[string]any
	}{
		{name: "OpenAI Responses", format: "openai-response", body: map[string]any{
			"model": model, "input": "Reply exactly OK.", "store": false,
		}},
		{name: "OpenAI Chat Completions", format: "openai", body: map[string]any{
			"model": model, "messages": []map[string]any{{"role": "user", "content": "Reply exactly OK."}},
		}},
		{name: "Anthropic Messages", format: "claude", body: map[string]any{
			"model": model, "max_tokens": 16,
			"messages": []map[string]any{{"role": "user", "content": "Reply exactly OK."}},
		}},
		{name: "Gemini GenerateContent", format: "gemini", body: map[string]any{
			"contents": []map[string]any{{"role": "user", "parts": []map[string]any{{"text": "Reply exactly OK."}}}},
		}},
	}
	executor := NewGrokHTTPExecutor()
	for _, request := range payloads {
		payload, marshalErr := json.Marshal(request.body)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		t.Run(request.name+" unary", func(t *testing.T) {
			response, executeErr := executor.ExecuteCanonical(ctx, "live-grok-contract", credential, ExecuteRequest{
				Model: model, Format: request.format, Payload: payload, OriginalRequest: payload,
			})
			if executeErr != nil {
				t.Fatalf("execute live request: %v", executeErr)
			}
			if !json.Valid(response.Payload) {
				t.Fatal("live response is not JSON")
			}
		})
		t.Run(request.name+" stream", func(t *testing.T) {
			streamBody := make(map[string]any, len(request.body)+1)
			for key, value := range request.body {
				streamBody[key] = value
			}
			streamBody["stream"] = true
			streamPayload, marshalErr := json.Marshal(streamBody)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			stream, streamErr := executor.ExecuteStreamCanonical(ctx, "live-grok-contract", credential, ExecuteRequest{
				Model: model, Format: request.format, Payload: streamPayload, OriginalRequest: streamPayload,
			})
			if streamErr != nil {
				t.Fatalf("start live stream: %v", streamErr)
			}
			chunks := 0
			for chunk := range stream.Chunks {
				if chunk.Err != nil {
					t.Fatalf("read live stream: %v", chunk.Err)
				}
				if len(chunk.Payload) > 0 {
					chunks++
				}
			}
			if chunks == 0 {
				t.Fatal("live stream returned no chunks")
			}
		})
	}
	for _, request := range []struct {
		format string
		body   map[string]any
	}{
		{format: "openai-response", body: payloads[0].body},
		{format: "claude", body: payloads[2].body},
		{format: "gemini", body: payloads[3].body},
	} {
		payload, marshalErr := json.Marshal(request.body)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		response, countErr := executor.CountTokensCanonical(ctx, ExecuteRequest{
			Model: model, Format: request.format, Payload: payload, OriginalRequest: payload,
		})
		if countErr != nil || !json.Valid(response.Payload) {
			t.Fatalf("count live Grok tokens: %v / %s", countErr, response.Payload)
		}
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
