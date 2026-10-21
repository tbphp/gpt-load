package bifrost

import (
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

// SSE data lines never contain raw line breaks, so the fixture is one line.
const wireIterations = `"iterations":[{"type":"message","input_tokens":200,"output_tokens":20},` +
	`{"type":"advisor_message","model":"claude-opus-4-8","input_tokens":300,"output_tokens":400},` +
	`{"type":"message","input_tokens":250,"output_tokens":30}]`

func nativeAnthropicAttempt() (execution.AttemptSpec, preparedAttempt) {
	return execution.AttemptSpec{Operation: execution.OperationChatCompletion, ClientProtocol: protocol.Anthropic},
		preparedAttempt{mode: channel.RouteNative, upstreamProtocol: protocol.Anthropic}
}

func TestNativeAnthropicWireUsageKeepsAdvisorPassesOnTheStream(t *testing.T) {
	spec, prepared := nativeAnthropicAttempt()
	wire := newNativeWireUsage(spec, prepared)
	if wire == nil {
		t.Fatal("native anthropic chat must tap the wire usage")
	}
	stream := "event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":200,\"output_tokens\":1}}}\r\n\r\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":450,\"output_tokens\":50," + wireIterations + "}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for i := 0; i < len(stream); i += 7 { // arbitrary chunking must not matter
		end := min(i+7, len(stream))
		wire.observeSSE([]byte(stream[i:end]))
	}
	wire.finish()
	sdk := &execution.UsageEvidence{Raw: []byte(`{"sdk":true}`)}
	got := wire.override(sdk, true)
	if got == sdk || got.Normalized.Extra == nil || len(got.Normalized.Extra.Passes) != 1 {
		t.Fatalf("override = %#v, want one advisor pass", got)
	}
	if pass := got.Normalized.Extra.Passes[0]; pass.Model != "claude-opus-4-8" || pass.Tokens.UncachedInput != 300 || pass.Tokens.Output != 400 {
		t.Fatalf("pass = %#v", pass)
	}
	if string(got.Raw) != `{"sdk":true}` {
		t.Fatalf("raw = %s, want the SDK raw usage kept", got.Raw)
	}
}

func TestNativeAnthropicWireUsageKeepsAdvisorPassesOnTheBody(t *testing.T) {
	spec, prepared := nativeAnthropicAttempt()
	body := []byte(`{"usage":{"input_tokens":450,"output_tokens":50,` + wireIterations + `}}`)
	got := unaryNativeWireUsage(spec, prepared, body, nil)
	if got == nil || got.Normalized.Extra == nil || len(got.Normalized.Extra.Passes) != 1 {
		t.Fatalf("usage = %#v, want one advisor pass", got)
	}
}

func TestNativeAnthropicWireUsageLeavesOtherRoutesAlone(t *testing.T) {
	spec, prepared := nativeAnthropicAttempt()
	body := []byte(`{"usage":{"input_tokens":450,"output_tokens":50,` + wireIterations + `}}`)
	sdk := &execution.UsageEvidence{Normalized: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 450, Output: 50}}, Raw: []byte(`{}`)}
	converted := prepared
	converted.mode = channel.RouteConverted
	if newNativeWireUsage(spec, converted) != nil || unaryNativeWireUsage(spec, converted, body, sdk) != sdk {
		t.Fatal("converted routes keep their own usage capture")
	}
	plain := []byte(`{"usage":{"input_tokens":450,"output_tokens":50}}`)
	if got := unaryNativeWireUsage(spec, prepared, plain, sdk); got != sdk {
		t.Fatalf("a response without extra passes must keep the SDK usage, got %#v", got)
	}
}

func TestNativeGeminiWireUsageAddsToolUsePromptTokens(t *testing.T) {
	spec := execution.AttemptSpec{Operation: execution.OperationChatCompletion, ClientProtocol: protocol.Gemini}
	prepared := preparedAttempt{mode: channel.RouteNative, upstreamProtocol: protocol.Gemini}
	// the SDK counts promptTokenCount + candidates only
	sdk := &execution.UsageEvidence{Normalized: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 100, Output: 50}}, Raw: []byte(`{}`)}
	meta := `"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"toolUsePromptTokenCount":9000,"totalTokenCount":9150}`

	body := unaryNativeWireUsage(spec, prepared, []byte(`{"candidates":[{"finishReason":"STOP"}],`+meta+`}`), sdk)
	if body == sdk || body.Normalized.Tokens.UncachedInput != 9100 || body.Normalized.Tokens.Output != 50 {
		t.Fatalf("body usage = %#v, want 9100 input / 50 output", body)
	}

	wire := newNativeWireUsage(spec, prepared)
	wire.observeSSE([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]," + meta + "}\r\n\r\n"))
	wire.finish()
	if mid := wire.override(sdk, false); mid != sdk {
		t.Fatalf("Gemini usage is read when the stream ends, not before: %#v", mid)
	}
	end := wire.override(sdk, true)
	if end == sdk || end.Normalized.Tokens.UncachedInput != 9100 {
		t.Fatalf("stream usage = %#v, want 9100 input", end)
	}

	plain := `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"totalTokenCount":150}}`
	if got := unaryNativeWireUsage(spec, prepared, []byte(plain), sdk); got != sdk {
		t.Fatalf("a response without tool-use tokens must keep the SDK usage, got %#v", got)
	}
}
