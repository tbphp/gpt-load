package bifrost

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/parametertrace"
	"gpt-load/internal/reasoning"
)

func TestInspectWireAppliedReasoningUsesProviderPayloadShapes(t *testing.T) {
	t.Parallel()

	budget4096 := int64(4096)
	dynamicBudget := int64(-1)
	budget8192 := int64(8192)
	tests := []struct {
		name string
		body string
		want *reasoning.Config
	}{
		{
			name: "OpenAI chat",
			body: `{"model":"gpt-5","reasoning_effort":"HIGH"}`,
			want: &reasoning.Config{Effort: "high"},
		},
		{
			name: "OpenAI Responses",
			body: `{"model":"gpt-5","reasoning":{"mode":"PRO","effort":"XHIGH","max_tokens":4096}}`,
			want: &reasoning.Config{Mode: "pro", Effort: "xhigh", BudgetTokens: &budget4096},
		},
		{
			name: "Anthropic",
			body: `{"thinking":{"type":"adaptive","budget_tokens":4096},"output_config":{"effort":"HIGH"}}`,
			want: &reasoning.Config{Mode: "adaptive", Effort: "high", BudgetTokens: &budget4096},
		},
		{
			name: "Gemini",
			body: `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH","thinkingBudget":-1}}}`,
			want: &reasoning.Config{Effort: "high", BudgetTokens: &dynamicBudget},
		},
		{
			name: "Bedrock Anthropic",
			body: `{"additionalModelRequestFields":{"thinking":{"type":"enabled","budget_tokens":8192},"output_config":{"effort":"MEDIUM"}}}`,
			want: &reasoning.Config{Mode: "enabled", Effort: "medium", BudgetTokens: &budget8192},
		},
		{
			name: "Bedrock Nova",
			body: `{"additionalModelRequestFields":{"reasoningConfig":{"type":"enabled","maxReasoningEffort":"HIGH","budget_tokens":4096}}}`,
			want: &reasoning.Config{Mode: "enabled", Effort: "high", BudgetTokens: &budget4096},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := inspectWireAppliedReasoning([]byte(test.body)); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("inspectWireAppliedReasoning() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestTakeAppliedReasoningAlwaysClearsRawRequest(t *testing.T) {
	t.Parallel()

	raw := any(json.RawMessage(`{"messages":[{"role":"user","content":"secret prompt"}],"reasoning_effort":"HIGH"}`))
	got := takeAppliedReasoning(&raw)
	if raw != nil {
		t.Fatalf("raw request was retained: %#v", raw)
	}
	if want := (&reasoning.Config{Effort: "high"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("takeAppliedReasoning() = %#v, want %#v", got, want)
	}

	invalid := any(json.RawMessage(`{"reasoning_effort":`))
	if got := takeAppliedReasoning(&invalid); got != nil || invalid != nil {
		t.Fatalf("invalid raw request result/raw = %#v/%#v", got, invalid)
	}

	unsafe := any(map[string]any{
		"reasoning_effort": strings.Repeat("x", maxAppliedReasoningValueBytes+1),
		"messages":         []any{map[string]any{"content": "secret prompt"}},
	})
	if got := takeAppliedReasoning(&unsafe); got != nil || unsafe != nil {
		t.Fatalf("unsafe raw request result/raw = %#v/%#v", got, unsafe)
	}
}

func TestEnableConvertedWireCaptureRequiresBoundedConvertedRequest(t *testing.T) {
	t.Parallel()
	client := parametertrace.ProjectJSON([]byte(`{"messages":[{"role":"user","content":"secret"}]}`))

	native := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
	defer native.Cancel()
	enableConvertedWireCapture(native, preparedAttempt{
		mode:    channel.RouteNative,
		request: &schemas.BifrostChatRequest{Params: &schemas.ChatParameters{Reasoning: &schemas.ChatReasoning{}}},
	}, &client)
	if _, exists := native.Value(schemas.BifrostContextKeySendBackRawRequest).(bool); exists {
		t.Fatal("native request enabled raw capture")
	}

	withoutReasoning := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
	defer withoutReasoning.Cancel()
	enableConvertedWireCapture(withoutReasoning, preparedAttempt{
		mode:    channel.RouteConverted,
		request: &schemas.BifrostChatRequest{Params: &schemas.ChatParameters{}},
	}, &client)
	if value, _ := withoutReasoning.Value(schemas.BifrostContextKeySendBackRawRequest).(bool); !value {
		t.Fatal("bounded converted request did not enable raw capture")
	}

	converted := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
	defer converted.Cancel()
	enableConvertedWireCapture(converted, preparedAttempt{
		mode:    channel.RouteConverted,
		request: &schemas.BifrostChatRequest{Params: &schemas.ChatParameters{Reasoning: &schemas.ChatReasoning{}}},
	}, &client)
	for _, key := range []schemas.BifrostContextKey{
		schemas.BifrostContextKeyAllowPerRequestRawOverride,
		schemas.BifrostContextKeySendBackRawRequest,
	} {
		if value, _ := converted.Value(key).(bool); !value {
			t.Fatalf("context key %q = false", key)
		}
	}
	if value, ok := converted.Value(schemas.BifrostContextKeySendBackRawResponse).(bool); !ok || value {
		t.Fatalf("raw response override = %#v", converted.Value(schemas.BifrostContextKeySendBackRawResponse))
	}

	oversize := parametertrace.Snapshot{
		SchemaVersion: parametertrace.SchemaVersion,
		State:         parametertrace.CaptureSkippedOversize,
		Entries:       []parametertrace.Entry{},
	}
	skipped := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
	defer skipped.Cancel()
	enableConvertedWireCapture(skipped, preparedAttempt{mode: channel.RouteConverted}, &oversize)
	if _, exists := skipped.Value(schemas.BifrostContextKeySendBackRawRequest).(bool); exists {
		t.Fatal("oversize request enabled raw capture")
	}
}

func TestTakeWireObservationClearsRawAndComparesSafeParameters(t *testing.T) {
	client := parametertrace.ProjectJSON([]byte(`{"thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"secret prompt"}]}`))
	raw := any(json.RawMessage(`{"reasoning_effort":"high","messages":[{"role":"user","content":"secret prompt"}]}`))
	reasoningConfig, trace := takeWireObservation(&raw, &client)
	if raw != nil {
		t.Fatalf("raw request was retained: %#v", raw)
	}
	if !reflect.DeepEqual(reasoningConfig, &reasoning.Config{Effort: "high"}) {
		t.Fatalf("reasoning = %#v", reasoningConfig)
	}
	if trace == nil || trace.State != parametertrace.CaptureCaptured || len(trace.Changes) == 0 {
		t.Fatalf("trace = %#v", trace)
	}
	encoded, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret prompt") {
		t.Fatalf("trace leaked prompt: %s", encoded)
	}
}
