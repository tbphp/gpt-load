package bifrost

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
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

func TestEnableAppliedReasoningWireCaptureRequiresConvertedReasoning(t *testing.T) {
	t.Parallel()

	native := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
	defer native.Cancel()
	enableAppliedReasoningWireCapture(native, preparedAttempt{
		mode:    channel.RouteNative,
		request: &schemas.BifrostChatRequest{Params: &schemas.ChatParameters{Reasoning: &schemas.ChatReasoning{}}},
	})
	if _, exists := native.Value(schemas.BifrostContextKeySendBackRawRequest).(bool); exists {
		t.Fatal("native request enabled raw capture")
	}

	withoutReasoning := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
	defer withoutReasoning.Cancel()
	enableAppliedReasoningWireCapture(withoutReasoning, preparedAttempt{
		mode:    channel.RouteConverted,
		request: &schemas.BifrostChatRequest{Params: &schemas.ChatParameters{}},
	})
	if _, exists := withoutReasoning.Value(schemas.BifrostContextKeySendBackRawRequest).(bool); exists {
		t.Fatal("converted request without reasoning enabled raw capture")
	}

	converted := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
	defer converted.Cancel()
	enableAppliedReasoningWireCapture(converted, preparedAttempt{
		mode:    channel.RouteConverted,
		request: &schemas.BifrostChatRequest{Params: &schemas.ChatParameters{Reasoning: &schemas.ChatReasoning{}}},
	})
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
}
