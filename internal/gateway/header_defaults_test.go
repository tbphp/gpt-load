package gateway

import (
	"slices"
	"testing"

	"gpt-load/internal/state"
)

func TestRequestHeaderDefaultsPreserveNonemptyClientValues(t *testing.T) {
	for _, test := range []struct{ name, value, want string }{
		{"missing", "", "Go-http-client/1.1"},
		{"whitespace", " \t", "Go-http-client/1.1"},
		{"client", "client/1.0", "client/1.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := executionForwardInput()
			input.Request.Header.Set("User-Agent", test.value)
			input.Group.HeaderRules = state.HeaderRules{Default: map[string]string{"User-Agent": "Go-http-client/1.1", "X-Template": "${API_KEY}"}}
			spec, err := newExecutionAttemptSpec(input)
			if err != nil {
				t.Fatal(err)
			}
			if got := spec.Header.Get("User-Agent"); got != test.want {
				t.Fatalf("UA = %q, want %q", got, test.want)
			}
			if got := spec.Header.Get("X-Template"); got != input.APIKey {
				t.Fatalf("template = %q", got)
			}
			if got := input.Request.Header.Get("User-Agent"); got != test.value {
				t.Fatal("mutated client headers")
			}
		})
	}
}

func TestHeaderDefaultsAreIncludedInErrorSummaryRedaction(t *testing.T) {
	secrets := resolvedErrorSummarySecretValues("fixture-token", state.HeaderRules{Default: map[string]string{
		"X-Private": "fixture-private", "X-Template": "Bearer ${API_KEY}",
	}})
	for _, want := range []string{"fixture-private", "Bearer fixture-token"} {
		if !slices.Contains(secrets, want) {
			t.Fatalf("missing redaction value %q", want)
		}
	}
}

func TestHeaderDefaultsPreserveRepeatedClientValues(t *testing.T) {
	for _, values := range [][]string{{"", "client-value"}, {"client-value", ""}, {"", " \t"}} {
		input := executionForwardInput()
		input.Request.Header["X-Tenant"] = values
		input.Group.HeaderRules = state.HeaderRules{Default: map[string]string{"X-Tenant": "fallback"}}
		spec, err := newExecutionAttemptSpec(input)
		if err != nil {
			t.Fatal(err)
		}
		want := values
		if !slices.Contains(values, "client-value") {
			want = []string{"fallback"}
		}
		if got := spec.Header.Values("X-Tenant"); !slices.Equal(got, want) {
			t.Fatalf("values %q: got %q, want %q", values, got, want)
		}
	}
}
