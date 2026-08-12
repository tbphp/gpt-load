package bifrost

import (
	"bytes"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestNativeFirstSSEEventGateAcceptsTwoMiBEventAcrossChunks(t *testing.T) {
	gate := &nativeFirstSSEEventGate{}
	wire := []byte("data: " + strings.Repeat("x", 2<<20) + "\n\n")
	split := len(wire) / 3
	for _, chunk := range [][]byte{wire[:split], wire[split : 2*split], wire[2*split:]} {
		output, err := gate.push(chunk)
		if err != nil {
			t.Fatalf("push() error = %v", err)
		}
		if gate.ready && len(output) != len(wire) {
			t.Fatalf("ready output length = %d, want %d", len(output), len(wire))
		}
	}
	if !gate.ready {
		t.Fatal("two MiB first SSE event did not become ready")
	}
}

func TestRewriteNativeResponseModelCoversNativeProtocolShapes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name           string
		clientProtocol protocol.Protocol
		body           string
	}{
		{name: "OpenAI chat", clientProtocol: protocol.OpenAICompletions, body: `{"model":"provider-model","choices":[]}`},
		{name: "OpenAI Responses object", clientProtocol: protocol.OpenAIResponses, body: `{"type":"response","model":"provider-model","output":[]}`},
		{name: "OpenAI Responses event", clientProtocol: protocol.OpenAIResponses, body: `{"type":"response.completed","response":{"model":"provider-model"}}`},
		{name: "Anthropic message", clientProtocol: protocol.Anthropic, body: `{"type":"message","model":"provider-model","content":[]}`},
		{name: "Anthropic message_start", clientProtocol: protocol.Anthropic, body: `{"type":"message_start","message":{"model":"provider-model"}}`},
		{name: "Gemini response", clientProtocol: protocol.Gemini, body: `{"modelVersion":"provider-model","candidates":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			rewritten, err := rewriteClientResponseModel(test.clientProtocol, []byte(test.body), "public-model")
			if err != nil {
				t.Fatalf("rewriteClientResponseModel() error = %v", err)
			}
			if !bytes.Contains(rewritten, []byte(`"public-model"`)) || bytes.Contains(rewritten, []byte(`"provider-model"`)) {
				t.Fatalf("rewritten response = %s", rewritten)
			}
		})
	}
}

func TestNativeAliasSSERewriterCoversNativeProtocolShapes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name           string
		clientProtocol protocol.Protocol
		payload        string
	}{
		{name: "OpenAI chat", clientProtocol: protocol.OpenAICompletions, payload: `{"model":"provider-model","choices":[]}`},
		{name: "OpenAI Responses", clientProtocol: protocol.OpenAIResponses, payload: `{"type":"response.completed","response":{"model":"provider-model"}}`},
		{name: "Anthropic", clientProtocol: protocol.Anthropic, payload: `{"type":"message_start","message":{"model":"provider-model"}}`},
		{name: "Gemini", clientProtocol: protocol.Gemini, payload: `{"modelVersion":"provider-model","candidates":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			rewriter := newNativeAliasSSERewriter(execution.AttemptSpec{
				ClientProtocol: test.clientProtocol,
				ClientModel:    "public-model",
				UpstreamModel:  "provider-model",
			})
			raw := []byte("event: update\r\ndata: " + test.payload + "\r\n\r\ndata: [DONE]\r\n\r\n")
			split := len(raw) / 2
			first, err := rewriter.push(raw[:split])
			if err != nil {
				t.Fatalf("first push error = %v", err)
			}
			second, err := rewriter.push(raw[split:])
			if err != nil {
				t.Fatalf("second push error = %v", err)
			}
			tail, err := rewriter.finish()
			if err != nil {
				t.Fatalf("finish error = %v", err)
			}
			output := bytes.Join([][]byte{first, second, tail}, nil)
			if !bytes.Contains(output, []byte(`"public-model"`)) || bytes.Contains(output, []byte(`"provider-model"`)) {
				t.Fatalf("rewritten SSE = %s", output)
			}
			if !bytes.Contains(output, []byte("data: [DONE]\r\n\r\n")) {
				t.Fatalf("terminal SSE marker changed: %q", output)
			}
		})
	}
}

func TestNativeAliasSSERewriterIsDisabledForEqualAliases(t *testing.T) {
	t.Parallel()

	if rewriter := newNativeAliasSSERewriter(execution.AttemptSpec{
		ClientProtocol: protocol.OpenAICompletions,
		ClientModel:    "same-model",
		UpstreamModel:  "same-model",
	}); rewriter != nil {
		t.Fatal("equal aliases enabled native SSE rewriting")
	}
}
