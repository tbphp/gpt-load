package bifrost

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestConvertedStreamSamplesUpstreamMetadataBeforeFrame(t *testing.T) {
	observed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"role\":\"assistant\",\"type\":\"message\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n")
		w.(http.Flusher).Flush()
		select {
		case <-observed:
		case <-time.After(time.Second):
			t.Error("first upstream data line was held for conversion or framing")
		}
		_, _ = fmt.Fprint(w, "\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: server.URL})
	spec := convertedSpec(channel.Anthropic, protocol.OpenAICompletions, execution.OperationChatCompletion, "/v1/chat/completions", []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`))
	ctx, stop := execution.WithFirstResponseObserver(t.Context(), func() { close(observed) })
	defer stop()
	result := runtime.ExecuteStream(ctx, spec, func(execution.StreamEvent) error { return nil })
	if result.Error != nil {
		t.Fatalf("stream = %+v", result.Error)
	}
}
