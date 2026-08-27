package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type claudeRoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn claudeRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type unscopedClaudeExecutionError struct{}

func (unscopedClaudeExecutionError) Error() string   { return "unclassified rate limit" }
func (unscopedClaudeExecutionError) StatusCode() int { return http.StatusTooManyRequests }

func testClaudeExecutionCredential() ClaudeCredential {
	return ClaudeCredential{
		Type: ProviderClaude, AccessToken: "sk-ant-oat-access", RefreshToken: "refresh-secret",
		AccountUUID: "account-one", OrganizationUUID: "organization-one",
		DeviceIDs: []string{strings.Repeat("a", 64)}, Expire: "2030-01-01T00:00:00Z",
	}
}

const claudeExecutionSSE = "event: message_start\n" +
	"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-one\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
	"event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
	"event: content_block_stop\n" +
	"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: message_delta\n" +
	"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
	"event: message_stop\n" +
	"data: {\"type\":\"message_stop\"}\n\n"

func TestNewClaudeAuthUsesOAuthMetadataWithoutAPIKeyMode(t *testing.T) {
	credential := testClaudeExecutionCredential()
	auth := NewClaudeAuth("credential-one", credential, "https://example.test")
	if auth.ID != "credential-one" || auth.Provider != ProviderClaude ||
		auth.Attributes["api_key"] != "" || auth.Attributes["base_url"] != "https://example.test" ||
		auth.Metadata["access_token"] != credential.AccessToken ||
		auth.Metadata["account_uuid"] != credential.AccountUUID {
		t.Fatalf("Claude auth = %#v", auth)
	}
	deviceIDs, ok := auth.Metadata["claude_device_ids"].([]string)
	if !ok || len(deviceIDs) != 1 || deviceIDs[0] != credential.DeviceIDs[0] {
		t.Fatalf("Claude auth device IDs = %#v", auth.Metadata["claude_device_ids"])
	}
}

func TestClaudeHTTPExecutorSupportsNativeAndConvertedFormats(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		payload string
	}{
		{name: "anthropic", format: "claude", payload: `{"model":"claude-sonnet-4-5","max_tokens":32,"system":"system rule","messages":[{"role":"user","content":"hello"}]}`},
		{name: "openai chat", format: "openai", payload: `{"model":"claude-sonnet-4-5","messages":[{"role":"system","content":"system rule"},{"role":"user","content":"hello"}]}`},
		{name: "openai responses", format: "openai-response", payload: `{"model":"claude-sonnet-4-5","instructions":"system rule","input":"hello"}`},
		{name: "gemini", format: "gemini", payload: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"systemInstruction":{"parts":[{"text":"system rule"}]}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests int
			transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if request.URL.Host != "api.anthropic.com" || request.URL.Path != "/v1/messages" ||
					request.Header.Get("Authorization") != "Bearer sk-ant-oat-access" ||
					request.Header.Get("X-Api-Key") != "" {
					t.Fatalf("upstream request = %s headers=%v", request.URL, request.Header)
				}
				var upstream map[string]any
				if json.Unmarshal(body, &upstream) != nil || upstream["model"] != "claude-sonnet-4-5" || upstream["messages"] == nil {
					t.Fatalf("translated upstream body = %s", body)
				}
				responseBody := `{"id":"msg-one","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`
				contentType := "application/json"
				if test.format != "claude" {
					responseBody = claudeExecutionSSE
					contentType = "text/event-stream"
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {contentType}},
					Body:       io.NopCloser(strings.NewReader(responseBody)),
					Request:    request,
				}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			response, err := NewClaudeHTTPExecutor().ExecuteCanonical(ctx, "credential-one", testClaudeExecutionCredential(), ExecuteRequest{
				Model: "claude-sonnet-4-5", Payload: []byte(test.payload), Format: test.format,
				OriginalRequest: []byte(test.payload),
			})
			if err != nil {
				t.Fatal(err)
			}
			if requests != 1 || !json.Valid(response.Payload) || !strings.Contains(string(response.Payload), "ok") {
				t.Fatalf("requests/response = %d/%s", requests, response.Payload)
			}
		})
	}
}

func TestClaudeHTTPExecutorCountsTokensThroughAnthropicUpstream(t *testing.T) {
	for _, test := range []struct {
		format    string
		payload   string
		wantField string
	}{
		{
			format: "claude", wantField: `"input_tokens":7`,
			payload: `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			format: "openai-response", wantField: `"input_tokens":7`,
			payload: `{"model":"claude-sonnet-4-5","input":"hello"}`,
		},
		{
			format: "gemini", wantField: `"totalTokens":7`,
			payload: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
		},
	} {
		t.Run(test.format, func(t *testing.T) {
			transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if request.URL.Host != "api.anthropic.com" ||
					request.URL.Path != "/v1/messages/count_tokens" ||
					request.URL.Query().Get("beta") != "true" ||
					request.Header.Get("Authorization") != "Bearer sk-ant-oat-access" ||
					request.Header.Get("X-Api-Key") != "" {
					t.Fatalf("upstream request = %s headers=%v", request.URL, request.Header)
				}
				var upstream map[string]any
				if json.Unmarshal(body, &upstream) != nil ||
					upstream["model"] != "claude-sonnet-4-5" || upstream["messages"] == nil {
					t.Fatalf("translated upstream body = %s", body)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"input_tokens":7}`)),
					Request:    request,
				}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			executor := NewClaudeHTTPExecutor()
			counter, ok := executor.(interface {
				CountTokensCanonical(context.Context, string, ClaudeCredential, ExecuteRequest) (ExecuteResponse, error)
			})
			if !ok {
				t.Fatal("Claude HTTP executor does not expose upstream CountTokens")
			}
			response, err := counter.CountTokensCanonical(
				ctx,
				"credential-one",
				testClaudeExecutionCredential(),
				ExecuteRequest{
					Model: "claude-sonnet-4-5", Payload: []byte(test.payload), Format: test.format,
					OriginalRequest: []byte(test.payload),
				},
			)
			if err != nil || !strings.Contains(string(response.Payload), test.wantField) {
				t.Fatalf("CountTokensCanonical() = %s, %v", response.Payload, err)
			}
		})
	}
}

