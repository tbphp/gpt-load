package bifrost

import (
	"bytes"
	"encoding/json"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/execution/responsealias"
	"gpt-load/internal/protocol"
)

func TestParseRequestReasoningAliasMode(t *testing.T) {
	cases := []struct {
		raw  string
		want responsealias.ReasoningMode
	}{
		{raw: ``, want: responsealias.ReasoningModeOff},
		{raw: `"off"`, want: responsealias.ReasoningModeOff},
		{raw: `"reasoning_to_content"`, want: responsealias.ReasoningModeReasoningToContent},
		{raw: `"reasoning_content_to_reasoning"`, want: responsealias.ReasoningModeContentToReasoning},
		{raw: `"true"`, want: responsealias.ReasoningModeOff},
		{raw: `"false"`, want: responsealias.ReasoningModeOff},
		{raw: `true`, want: responsealias.ReasoningModeOff},
		{raw: `false`, want: responsealias.ReasoningModeOff},
		{raw: `"duplicate"`, want: responsealias.ReasoningModeOff},
		{raw: `"maybe"`, want: responsealias.ReasoningModeOff},
	}
	for _, test := range cases {
		if got := parseRequestReasoningAliasMode(json.RawMessage(test.raw)); got != test.want {
			t.Fatalf("parseRequestReasoningAliasMode(%s) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestParseResponseReasoningAliasMode(t *testing.T) {
	cases := []struct {
		raw  string
		want responsealias.ReasoningMode
	}{
		{raw: ``, want: responsealias.ReasoningModeOff},
		{raw: `"off"`, want: responsealias.ReasoningModeOff},
		{raw: `"duplicate"`, want: responsealias.ReasoningModeDuplicate},
		{raw: `"reasoning_to_content"`, want: responsealias.ReasoningModeOff},
		{raw: `"reasoning_content_to_reasoning"`, want: responsealias.ReasoningModeOff},
		{raw: `"true"`, want: responsealias.ReasoningModeOff},
		{raw: `"false"`, want: responsealias.ReasoningModeOff},
		{raw: `true`, want: responsealias.ReasoningModeOff},
		{raw: `false`, want: responsealias.ReasoningModeOff},
		{raw: `"maybe"`, want: responsealias.ReasoningModeOff},
	}
	for _, test := range cases {
		if got := parseResponseReasoningAliasMode(json.RawMessage(test.raw)); got != test.want {
			t.Fatalf("parseResponseReasoningAliasMode(%s) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestReasoningAliasModesReadBothKeys(t *testing.T) {
	spec := execution.AttemptSpec{
		ClientProtocol: protocol.OpenAICompletions,
		TargetConfig: json.RawMessage(
			`{"base_url":"https://example.com/v1","reasoning_content_alias":"duplicate","request_reasoning_alias":"reasoning_content_to_reasoning"}`,
		),
	}
	responseMode, requestMode := reasoningAliasModes(spec)
	if responseMode != responsealias.ReasoningModeDuplicate {
		t.Fatalf("response mode = %q", responseMode)
	}
	if requestMode != responsealias.ReasoningModeContentToReasoning {
		t.Fatalf("request mode = %q", requestMode)
	}
	if !needsResponseReasoningAlias(spec) {
		t.Fatal("needsResponseReasoningAlias = false for duplicate")
	}
	if !needsRequestReasoningAlias(spec) {
		t.Fatal("needsRequestReasoningAlias = false for set direction")
	}

	// A stored request duplicate (response-only value) selects off.
	stray := execution.AttemptSpec{
		ClientProtocol: protocol.OpenAICompletions,
		TargetConfig:   json.RawMessage(`{"base_url":"https://example.com/v1","request_reasoning_alias":"duplicate"}`),
	}
	if got := requestReasoningAliasMode(stray); got != responsealias.ReasoningModeOff {
		t.Fatalf("request mode for stored duplicate = %q, want off", got)
	}

	// Non chat-completions protocols never take the alias path.
	other := spec
	other.ClientProtocol = protocol.Anthropic
	if needsResponseReasoningAlias(other) || needsRequestReasoningAlias(other) {
		t.Fatal("alias gated wrong protocol")
	}
}

func TestRewriteClientResponseAliasEmitsBothSpellings(t *testing.T) {
	body := []byte(`{"model":"up","choices":[{"message":{"role":"assistant","content":"x","reasoning_content":"deep think"}}]}`)
	out, err := rewriteClientResponseAlias(protocol.OpenAICompletions, body, "", responsealias.ReasoningModeDuplicate)
	if err != nil {
		t.Fatalf("rewrite error = %v", err)
	}
	if !bytes.Contains(out, []byte(`"reasoning":"deep think"`)) {
		t.Fatalf("reasoning duplicate missing: %s", out)
	}
	if !bytes.Contains(out, []byte(`"reasoning_content":"deep think"`)) {
		t.Fatalf("source spelling dropped: %s", out)
	}
	out, err = rewriteClientResponseAlias(protocol.OpenAICompletions, body, "", responsealias.ReasoningModeOff)
	if err != nil {
		t.Fatalf("off rewrite error = %v", err)
	}
	if !bytes.Equal(out, body) {
		t.Fatalf("off mode rewrote body: %s", out)
	}
}
