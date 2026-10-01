package embedded

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestExecutionObservationCapturesFinalReasoningDetails(t *testing.T) {
	budget := int64(2048)
	zero := int64(0)
	for _, test := range []struct {
		name, format, provider, source, wire, mode, effort string
		budget                                             *int64
	}{
		{
			name: "Claude final budget", format: "claude", provider: ProviderClaude,
			source: `{"thinking":{"type":"enabled","budget_tokens":4096}}`,
			wire:   `{"thinking":{"type":"enabled","budget_tokens":2048}}`,
			mode:   "enabled", effort: "medium", budget: &budget,
		},
		{
			name: "Claude mode without effort", format: "claude", provider: ProviderClaude,
			source: `{"thinking":{"type":"adaptive"}}`, wire: `{"thinking":{"type":"adaptive"}}`,
			mode: "adaptive",
		},
		{
			name: "Antigravity final zero budget", format: "gemini", provider: ProviderAntigravity,
			source: `{"generationConfig":{"thinkingConfig":{"thinkingBudget":4096}}}`,
			wire:   `{"request":{"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}}`,
			effort: "none", budget: &zero,
		},
		{
			name: "Codex effective update and mode", format: "openai-response", provider: ProviderCodex,
			source: `{"reasoning":{"mode":"pro","effort":"high"}}`,
			wire:   `{"reasoning":{"mode":"pro","effort":"high"},"input":[{"type":"configuration_update","reasoning":{"effort":"low"}}]}`,
			mode:   "pro", effort: "low",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation := newProviderExecutionObservation(ExecuteRequest{Format: test.format, Payload: []byte(test.source)}, test.provider)
			request, err := http.NewRequest(http.MethodPost, "https://upstream.example.test", strings.NewReader(test.wire))
			if err != nil {
				t.Fatal(err)
			}
			observation.observe(request)
			if observation.reasoningMode() != test.mode || observation.reasoningEffort() != test.effort ||
				!reflect.DeepEqual(observation.reasoningBudgetTokens(), test.budget) {
				t.Fatalf("observed mode=%q effort=%q budget=%v, want mode=%q effort=%q budget=%v",
					observation.reasoningMode(), observation.reasoningEffort(), observation.reasoningBudgetTokens(), test.mode, test.effort, test.budget)
			}
			if copy := observation.reasoningBudgetTokens(); copy != nil {
				*copy = 99
				if !reflect.DeepEqual(observation.reasoningBudgetTokens(), test.budget) {
					t.Fatal("returned budget shares mutable observation state")
				}
			}
		})
	}
}
