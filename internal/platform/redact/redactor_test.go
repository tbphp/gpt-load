package redact

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactorStringCoversCredentialShapes(t *testing.T) {
	redactor := New()
	tests := []struct {
		name  string
		input string
	}{
		{name: "OpenAI key", input: `error for sk-proj-abcdefghijklmnopqrstuvwxyz`},
		{name: "AccessKey", input: `token=gl-0123456789abcdef0123456789abcdef`},
		{name: "JSON field", input: `{"api_key":"custom-secret-value"}`},
		{name: "query field", input: `url?key=custom-secret-value&model=gpt-4o`},
		{name: "authorization", input: `Authorization: Bearer custom-secret-value`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactor.String(tt.input)
			if got == tt.input || !strings.Contains(got, Placeholder) {
				t.Fatalf("String() = %q, want redacted placeholder", got)
			}
		})
	}
}

func TestRedactorStringRedactsEntireAuthorizationValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "double quoted JSON bearer",
			input: `{"authorization":"Bearer custom-json-secret","safe":"kept"}`,
			want:  `{"authorization":"[REDACTED]","safe":"kept"}`,
		},
		{
			name:  "double quoted JSON basic",
			input: `{"Authorization":"Basic custom-json-secret","safe":"kept"}`,
			want:  `{"Authorization":"[REDACTED]","safe":"kept"}`,
		},
		{
			name:  "single quoted value",
			input: `{'authorization':'Token custom-single-secret','safe':'kept'}`,
			want:  `{'authorization':'[REDACTED]','safe':'kept'}`,
		},
		{
			name:  "basic header",
			input: "Authorization: Basic custom-header-secret\nX-Safe: kept",
			want:  "Authorization: [REDACTED]\nX-Safe: kept",
		},
	}

	redactor := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactor.String(tt.input); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedactorUsesExactKnownSecretForCustomPrefixes(t *testing.T) {
	const secret = "provider-secret-without-standard-prefix"
	got := New().String("upstream echoed "+secret+" twice "+secret, secret)
	if strings.Contains(got, secret) || strings.Count(got, Placeholder) != 2 {
		t.Fatalf("String() = %q, want both known-secret occurrences redacted", got)
	}
}

func TestRedactorLeavesOrdinaryTextAndEmptySecretsAlone(t *testing.T) {
	inputs := []string{
		"model gpt-4o completed normally",
		`{"monkey":"banana","token_count":12}`,
	}
	for _, input := range inputs {
		if got := New().String(input, ""); got != input {
			t.Fatalf("String() = %q, want %q", got, input)
		}
	}
}

func TestRedactorBytesDoesNotAliasInput(t *testing.T) {
	input := []byte(`{"token":"custom-secret"}`)
	got := New().Bytes(input)
	if bytes.Equal(got, input) || !bytes.Contains(got, []byte(Placeholder)) {
		t.Fatalf("Bytes() = %q, want redacted copy", got)
	}
	if string(input) != `{"token":"custom-secret"}` {
		t.Fatalf("Bytes() mutated input: %q", input)
	}
}