func TestClaudeHTTPExecutorStreamsAllSupportedFormats(t *testing.T) {
	tests := []struct {
		format  string
		payload string
	}{
		{format: "claude", payload: `{"model":"claude-sonnet-4-5","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`},
		{format: "openai", payload: `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}],"stream":true}`},
		{format: "openai-response", payload: `{"model":"claude-sonnet-4-5","input":"hello","stream":true}`},
		{format: "gemini", payload: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`},
	}
	for _, test := range tests {
		t.Run(test.format, func(t *testing.T) {
			transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(claudeExecutionSSE)),
					Request:    request,
				}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			stream, err := NewClaudeHTTPExecutor().ExecuteStreamCanonical(ctx, "credential-one", testClaudeExecutionCredential(), ExecuteRequest{
				Model: "claude-sonnet-4-5", Payload: []byte(test.payload), Format: test.format,
				OriginalRequest: []byte(test.payload),
			})
			if err != nil {
				t.Fatal(err)
			}
			var payload bytes.Buffer
			for chunk := range stream.Chunks {
				if chunk.Err != nil {
					t.Fatal(chunk.Err)
				}
				payload.Write(chunk.Payload)
			}
			if payload.Len() == 0 || !strings.Contains(payload.String(), "ok") {
				t.Fatalf("stream payload = %q", payload.String())
			}
		})
	}
}

func TestClaudeHTTPExecutorPreservesRichOpenAIChatSemantics(t *testing.T) {
	requestPayload := []byte(`{
		"model":"claude-sonnet-4-5",
		"max_tokens":16000,
		"reasoning_effort":"high",
		"messages":[
			{"role":"system","content":"system rule"},
			{"role":"user","content":[
				{"type":"text","text":"inspect this image"},
				{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}
			]}
		],
		"tools":[{"type":"function","function":{"name":"inspect_image","description":"inspect","parameters":{"type":"object","properties":{}}}}],
		"tool_choice":"auto"
	}`)
	const richSSE = "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-rich\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"sig\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"considering\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-one\",\"name\":\"inspect_image\",\"input\":{}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"detail\\\":\\\"ok\\\"}\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	var upstreamBody []byte
	transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		upstreamBody = append([]byte(nil), body...)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(richSSE)),
			Request:    request,
		}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	response, err := NewClaudeHTTPExecutor().ExecuteCanonical(ctx, "credential-one", testClaudeExecutionCredential(), ExecuteRequest{
		Model: "claude-sonnet-4-5", Payload: requestPayload, Format: "openai", OriginalRequest: requestPayload,
	})
	if err != nil {
		t.Fatal(err)
	}

	var upstream struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
		Messages []struct {
			Content []struct {
				Type   string `json:"type"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Thinking struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens"`
		} `json:"thinking"`
	}
	if err := json.Unmarshal(upstreamBody, &upstream); err != nil {
		t.Fatalf("decode upstream request: %v; body=%s", err, upstreamBody)
	}
	var imageSource struct {
		Type      string
		MediaType string
		Data      string
	}
	for _, message := range upstream.Messages {
		for _, content := range message.Content {
			if content.Type == "image" {
				imageSource.Type = content.Source.Type
				imageSource.MediaType = content.Source.MediaType
				imageSource.Data = content.Source.Data
			}
		}
	}
	if !strings.Contains(string(upstreamBody), "system rule") ||
		imageSource.Type != "base64" || imageSource.MediaType != "image/png" || imageSource.Data != "AAAA" ||
		len(upstream.Tools) != 1 || !strings.HasSuffix(upstream.Tools[0].Name, "inspect_image") ||
		upstream.Thinking.Type != "enabled" || upstream.Thinking.BudgetTokens <= 0 {
		t.Fatalf("rich upstream request = %s", upstreamBody)
	}

	var downstream struct {
		Choices []struct {
			Message struct {
				ReasoningContent string          `json:"reasoning_content"`
				LegacyReasoning  json.RawMessage `json:"reasoning"`
				ToolCalls        []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(response.Payload, &downstream); err != nil {
		t.Fatalf("decode downstream response: %v; body=%s", err, response.Payload)
	}
	if len(downstream.Choices) != 1 || downstream.Choices[0].Message.ReasoningContent != "considering" ||
		len(downstream.Choices[0].Message.LegacyReasoning) != 0 ||
		len(downstream.Choices[0].Message.ToolCalls) != 1 ||
		downstream.Choices[0].Message.ToolCalls[0].Function.Name != "inspect_image" ||
		downstream.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"detail":"ok"}` ||
		downstream.Choices[0].FinishReason != "tool_calls" ||
		downstream.Usage.PromptTokens != 2 || downstream.Usage.CompletionTokens != 3 ||
		response.AppliedReasoningEffort != "high" {
		t.Fatalf("rich downstream response = %s; applied=%q", response.Payload, response.AppliedReasoningEffort)
	}
}

func TestClaudeHTTPExecutorHonorsRequestCancellation(t *testing.T) {
	started := make(chan struct{})
	var requests atomic.Int32
	transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	base := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	ctx, cancel := context.WithCancel(base)
	done := make(chan error, 1)
	go func() {
		_, err := NewClaudeHTTPExecutor().ExecuteCanonical(ctx, "credential-one", testClaudeExecutionCredential(), ExecuteRequest{
			Model: "claude-sonnet-4-5", Format: "claude",
			Payload: []byte(`{"model":"claude-sonnet-4-5","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`),
		})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteCanonical() error = %v, want context canceled", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("upstream requests = %d, want 1", requests.Load())
	}
}

