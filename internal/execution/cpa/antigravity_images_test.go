package cpa

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

const antigravityTestImage = "aW1hZ2U="

func antigravityImagesRequest(payload string) providerRequest {
	return providerRequest{
		AttemptID: "images-attempt", Model: "gemini-3.1-flash-image", Format: "openai-image",
		RequestPath: "/v1/images/generations", Payload: []byte(payload), OriginalRequest: []byte(payload),
		Headers: http.Header{"Content-Type": {"application/json"}},
		BaseURL: "https://antigravity.example.test", ProxyURL: "http://proxy.example.test",
	}
}

func antigravityImagesResponse(t *testing.T, metadata string) []byte {
	t.Helper()
	root := map[string]any{
		"modelVersion": "gemini-3.1-flash-image",
		"candidates": []any{map[string]any{
			"finishReason": "STOP",
			"content": map[string]any{"parts": []any{
				map[string]any{"text": "private generated text"},
				map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": antigravityTestImage}},
			}},
		}},
	}
	if metadata != "" {
		root["usageMetadata"] = json.RawMessage(metadata)
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func antigravityImagesAttempt(response providerResponse, bridge *antigravityProviderBridge) execution.AttemptResult {
	spec := execution.AttemptSpec{
		ClientProtocol: protocol.OpenAIImages, Operation: execution.OperationImagesGenerate,
		RouteMode: execution.RouteConverted, UpstreamModel: "gemini-3.1-flash-image",
	}
	result := unaryProviderSuccess(bridge, spec, response)
	normalizeCPAImagesAttemptResult(spec, &result)
	return result
}

func TestAntigravityImagesConvertsGenerationAndPreservesUsage(t *testing.T) {
	executor := &recordingAntigravityExecutor{response: antigravityImagesResponse(t,
		`{"promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":20,"thoughtsTokenCount":5,"totalTokenCount":125}`,
	)}
	bridge := &antigravityProviderBridge{executor: executor}
	request := antigravityImagesRequest(`{"model":"gemini-3.1-flash-image","prompt":"  画一只猫  ","n":1,"stream":false,"size":"auto","quality":"auto","response_format":"b64_json"}`)
	original := bytes.Clone(request.Payload)
	before := time.Now().Unix()
	response, err := bridge.Execute(t.Context(), "17", antigravityProviderTestCredential(), request)
	if err != nil {
		t.Fatal(err)
	}
	wantPayload := `{"contents":[{"role":"user","parts":[{"text":"  画一只猫  "}]}],"generationConfig":{"responseModalities":["TEXT","IMAGE"]}}`
	var got, want any
	if err := json.Unmarshal(executor.request.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(wantPayload), &want); err != nil {
		t.Fatal(err)
	}
	if executor.request.Format != "gemini" || !reflect.DeepEqual(got, want) ||
		executor.request.Model != request.Model || executor.request.AttemptID != request.AttemptID ||
		executor.request.BaseURL != request.BaseURL || executor.request.ProxyURL != request.ProxyURL ||
		!bytes.Equal(executor.request.OriginalRequest, executor.request.Payload) {
		t.Fatalf("Gemini request = %#v, payload = %s", executor.request, executor.request.Payload)
	}
	if !bytes.Equal(request.Payload, original) || !bytes.Equal(request.OriginalRequest, original) {
		t.Fatal("conversion mutated the caller's request")
	}
	var body struct {
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Data    []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
		Usage struct {
			Input  int64 `json:"input_tokens"`
			Output int64 `json:"output_tokens"`
			Total  int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(response.Payload, &body); err != nil {
		t.Fatal(err)
	}
	if body.Created < before || body.Created > time.Now().Unix() || body.Model != request.Model ||
		len(body.Data) != 1 || body.Data[0].Base64 != antigravityTestImage ||
		body.Usage.Input != 100 || body.Usage.Output != 25 || body.Usage.Total != 125 ||
		strings.Contains(string(response.Payload), "private generated text") {
		t.Fatalf("Images response = %s", response.Payload)
	}
	result := antigravityImagesAttempt(response, bridge)
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	if result.Usage == nil || result.Usage.Normalized.State != usage.StateComplete ||
		result.Usage.Normalized.Tokens != (usage.Tokens{UncachedInput: 60, CacheRead: 40, Output: 25}) {
		t.Fatalf("canonical usage = %+v", result.Usage)
	}
}

func TestAntigravityImagesRejectsEditingRoute(t *testing.T) {
	request := antigravityImagesRequest(`{"prompt":"draw"}`)
	request.RequestPath = "/v1/images/edits"
	if err := (&antigravityProviderBridge{}).ValidateRequest(request); err == nil {
		t.Fatal("image edits were accepted")
	}
}

func TestAntigravityImagesAcceptsSnakeCaseImageAndPreservesUpstreamError(t *testing.T) {
	executor := &recordingAntigravityExecutor{response: []byte(`{"candidates":[{"content":{"parts":[{"inline_data":{"mime_type":"image/jpeg","data":"AA=="}}]}}]}`)}
	bridge := &antigravityProviderBridge{executor: executor}
	response, err := bridge.Execute(t.Context(), "17", antigravityProviderTestCredential(), antigravityImagesRequest(`{"prompt":"draw"}`))
	if err != nil || !bytes.Contains(response.Payload, []byte(`"b64_json":"AA=="`)) {
		t.Fatalf("snake case image = %s, error = %v", response.Payload, err)
	}
	upstreamError := antigravityClassifiedTestError{status: http.StatusTooManyRequests, code: "RESOURCE_EXHAUSTED"}
	executor.err = upstreamError
	_, err = bridge.Execute(t.Context(), "17", antigravityProviderTestCredential(), antigravityImagesRequest(`{"prompt":"draw"}`))
	if err != upstreamError {
		t.Fatalf("upstream error changed: %v", err)
	}
}
