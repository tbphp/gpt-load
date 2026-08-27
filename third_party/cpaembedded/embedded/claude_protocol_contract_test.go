package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const claudeWebSearchSSE = "event: message_start\n" +
	"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-search\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
	"event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"server_tool_use\",\"id\":\"srvtoolu_search\",\"name\":\"web_search\",\"input\":{}}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"GPT-Load\\\"}\"}}\n\n" +
	"event: content_block_stop\n" +
	"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"web_search_tool_result\",\"tool_use_id\":\"srvtoolu_search\",\"content\":[{\"type\":\"web_search_result\",\"title\":\"GPT-Load\",\"url\":\"https://example.com/gpt-load\",\"encrypted_content\":\"ENC_SEARCH\"}]}}\n\n" +
	"event: content_block_stop\n" +
	"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"citations_delta\",\"citation\":{\"type\":\"web_search_result_location\",\"cited_text\":\"GPT-Load\",\"url\":\"https://example.com/gpt-load\",\"title\":\"GPT-Load\",\"encrypted_index\":\"IDX_SEARCH\"}}}\n\n" +
	"event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"text_delta\",\"text\":\"GPT-Load source.\"}}\n\n" +
	"event: content_block_stop\n" +
	"data: {\"type\":\"content_block_stop\",\"index\":2}\n\n" +
	"event: message_delta\n" +
	"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n" +
	"event: message_stop\n" +
	"data: {\"type\":\"message_stop\"}\n\n"

func TestClaudeHTTPExecutorPreservesResponsesWebSearchCitationAndReplay(t *testing.T) {
	requestPayload := []byte(`{
		"model":"claude-sonnet-4-5",
		"tools":[{"type":"web_search","max_uses":1}],
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Find GPT-Load"}]}]
	}`)
	var upstreamBodies [][]byte
	transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		upstreamBodies = append(upstreamBodies, bytes.Clone(body))
		responseBody := claudeWebSearchSSE
		if len(upstreamBodies) > 1 {
			responseBody = claudeExecutionSSE
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(responseBody)),
			Request:    request,
		}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	executor := NewClaudeHTTPExecutor()
	response, err := executor.ExecuteCanonical(ctx, "credential-one", testClaudeExecutionCredential(), ExecuteRequest{
		Model: "claude-sonnet-4-5", Payload: requestPayload, Format: "openai-response", OriginalRequest: requestPayload,
	})
	if err != nil {
		t.Fatal(err)
	}

	var firstUpstream struct {
		Tools []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(upstreamBodies[0], &firstUpstream); err != nil {
		t.Fatalf("decode first upstream request: %v; body=%s", err, upstreamBodies[0])
	}
	if len(firstUpstream.Tools) != 1 || firstUpstream.Tools[0].Type != "web_search_20250305" || firstUpstream.Tools[0].Name != "web_search" {
		t.Fatalf("web search upstream tools = %s", upstreamBodies[0])
	}

	var firstResponse struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response.Payload, &firstResponse); err != nil {
		t.Fatalf("decode first Responses payload: %v; body=%s", err, response.Payload)
	}
	if len(firstResponse.Output) != 2 {
		t.Fatalf("Responses output = %s", response.Payload)
	}
	var searchItem struct {
		Type    string `json:"type"`
		Results []struct {
			EncryptedContent string `json:"encrypted_content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(firstResponse.Output[0], &searchItem); err != nil {
		t.Fatal(err)
	}
	var messageItem struct {
		Type    string `json:"type"`
		Content []struct {
			Text        string `json:"text"`
			Annotations []struct {
				EncryptedIndex string `json:"encrypted_index"`
			} `json:"annotations"`
		} `json:"content"`
	}
	if err := json.Unmarshal(firstResponse.Output[1], &messageItem); err != nil {
		t.Fatal(err)
	}
	if searchItem.Type != "web_search_call" || len(searchItem.Results) != 1 || searchItem.Results[0].EncryptedContent != "ENC_SEARCH" ||
		messageItem.Type != "message" || len(messageItem.Content) != 1 || messageItem.Content[0].Text != "GPT-Load source." ||
		len(messageItem.Content[0].Annotations) != 1 || messageItem.Content[0].Annotations[0].EncryptedIndex != "IDX_SEARCH" {
		t.Fatalf("web search Responses payload = %s", response.Payload)
	}

	replayInput := append([]json.RawMessage(nil), firstResponse.Output...)
	replayInput = append(replayInput, json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"Continue"}]}`))
	replayPayload, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"tools": []map[string]any{{"type": "web_search", "max_uses": 1}},
		"input": replayInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ExecuteCanonical(ctx, "credential-one", testClaudeExecutionCredential(), ExecuteRequest{
		Model: "claude-sonnet-4-5", Payload: replayPayload, Format: "openai-response", OriginalRequest: replayPayload,
	}); err != nil {
		t.Fatal(err)
	}
	if len(upstreamBodies) != 2 {
		t.Fatalf("upstream requests = %d, want 2", len(upstreamBodies))
	}
	assertClaudeWebSearchReplay(t, upstreamBodies[1])
}