func TestClaudeHTTPExecutorSanitizesAndClassifiesFastModeEntitlement429(t *testing.T) {
	const providerSecret = "provider-secret-body"
	var requests atomic.Int32
	transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": {"application/json"}, "Retry-After": {"17"}},
			Body: io.NopCloser(strings.NewReader(
				`{"type":"error","error":{"type":"rate_limit_error","code":"fast_mode_credits","message":"Usage credits are required for fast mode. ` + providerSecret + `"}}`,
			)),
			Request: request,
		}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	_, err := NewClaudeHTTPExecutor().ExecuteCanonical(ctx, "credential-one", testClaudeExecutionCredential(), ExecuteRequest{
		Model: "claude-opus-5", Format: "claude",
		Payload: []byte(`{"model":"claude-opus-5","max_tokens":16,"speed":"fast","messages":[{"role":"user","content":"hello"}]}`),
	})
	var executionErr *ClaudeExecutionError
	if !errors.As(err, &executionErr) || executionErr.StatusCode() != http.StatusTooManyRequests ||
		!executionErr.IsRequestScoped() || executionErr.ErrorType() != "rate_limit_error" ||
		executionErr.ErrorCode() != "fast_mode_credits" || executionErr.RetryAfter() == nil ||
		*executionErr.RetryAfter() < 17*time.Second || *executionErr.RetryAfter() > 2*time.Minute ||
		strings.Contains(err.Error(), providerSecret) || requests.Load() != 1 {
		t.Fatalf("Claude execution error = %#v / %v", executionErr, err)
	}
}

func TestClaudeHTTPExecutorPreservesCredentialScopeForRateLimits(t *testing.T) {
	for _, test := range []struct {
		name             string
		headers          http.Header
		credentialScoped bool
	}{
		{
			name: "unified account limit",
			headers: http.Header{
				"Content-Type":                       {"application/json"},
				"Anthropic-Ratelimit-Unified-Status": {"rejected"},
			},
			credentialScoped: true,
		},
		{
			name: "ordinary model limit",
			headers: http.Header{
				"Content-Type": {"application/json"},
			},
			credentialScoped: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     test.headers.Clone(),
					Body: io.NopCloser(strings.NewReader(
						`{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`,
					)),
					Request: request,
				}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			_, err := NewClaudeHTTPExecutor().ExecuteCanonical(ctx, "credential-one", testClaudeExecutionCredential(), ExecuteRequest{
				Model: "claude-sonnet-4-5", Format: "claude",
				Payload: []byte(`{"model":"claude-sonnet-4-5","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`),
			})
			var scoped interface{ IsCredentialScoped() bool }
			if !errors.As(err, &scoped) || scoped == nil || scoped.IsCredentialScoped() != test.credentialScoped {
				t.Fatalf("credential scope = %#v / %v; want %t", scoped, err, test.credentialScoped)
			}
		})
	}
}

func TestClaudeHTTPExecutorPreservesUnifiedRateLimitResetBeyondOneHour(t *testing.T) {
	resetAt := time.Now().Add(5 * time.Hour).Unix()
	transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header: http.Header{
				"Content-Type":                          {"application/json"},
				"Anthropic-Ratelimit-Unified-Status":    {"rejected"},
				"Anthropic-Ratelimit-Unified-5h-Status": {"rejected"},
				"Anthropic-Ratelimit-Unified-5h-Reset":  {strconv.FormatInt(resetAt, 10)},
			},
			Body: io.NopCloser(strings.NewReader(
				`{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`,
			)),
			Request: request,
		}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	_, err := NewClaudeHTTPExecutor().ExecuteCanonical(ctx, "credential-one", testClaudeExecutionCredential(), ExecuteRequest{
		Model: "claude-sonnet-4-5", Format: "claude",
		Payload: []byte(`{"model":"claude-sonnet-4-5","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`),
	})
	var retry interface{ RetryAfter() *time.Duration }
	if !errors.As(err, &retry) || retry == nil || retry.RetryAfter() == nil ||
		*retry.RetryAfter() < 4*time.Hour || *retry.RetryAfter() > 6*time.Hour {
		t.Fatalf("retry after = %#v / %v", retry, err)
	}
}

func TestNormalizeClaudeExecutionErrorDoesNotInventCredentialScope(t *testing.T) {
	err := normalizeClaudeExecutionError(unscopedClaudeExecutionError{})
	var scoped interface{ IsCredentialScoped() bool }
	if errors.As(err, &scoped) {
		t.Fatalf("unclassified error gained credential scope: %#v", err)
	}
}

