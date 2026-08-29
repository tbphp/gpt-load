package responsealias

import (
	"bytes"
	"testing"

	"gpt-load/internal/protocol"
)

func TestRewriteJSONCoversClientProtocolShapes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name           string
		clientProtocol protocol.Protocol
		body           string
	}{
		{name: "OpenAI chat", clientProtocol: protocol.OpenAICompletions, body: `{"model":"provider-model","choices":[]}`},
		{name: "OpenAI Responses", clientProtocol: protocol.OpenAIResponses, body: `{"type":"response.completed","response":{"model":"provider-model"}}`},
		{name: "OpenAI Embeddings", clientProtocol: protocol.OpenAIEmbeddings, body: `{"object":"list","model":"provider-model","data":[]}`},
		{name: "Anthropic", clientProtocol: protocol.Anthropic, body: `{"type":"message_start","message":{"model":"provider-model"}}`},
		{name: "Gemini", clientProtocol: protocol.Gemini, body: `{"modelVersion":"provider-model","candidates":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			rewritten, err := RewriteJSON(test.clientProtocol, []byte(test.body), "public-model")
			if err != nil {
				t.Fatalf("RewriteJSON() error = %v", err)
			}
			if !bytes.Contains(rewritten, []byte(`"public-model"`)) || bytes.Contains(rewritten, []byte(`"provider-model"`)) {
				t.Fatalf("rewritten response = %s", rewritten)
			}
		})
	}
}

func TestRewriteSSEPreservesMultipleEventsAndJoinsMultilineData(t *testing.T) {
	t.Parallel()

	input := []byte("event: response\r\ndata: {\"type\":\"response.completed\",\r\ndata: \"response\":{\"model\":\"provider-model\"}}\r\n\r\ndata: [DONE]\r\n\r\n")
	rewritten, err := RewriteSSE(protocol.OpenAIResponses, input, "public-model")
	if err != nil {
		t.Fatalf("RewriteSSE() error = %v", err)
	}
	if !bytes.Contains(rewritten, []byte(`"public-model"`)) || bytes.Contains(rewritten, []byte(`"provider-model"`)) {
		t.Fatalf("rewritten SSE = %q", rewritten)
	}
	if !bytes.Contains(rewritten, []byte("event: response\r\n")) || !bytes.HasSuffix(rewritten, []byte("data: [DONE]\r\n\r\n")) {
		t.Fatalf("SSE framing changed: %q", rewritten)
	}
}
