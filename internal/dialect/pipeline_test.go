package dialect

import (
	"net/http"
	"testing"

	"gpt-load/internal/state"
)

func TestApplyCredentialUsesDialectDefaultWithEmptyRules(t *testing.T) {
	headers := make(http.Header)

	ApplyCredential(
		NewOpenAI(http.DefaultClient),
		headers,
		"sk-default",
		state.HeaderRules{},
	)

	if got := headers.Get("Authorization"); got != "Bearer sk-default" {
		t.Fatalf("Authorization = %q, want default Bearer credential", got)
	}
}

func TestApplyCredentialExpandsSetRulesAfterDefault(t *testing.T) {
	headers := http.Header{"X-Remove-Me": {"old"}}
	rules := state.HeaderRules{
		Set: map[string]string{
			"Authorization": "Token ${API_KEY}",
			"X-Custom-Key":  "prefix-${API_KEY}-${API_KEY}",
		},
		Remove: []string{"X-Remove-Me"},
	}

	ApplyCredential(
		NewOpenAI(http.DefaultClient),
		headers,
		"sk-custom",
		rules,
	)

	if got := headers.Get("Authorization"); got != "Token sk-custom" {
		t.Fatalf("Authorization = %q, want custom override", got)
	}
	if got := headers.Get("X-Custom-Key"); got != "prefix-sk-custom-sk-custom" {
		t.Fatalf("X-Custom-Key = %q, want all templates expanded", got)
	}
	if got := headers.Get("X-Remove-Me"); got != "" {
		t.Fatalf("X-Remove-Me = %q, want removed", got)
	}
}

func TestApplyCredentialRemoveWinsOverSet(t *testing.T) {
	headers := make(http.Header)
	rules := state.HeaderRules{
		Set:    map[string]string{"Authorization": "Token ${API_KEY}"},
		Remove: []string{"Authorization"},
	}

	ApplyCredential(
		NewOpenAI(http.DefaultClient),
		headers,
		"sk-removed",
		rules,
	)

	if got := headers.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want final remove to win", got)
	}
}