func TestParseClaudeCredentialJSONCreatesAndPreservesOneDeviceIdentity(t *testing.T) {
	raw := []byte(`{
		"type":"claude",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_uuid":"account-one",
		"email":"owner@example.com",
		"expired":"2026-08-16T09:00:00Z"
	}`)
	credential, err := ParseClaudeCredentialJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Type != ProviderClaude || credential.AccountUUID != "account-one" ||
		len(credential.DeviceIDs) != 1 || len(credential.DeviceIDs[0]) != 64 ||
		credential.DeviceIDs[0] != strings.ToLower(credential.DeviceIDs[0]) {
		t.Fatalf("credential = %#v", credential)
	}
	canonical, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := ParseClaudeCredentialJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(reparsed.DeviceIDs) != 1 || reparsed.DeviceIDs[0] != credential.DeviceIDs[0] {
		t.Fatalf("device identity changed: %q -> %q", credential.DeviceIDs, reparsed.DeviceIDs)
	}
	expiresAt, known := ClaudeCredentialExpiresAt(reparsed)
	if !known || !expiresAt.Equal(time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("expires = %s, %t", expiresAt, known)
	}
}

func TestParseClaudeCredentialJSONRequiresExpiredTimestamp(t *testing.T) {
	for _, raw := range []string{
		`{"type":"claude","access_token":"access","refresh_token":"refresh","account_uuid":"account"}`,
		`{"type":"claude","access_token":"access","refresh_token":"refresh","account_uuid":"account","expired":""}`,
	} {
		if _, err := ParseClaudeCredentialJSON([]byte(raw)); err == nil {
			t.Fatalf("ParseClaudeCredentialJSON() accepted missing expired: %s", raw)
		}
	}
	if _, err := ParseClaudeCredentialJSON([]byte(
		`{"type":"claude","access_token":"access","refresh_token":"refresh","account_uuid":"account","expired":"2026-08-16T07:00:00Z"}`,
	)); err != nil {
		t.Fatalf("ParseClaudeCredentialJSON() rejected an expired but refreshable credential: %v", err)
	}
}

func TestParseClaudeCredentialJSONRejectsConfigurationInjection(t *testing.T) {
	for _, field := range []string{"proxy_url", "headers", "retry", "cooldown", "selector", "weight", "file_path"} {
		raw := []byte(`{"type":"claude","access_token":"access","refresh_token":"refresh","account_uuid":"account","` + field + `":"injected"}`)
		if _, err := ParseClaudeCredentialJSON(raw); err == nil {
			t.Fatalf("ParseClaudeCredentialJSON() accepted %q", field)
		}
	}
}

func TestParseClaudeCredentialJSONDiscardsCPAControlMetadata(t *testing.T) {
	raw := []byte(`{
		"type":"claude",
		"access_token":"access",
		"refresh_token":"refresh",
		"account_uuid":"account",
		"expired":"2030-01-01T00:00:00Z",
		"disabled":false,
		"prefix":"team-a",
		"note":"source note",
		"websockets":false
	}`)
	credential, err := ParseClaudeCredentialJSON(raw)
	if err != nil {
		t.Fatalf("ParseClaudeCredentialJSON() error = %v", err)
	}
	canonical, err := json.Marshal(credential)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, field := range []string{"disabled", "prefix", "note", "websockets"} {
		if strings.Contains(string(canonical), field) {
			t.Fatalf("canonical credential retained CPA control %q: %s", field, canonical)
		}
	}
}

func TestParseClaudeCredentialJSONRejectsMalformedCPAControlMetadata(t *testing.T) {
	raw := []byte(`{
		"type":"claude",
		"access_token":"access",
		"refresh_token":"refresh",
		"account_uuid":"account",
		"expired":"2030-01-01T00:00:00Z",
		"disabled":"false"
	}`)
	if _, err := ParseClaudeCredentialJSON(raw); err == nil {
		t.Fatal("ParseClaudeCredentialJSON() accepted malformed CPA disabled metadata")
	}
}

func TestParseClaudeCredentialJSONRejectsDuplicateSecretFields(t *testing.T) {
	raw := []byte(`{
		"type":"claude",
		"access_token":"first-access",
		"access_token":"second-access",
		"refresh_token":"refresh",
		"account_uuid":"account"
	}`)
	if _, err := ParseClaudeCredentialJSON(raw); err == nil {
		t.Fatal("ParseClaudeCredentialJSON() accepted a duplicate access_token")
	}
}

func TestBeginClaudeBrowserAuthorizationUsesFixedPKCECallback(t *testing.T) {
	authorization, err := BeginClaudeBrowserAuthorization()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Scheme != "https" || authorization.State == "" || authorization.CodeVerifier == "" ||
		authorization.CodeChallenge == "" || query.Get("state") != authorization.State ||
		query.Get("redirect_uri") != ClaudeRedirectURI || query.Get("code_challenge") != authorization.CodeChallenge ||
		query.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization = %#v URL=%s", authorization, authorization.AuthorizationURL)
	}
}

