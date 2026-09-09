package cpa

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/dialect"
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

func TestAntigravityImagesRejectsUnsupportedInputs(t *testing.T) {
	tests := []string{
		`{}`, `{"prompt":""}`, `{"prompt":"   "}`, `{"prompt":5}`,
		`{"prompt":"draw","n":2}`, `{"prompt":"draw","n":1.5}`, `{"prompt":"draw","n":null}`,
		`{"prompt":"draw","stream":true}`, `{"prompt":"draw","stream":"false"}`,
		`{"prompt":"draw","response_format":"url"}`, `{"prompt":"draw","size":"1024x1024"}`,
		`{"prompt":"draw","quality":"high"}`, `{"prompt":"draw","image":"private data"}`,
		`{"prompt":"draw","unknown_option":true}`, `null`, `[]`,
	}
	bridge := &antigravityProviderBridge{}
	for _, payload := range tests {
		t.Run(payload, func(t *testing.T) {
			if err := bridge.ValidateRequest(antigravityImagesRequest(payload)); err == nil {
				t.Fatal("unsupported Images request was accepted")
			}
		})
	}
	if err := bridge.ValidateRequest(antigravityImagesRequest(`{"prompt":"draw"}`)); err != nil {
		t.Fatalf("default request rejected: %v", err)
	}
	request := antigravityImagesRequest(`{"prompt":"draw"}`)
	request.RequestPath = "/v1/images/edits"
	if err := bridge.ValidateRequest(request); err == nil {
		t.Fatal("image edits were accepted")
	}
}

func TestAntigravityImagesRejectsInvalidResponses(t *testing.T) {
	for name, payload := range map[string]string{
		"invalid JSON": "not JSON private response",
		"empty":        `{}`,
		"blocked":      `{"promptFeedback":{"blockReason":"SAFETY"}}`,
		"text only":    `{"candidates":[{"content":{"parts":[{"text":"private response"}]}}]}`,
		"empty image":  `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":""}}]}}]}`,
		"bad base64":   `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"!private response!"}}]}}]}`,
		"wrong MIME":   `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"text/plain","data":"AA=="}}]}}]}`,
		"two images":   `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"AA=="}},{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}}]}`,
		"thought only": `{"candidates":[{"content":{"parts":[{"thought":true,"inlineData":{"mimeType":"image/png","data":"AA=="}}]}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			bridge := &antigravityProviderBridge{executor: &recordingAntigravityExecutor{response: []byte(payload)}}
			_, err := bridge.Execute(t.Context(), "17", antigravityProviderTestCredential(), antigravityImagesRequest(`{"prompt":"draw"}`))
			if err == nil {
				t.Fatal("invalid image output was accepted")
			}
			result := unaryExecutionError(t.Context(), bridge, err, antigravityProviderTestCredential())
			normalizeCPAImagesAttemptResult(execution.AttemptSpec{ClientProtocol: protocol.OpenAIImages}, &result)
			if result.Error == nil || result.StatusCode != http.StatusBadGateway ||
				result.DispatchState != execution.DispatchMaybeSent || result.Error.ReplaySafety != execution.ReplaySafetyUnknown ||
				strings.Contains(err.Error(), "private response") || strings.Contains(result.Error.Summary, "private response") {
				t.Fatalf("invalid image error = %+v / %v", result, err)
			}
		})
	}
}

func TestAntigravityImagesPreservesGeminiUsageStates(t *testing.T) {
	for name, metadata := range map[string]string{
		"missing":         "",
		"empty":           `{}`,
		"partial":         `{"promptTokenCount":10}`,
		"invalid":         `{"promptTokenCount":"invalid","candidatesTokenCount":20}`,
		"negative cached": `{"promptTokenCount":10,"cachedContentTokenCount":20,"candidatesTokenCount":5}`,
		"missing invalid": `{"promptTokenCount":"invalid","candidatesTokenCount":"invalid"}`,
	} {
		t.Run(name, func(t *testing.T) {
			upstream := antigravityImagesResponse(t, metadata)
			bridge := &antigravityProviderBridge{executor: &recordingAntigravityExecutor{response: upstream}}
			response, err := bridge.Execute(t.Context(), "17", antigravityProviderTestCredential(), antigravityImagesRequest(`{"prompt":"draw"}`))
			if err != nil {
				t.Fatal(err)
			}
			want, err := dialect.NewGemini().ExtractUsage(upstream)
			if err != nil {
				t.Fatal(err)
			}
			result := antigravityImagesAttempt(response, bridge)
			if result.Usage == nil || !reflect.DeepEqual(result.Usage.Normalized, want) {
				t.Fatalf("Images canonical usage = %+v, want %+v", result.Usage, want)
			}
		})
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
