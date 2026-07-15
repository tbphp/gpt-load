package models

import (
	"bytes"
	"testing"
)

func TestJSONScanCopiesInput(t *testing.T) {
	t.Parallel()

	source := []byte(`{"enabled":true}`)
	var value JSON
	if err := value.Scan(source); err != nil {
		t.Fatalf("Scan(valid JSON) error = %v", err)
	}

	source[2] = 'x'
	if !bytes.Equal(value, []byte(`{"enabled":true}`)) {
		t.Fatalf("JSON after source mutation = %q, want an independent copy", value)
	}
}

func TestJSONScanAcceptsDatabaseRepresentations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  JSON
	}{
		{name: "bytes", input: []byte(`["openai"]`), want: JSON(`["openai"]`)},
		{name: "string", input: `{"model":"gpt"}`, want: JSON(`{"model":"gpt"}`)},
		{name: "null", input: nil, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			value := JSON(`{"stale":true}`)
			if err := value.Scan(tt.input); err != nil {
				t.Fatalf("Scan(%T) error = %v", tt.input, err)
			}
			if !bytes.Equal(value, tt.want) {
				t.Fatalf("Scan(%T) = %q, want %q", tt.input, value, tt.want)
			}
		})
	}
}

func TestJSONScanRejectsInvalidValuesWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
	}{
		{name: "invalid bytes", input: []byte(`{"broken":`)},
		{name: "invalid string", input: `not-json`},
		{name: "unsupported type", input: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			value := JSON(`{"preserved":true}`)
			if err := value.Scan(tt.input); err == nil {
				t.Fatalf("Scan(%T) error = nil, want validation error", tt.input)
			}
			if !bytes.Equal(value, []byte(`{"preserved":true}`)) {
				t.Fatalf("JSON after rejected Scan = %q, want original value", value)
			}
		})
	}
}

func TestJSONValueValidatesBeforePersistence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   JSON
		want    any
		wantErr bool
	}{
		{name: "object", value: JSON(`{"enabled":true}`), want: `{"enabled":true}`},
		{name: "array", value: JSON(`["openai"]`), want: `["openai"]`},
		{name: "null database value", value: nil, want: nil},
		{name: "empty database value", value: JSON{}, want: nil},
		{name: "invalid", value: JSON(`{"broken":`), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.value.Value()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Value() error = nil, want validation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Value() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Value() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