func TestCompleteClaudeBrowserAuthorizationReturnsCanonicalCredential(t *testing.T) {
	fixedNow := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["grant_type"] != "authorization_code" || payload["code"] != "authorization-code" ||
				payload["state"] != "state-one" || payload["redirect_uri"] != ClaudeRedirectURI ||
				payload["code_verifier"] != "verifier-one" {
				t.Errorf("token payload = %#v", payload)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"access_token":"access-one","refresh_token":"refresh-one","expires_in":3600,"account":{"uuid":"account-token","email_address":"token@example.com"},"organization":{"uuid":"org-one","name":"Organization"}}`))
		case "/profile":
			if request.Header.Get("Authorization") != "Bearer access-one" {
				t.Errorf("profile authorization = %q", request.Header.Get("Authorization"))
			}
			_, _ = writer.Write([]byte(`{"account":{"uuid":"account-profile","email":"profile@example.com"},"organization":{"uuid":"org-one","name":"Organization"}}`))
		case "/roles":
			_, _ = writer.Write([]byte(`[]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	credential, err := CompleteClaudeBrowserAuthorization(t.Context(), BrowserAuthorizationCompletion{
		ExpectedState: "state-one", ReturnedState: "state-one", Code: "authorization-code", CodeVerifier: "verifier-one",
	}, ClaudeOptions{
		TokenURL: server.URL + "/token", ProfileURL: server.URL + "/profile", RolesURL: server.URL + "/roles",
		HTTPClient: server.Client(), Now: func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	if credential.Type != ProviderClaude || credential.AccessToken != "access-one" || credential.RefreshToken != "refresh-one" ||
		credential.AccountUUID != "account-profile" || credential.Email != "profile@example.com" ||
		credential.OrganizationUUID != "org-one" || len(credential.DeviceIDs) != 1 ||
		credential.LastRefresh != fixedNow.Format(time.RFC3339) ||
		credential.Expire != fixedNow.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestClaudeTokenEndpointErrorDoesNotExposeResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"invalid_grant","message":"provider-secret-body"}`))
	}))
	defer server.Close()

	_, err := CompleteClaudeBrowserAuthorization(t.Context(), BrowserAuthorizationCompletion{
		ExpectedState: "state-one", ReturnedState: "state-one", Code: "authorization-code", CodeVerifier: "verifier-one",
	}, ClaudeOptions{TokenURL: server.URL, HTTPClient: server.Client()})
	var tokenErr *TokenEndpointError
	if !errors.As(err, &tokenErr) || tokenErr.Code != "invalid_grant" || strings.Contains(err.Error(), "provider-secret-body") {
		t.Fatalf("token error = %#v, %v", tokenErr, err)
	}
}

func TestRefreshClaudeCredentialOncePreservesIdentityAndDevice(t *testing.T) {
	deviceID := strings.Repeat("a", 64)
	current := ClaudeCredential{
		Type: ProviderClaude, AccessToken: "old-access", RefreshToken: "old-refresh",
		AccountUUID: "account-one", OrganizationUUID: "org-one", OrganizationName: "Old Organization",
		Email: "owner@example.com", DeviceIDs: []string{deviceID}, Expire: "2030-01-01T00:00:00Z",
	}
	var tokenRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			tokenRequests.Add(1)
			_, _ = writer.Write([]byte(`{"access_token":"new-access","refresh_token":"rotated-refresh","expires_in":1200}`))
		case "/profile":
			writer.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	fixedNow := time.Date(2026, 8, 16, 8, 30, 0, 0, time.UTC)

	refreshed, err := RefreshClaudeCredentialOnce(t.Context(), current, ClaudeOptions{
		TokenURL: server.URL + "/token", ProfileURL: server.URL + "/profile",
		HTTPClient: server.Client(), Now: func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	if tokenRequests.Load() != 1 || refreshed.AccessToken != "new-access" || refreshed.RefreshToken != "rotated-refresh" ||
		refreshed.AccountUUID != current.AccountUUID || refreshed.OrganizationUUID != current.OrganizationUUID ||
		refreshed.Email != current.Email || len(refreshed.DeviceIDs) != 1 || refreshed.DeviceIDs[0] != deviceID ||
		refreshed.LastRefresh != fixedNow.Format(time.RFC3339) {
		t.Fatalf("refreshed = %#v, token requests = %d", refreshed, tokenRequests.Load())
	}
}

func TestRefreshClaudeCredentialOnceRejectsIdentityChange(t *testing.T) {
	current := ClaudeCredential{
		Type: ProviderClaude, AccessToken: "old-access", RefreshToken: "old-refresh",
		AccountUUID: "account-one", OrganizationUUID: "org-one", DeviceIDs: []string{strings.Repeat("b", 64)},
		Expire: "2030-01-01T00:00:00Z",
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			_, _ = writer.Write([]byte(`{"access_token":"new-access","expires_in":1200}`))
		case "/profile":
			_, _ = writer.Write([]byte(`{"account":{"uuid":"account-two","email":"other@example.com"},"organization":{"uuid":"org-one"}}`))
		}
	}))
	defer server.Close()

	_, err := RefreshClaudeCredentialOnce(t.Context(), current, ClaudeOptions{
		TokenURL: server.URL + "/token", ProfileURL: server.URL + "/profile", HTTPClient: server.Client(),
	})
	if !errors.Is(err, ErrClaudeCredentialIdentityChanged) {
		t.Fatalf("refresh identity error = %v", err)
	}
}

func TestRefreshClaudeCredentialOnceRejectsOrganizationChange(t *testing.T) {
	current := ClaudeCredential{
		Type: ProviderClaude, AccessToken: "old-access", RefreshToken: "old-refresh",
		AccountUUID: "account-one", OrganizationUUID: "org-one", DeviceIDs: []string{strings.Repeat("c", 64)},
		Expire: "2030-01-01T00:00:00Z",
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			_, _ = writer.Write([]byte(`{"access_token":"new-access","expires_in":1200}`))
		case "/profile":
			_, _ = writer.Write([]byte(`{"account":{"uuid":"account-one"},"organization":{"uuid":"org-two"}}`))
		}
	}))
	defer server.Close()

	_, err := RefreshClaudeCredentialOnce(t.Context(), current, ClaudeOptions{
		TokenURL: server.URL + "/token", ProfileURL: server.URL + "/profile", HTTPClient: server.Client(),
	})
	if !errors.Is(err, ErrClaudeOrganizationIdentityChanged) {
		t.Fatalf("refresh organization error = %v", err)
	}
}

func TestRefreshClaudeCredentialOnceAllowsOrganizationDiscoveryAndKeepsRefreshToken(t *testing.T) {
	current := ClaudeCredential{
		Type: ProviderClaude, AccessToken: "old-access", RefreshToken: "old-refresh",
		AccountUUID: "account-one", DeviceIDs: []string{strings.Repeat("d", 64)}, Expire: "2030-01-01T00:00:00Z",
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			_, _ = writer.Write([]byte(`{"access_token":"new-access","expires_in":1200}`))
		case "/profile":
			_, _ = writer.Write([]byte(`{"account":{"uuid":"account-one"},"organization":{"uuid":"org-discovered","name":"Organization"}}`))
		}
	}))
	defer server.Close()

	refreshed, err := RefreshClaudeCredentialOnce(t.Context(), current, ClaudeOptions{
		TokenURL: server.URL + "/token", ProfileURL: server.URL + "/profile", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.RefreshToken != "old-refresh" || refreshed.OrganizationUUID != "org-discovered" ||
		refreshed.DeviceIDs[0] != current.DeviceIDs[0] {
		t.Fatalf("refreshed = %#v", refreshed)
	}
}

func TestRefreshClaudeCredentialOnceHonorsCallerCancellation(t *testing.T) {
	current := ClaudeCredential{
		Type: ProviderClaude, AccessToken: "old-access", RefreshToken: "old-refresh",
		AccountUUID: "account-one", DeviceIDs: []string{strings.Repeat("e", 64)}, Expire: "2030-01-01T00:00:00Z",
	}
	client := &http.Client{Transport: claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	_, err := RefreshClaudeCredentialOnce(ctx, current, ClaudeOptions{TokenURL: "https://platform.claude.test/token", HTTPClient: client})
	if !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
		t.Fatalf("canceled refresh = %v after %s", err, time.Since(started))
	}
}

func TestDiscoverClaudeModelsMergesBootstrapEntitlementsWithRegistry(t *testing.T) {
	credential := testClaudeExecutionCredential()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/bootstrap" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer "+credential.AccessToken ||
			request.Header.Get("Anthropic-Beta") != "oauth-2025-04-20" ||
			!strings.HasPrefix(request.Header.Get("User-Agent"), "claude-code/") ||
			request.URL.Query().Get("entrypoint") == "" {
			t.Fatalf("bootstrap request = %s headers=%v", request.URL, request.Header)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"org_model_default":"claude-org-default",
			"additional_model_options":[
				{"model":"claude-fable-5","name":"Claude Fable 5","description":"account preview"},
				{"model":"claude-disabled-preview","name":"Disabled","description":"","disabled_reason":"not enabled"}
			],
			"model_access":[
				{"api_name":"claude-sonnet-4-5-20250929","entitled":false},
				{"api_name":"claude-disabled-preview","entitled":true},
				{"api_name":"claude-account-only","entitled":true}
			]
		}`))
	}))
	defer server.Close()

	models, err := DiscoverClaudeModels(t.Context(), credential, ClaudeOptions{
		BootstrapURL: server.URL + "/bootstrap",
		HTTPClient:   server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]ClaudeModel, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	for _, expected := range []string{"claude-opus-4-6", "claude-fable-5", "claude-account-only", "claude-org-default"} {
		if _, ok := byID[expected]; !ok {
			t.Fatalf("discovered models missing %q: %#v", expected, models)
		}
	}
	for _, denied := range []string{"claude-sonnet-4-5-20250929", "claude-disabled-preview"} {
		if _, ok := byID[denied]; ok {
			t.Fatalf("discovered models retained denied %q: %#v", denied, models)
		}
	}
}