func assertClaudeWebSearchReplay(t *testing.T, payload []byte) {
	t.Helper()
	var upstream struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(payload, &upstream); err != nil {
		t.Fatalf("decode replay upstream request: %v; body=%s", err, payload)
	}
	var assistantParts []map[string]any
	for _, message := range upstream.Messages {
		if message.Role != "assistant" {
			continue
		}
		parts, ok := message.Content.([]any)
		if !ok {
			continue
		}
		for _, rawPart := range parts {
			if part, ok := rawPart.(map[string]any); ok {
				assistantParts = append(assistantParts, part)
			}
		}
	}
	if len(assistantParts) != 3 || assistantParts[0]["type"] != "server_tool_use" ||
		assistantParts[1]["type"] != "web_search_tool_result" || assistantParts[2]["type"] != "text" {
		t.Fatalf("replayed assistant parts = %s", payload)
	}
	results, ok := assistantParts[1]["content"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("replayed search results = %s", payload)
	}
	result, ok := results[0].(map[string]any)
	if !ok || result["encrypted_content"] != "ENC_SEARCH" {
		t.Fatalf("replayed encrypted search result = %s", payload)
	}
	citations, ok := assistantParts[2]["citations"].([]any)
	if !ok || len(citations) != 1 {
		t.Fatalf("replayed citations = %s", payload)
	}
	citation, ok := citations[0].(map[string]any)
	if !ok || citation["encrypted_index"] != "IDX_SEARCH" {
		t.Fatalf("replayed encrypted citation = %s", payload)
	}
}

func TestClaudeHTTPExecutorStreamsSequentialOpenAIToolCallIndices(t *testing.T) {
	const responseSSE = "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-tools\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Checking.\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-one\",\"name\":\"first_tool\",\"input\":{}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-two\",\"name\":\"second_tool\",\"input\":{}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(responseSSE)),
			Request:    request,
		}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	requestPayload := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"Use both tools"}],
		"tools":[
			{"type":"function","function":{"name":"first_tool","parameters":{"type":"object"}}},
			{"type":"function","function":{"name":"second_tool","parameters":{"type":"object"}}}
		],
		"stream":true
	}`)
	stream, err := NewClaudeHTTPExecutor().ExecuteStreamCanonical(ctx, "credential-one", testClaudeExecutionCredential(), ExecuteRequest{
		Model: "claude-sonnet-4-5", Payload: requestPayload, Format: "openai", OriginalRequest: requestPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	type toolCall struct {
		Index *int   `json:"index"`
		ID    string `json:"id"`
	}
	var calls []toolCall
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		var envelope struct {
			Choices []struct {
				Delta struct {
					ToolCalls []toolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal(chunk.Payload, &envelope) == nil && len(envelope.Choices) == 1 {
			calls = append(calls, envelope.Choices[0].Delta.ToolCalls...)
		}
	}
	if len(calls) != 2 || calls[0].Index == nil || *calls[0].Index != 0 || calls[0].ID != "tool-one" ||
		calls[1].Index == nil || *calls[1].Index != 1 || calls[1].ID != "tool-two" {
		t.Fatalf("streamed tool calls = %#v, want zero-based sequential indices", calls)
	}
}