func TestDecodeClaudeJSONObjectRejectsTrailingGarbage(t *testing.T) {
	var payload map[string]any
	if err := decodeClaudeJSONObject([]byte(`{} trailing`), &payload); err == nil {
		t.Fatal("decodeClaudeJSONObject() accepted trailing garbage")
	}
}

func TestObserveClaudeAccountCombinesProfileRolesBootstrapAndUsage(t *testing.T) {
	credential := testClaudeExecutionCredential()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+credential.AccessToken {
			t.Fatalf("%s authorization = %q", request.URL.Path, request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/profile":
			_, _ = writer.Write([]byte(`{
				"account":{"uuid":"account-one","email":"owner@example.com","display_name":"Owner","created_at":"2025-01-02T03:04:05Z"},
				"organization":{"uuid":"organization-one","name":"Example Org","organization_type":"claude_team","rate_limit_tier":"org-tier","seat_tier":"team_standard","has_extra_usage_enabled":true,"billing_type":"stripe_subscription","subscription_created_at":"2025-02-03T04:05:06Z"}
			}`))
		case "/roles":
			_, _ = writer.Write([]byte(`{"organization_role":"admin","workspace_role":"member","organization_name":"Example Org"}`))
		case "/bootstrap":
			if request.Header.Get("Anthropic-Beta") != claudeBootstrapBeta ||
				request.Header.Get("User-Agent") != claudeBootstrapUserAgent {
				t.Fatalf("bootstrap headers = %v", request.Header)
			}
			_, _ = writer.Write([]byte(`{"oauth_account":{"account_uuid":"account-one","account_email":"owner@example.com","organization_uuid":"organization-one","organization_name":"Example Org","organization_type":"claude_team","organization_rate_limit_tier":"org-bootstrap-tier","user_rate_limit_tier":"user-tier","seat_tier":"team_standard"}}`))
		case "/usage":
			if request.Header.Get("Anthropic-Beta") != "" ||
				request.Header.Get("User-Agent") != "axios/1.15.2" ||
				request.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("usage headers = %v", request.Header)
			}
			writer.Header().Set("Request-Id", "usage-request-one")
			_, _ = writer.Write([]byte(`{
				"five_hour":{"utilization":25,"resets_at":"2026-08-16T10:00:00Z"},
				"seven_day":{"utilization":40,"resets_at":"2026-08-23T08:00:00Z"},
				"seven_day_sonnet":{"utilization":50,"resets_at":"2026-08-23T08:00:00Z"},
				"extra_usage":{"is_enabled":true,"monthly_limit":100,"used_credits":12.5,"utilization":12.5,"currency":"USD"},
				"limits":[{"kind":"weekly","group":"opus","percent":80,"resets_at":"2026-08-23T08:00:00Z","scope":{"model":{"display_name":"Claude Opus"}}}]
			}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	observation, err := ObserveClaudeAccount(t.Context(), credential, ClaudeOptions{
		ProfileURL: server.URL + "/profile", RolesURL: server.URL + "/roles",
		BootstrapURL: server.URL + "/bootstrap", UsageURL: server.URL + "/usage",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := observation.Profile
	if profile.DisplayName != "Owner" || profile.Email != "owner@example.com" ||
		profile.OrganizationName != "Example Org" || profile.OrganizationRole != "admin" ||
		profile.WorkspaceRole != "member" || profile.OrganizationType != "claude_team" ||
		profile.UserRateLimitTier != "user-tier" ||
		profile.OrganizationRateLimitTier != "org-bootstrap-tier" ||
		profile.ExtraUsageEnabled == nil || !*profile.ExtraUsageEnabled {
		t.Fatalf("profile = %#v", profile)
	}
	if observation.Usage.FiveHour == nil || observation.Usage.FiveHour.Utilization == nil ||
		*observation.Usage.FiveHour.Utilization != 25 ||
		observation.Usage.ExtraUsage == nil || observation.Usage.ExtraUsage.MonthlyLimit == nil ||
		len(observation.Usage.Limits) != 1 || !observation.AccountObserved || !observation.UsageObserved ||
		observation.Header.Get("Request-Id") != "usage-request-one" {
		t.Fatalf("usage/header = %#v / %v", observation.Usage, observation.Header)
	}
}

func TestObserveClaudeAccountFailsWhenEverySourceIsUnavailable(t *testing.T) {
	credential := testClaudeExecutionCredential()
	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				calls++
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(status)
				_, _ = writer.Write([]byte(`{"error":{"type":"upstream_failure","message":"sensitive upstream detail"}}`))
			}))
			defer server.Close()

			_, err := ObserveClaudeAccount(t.Context(), credential, ClaudeOptions{
				ProfileURL: server.URL + "/profile", RolesURL: server.URL + "/roles",
				BootstrapURL: server.URL + "/bootstrap", UsageURL: server.URL + "/usage",
				HTTPClient: server.Client(),
			})
			if err == nil {
				t.Fatalf("observation error = %v", err)
			}
			if calls != 4 {
				t.Fatalf("observation calls = %d, want 4", calls)
			}
			var upstream *ClaudeUpstreamHTTPError
			if !errors.As(err, &upstream) || upstream.StatusCode != status {
				t.Fatalf("observation error = %v, want status %d", err, status)
			}
			if strings.Contains(err.Error(), "sensitive upstream detail") {
				t.Fatalf("observation error leaked upstream response: %v", err)
			}
		})
	}
}

func TestObserveClaudeAccountClassifiesMixedAuthorizationFailures(t *testing.T) {
	credential := testClaudeExecutionCredential()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		status := http.StatusForbidden
		if request.URL.Path == "/profile" || request.URL.Path == "/bootstrap" {
			status = http.StatusUnauthorized
		}
		writer.WriteHeader(status)
	}))
	defer server.Close()

	_, err := ObserveClaudeAccount(t.Context(), credential, ClaudeOptions{
		ProfileURL: server.URL + "/profile", RolesURL: server.URL + "/roles",
		BootstrapURL: server.URL + "/bootstrap", UsageURL: server.URL + "/usage",
		HTTPClient: server.Client(),
	})
	var upstream *ClaudeUpstreamHTTPError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusUnauthorized {
		t.Fatalf("observation error = %v", err)
	}
}

func TestObserveClaudeAccountRejectsSourceWithoutUsableData(t *testing.T) {
	credential := testClaudeExecutionCredential()
	tests := []struct {
		name    string
		path    string
		payload string
	}{
		{name: "empty roles", path: "/roles", payload: `[]`},
		{name: "empty bootstrap", path: "/bootstrap", payload: `{}`},
		{name: "empty usage", path: "/usage", payload: `{}`},
		{name: "empty usage window", path: "/usage", payload: `{"five_hour":{}}`},
		{name: "empty extra usage", path: "/usage", payload: `{"extra_usage":{}}`},
		{name: "currency-only extra usage", path: "/usage", payload: `{"extra_usage":{"currency":""}}`},
		{name: "limit without percent", path: "/usage", payload: `{"limits":[{"kind":"weekly","group":"opus"}]}`},
		{name: "unknown usage schema", path: "/usage", payload: `{"new_unknown_field":"value"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path == test.path {
					_, _ = writer.Write([]byte(test.payload))
					return
				}
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = writer.Write([]byte(`{"error":"temporarily unavailable"}`))
			}))
			defer server.Close()

			_, err := ObserveClaudeAccount(t.Context(), credential, ClaudeOptions{
				ProfileURL: server.URL + "/profile", RolesURL: server.URL + "/roles",
				BootstrapURL: server.URL + "/bootstrap", UsageURL: server.URL + "/usage",
				HTTPClient: server.Client(),
			})
			if !errors.Is(err, ErrClaudeAccountObservationUnavailable) {
				t.Fatalf("observation error = %v, want unavailable", err)
			}
		})
	}
}

func TestObserveClaudeAccountKeepsPartialProfileWhenCompanionEndpointsFail(t *testing.T) {
	credential := testClaudeExecutionCredential()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/profile":
			_, _ = writer.Write([]byte(`{
				"account":{"uuid":"account-one","email":"owner@example.com","display_name":"Owner"},
				"organization":{"uuid":"organization-one","name":"Example Org"}
			}`))
		case "/roles", "/usage":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":"temporarily unavailable"}`))
		case "/bootstrap":
			_, _ = writer.Write([]byte(`{"oauth_account":{"account_uuid":"account-one","organization_uuid":"organization-one","seat_tier":"team_standard"}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	observation, err := ObserveClaudeAccount(t.Context(), credential, ClaudeOptions{
		ProfileURL: server.URL + "/profile", RolesURL: server.URL + "/roles",
		BootstrapURL: server.URL + "/bootstrap", UsageURL: server.URL + "/usage",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Profile.DisplayName != "Owner" || observation.Profile.OrganizationName != "Example Org" ||
		observation.Profile.SeatTier != "team_standard" ||
		strings.Join(observation.IncompleteSources, ",") != "roles,usage" {
		t.Fatalf("partial observation = %#v", observation)
	}
}

func TestObserveClaudeAccountTreatsMissingProfileIdentityAsPartial(t *testing.T) {
	credential := testClaudeExecutionCredential()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/profile":
			_, _ = writer.Write([]byte(`{"account":{"email":"owner@example.com"},"organization":{"uuid":"organization-one"}}`))
		case "/roles":
			_, _ = writer.Write([]byte(`{"organization_role":"member"}`))
		case "/bootstrap":
			_, _ = writer.Write([]byte(`{"oauth_account":{"account_uuid":"account-one","organization_uuid":"organization-one"}}`))
		case "/usage":
			_, _ = writer.Write([]byte(`{"five_hour":{"utilization":10}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	observation, err := ObserveClaudeAccount(t.Context(), credential, ClaudeOptions{
		ProfileURL: server.URL + "/profile", RolesURL: server.URL + "/roles",
		BootstrapURL: server.URL + "/bootstrap", UsageURL: server.URL + "/usage",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Profile.AccountUUID != credential.AccountUUID ||
		strings.Join(observation.IncompleteSources, ",") != "profile" {
		t.Fatalf("missing identity observation = %#v", observation)
	}
}

func TestObserveClaudeAccountRejectsIdentityMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/profile":
			_, _ = writer.Write([]byte(`{"account":{"uuid":"different-account"},"organization":{"uuid":"organization-one"}}`))
		case "/roles":
			_, _ = writer.Write([]byte(`{}`))
		case "/bootstrap":
			_, _ = writer.Write([]byte(`{}`))
		case "/usage":
			_, _ = writer.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	_, err := ObserveClaudeAccount(t.Context(), testClaudeExecutionCredential(), ClaudeOptions{
		ProfileURL: server.URL + "/profile", RolesURL: server.URL + "/roles",
		BootstrapURL: server.URL + "/bootstrap", UsageURL: server.URL + "/usage",
		HTTPClient: server.Client(),
	})
	if !errors.Is(err, ErrClaudeCredentialIdentityChanged) {
		t.Fatalf("identity mismatch error = %v", err)
	}
}

func TestObserveClaudeAccountKeepsUsageWhenProfileIsUnavailable(t *testing.T) {
	credential := testClaudeExecutionCredential()
	credential.Email = "owner@example.com"
	credential.OrganizationName = "Stored Org"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/profile":
			http.Error(writer, "unavailable", http.StatusServiceUnavailable)
		case "/roles":
			_, _ = writer.Write([]byte(`{}`))
		case "/bootstrap":
			_, _ = writer.Write([]byte(`{"oauth_account":{"account_uuid":"account-one","organization_uuid":"organization-one","organization_name":"Bootstrap Org","seat_tier":"team_standard"}}`))
		case "/usage":
			_, _ = writer.Write([]byte(`{"five_hour":{"utilization":10,"resets_at":"2026-08-16T10:00:00Z"}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	observation, err := ObserveClaudeAccount(t.Context(), credential, ClaudeOptions{
		ProfileURL: server.URL + "/profile", RolesURL: server.URL + "/roles",
		BootstrapURL: server.URL + "/bootstrap", UsageURL: server.URL + "/usage",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Profile.Email != credential.Email ||
		observation.Profile.OrganizationName != "Bootstrap Org" ||
		observation.Profile.SeatTier != "team_standard" ||
		observation.Usage.FiveHour == nil || !observation.AccountObserved || !observation.UsageObserved {
		t.Fatalf("observation = %#v", observation)
	}
}
