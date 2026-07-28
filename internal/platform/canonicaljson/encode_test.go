package canonicaljson

import (
	"testing"
)

func TestMarshalUsesJCSObjectOrderAndPreservesArrayOrder(t *testing.T) {
	input := map[string]any{
		"\ue000": "private-use",
		"😀":      "supplementary",
		"z":      "<&>",
		"a":      []any{int64(3), true, nil, "é"},
	}

	got, err := Marshal(input)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"a":[3,true,null,"é"],"z":"<&>","😀":"supplementary","":"private-use"}`
	if string(got) != want {
		t.Fatalf("Marshal() = %s, want %s", got, want)
	}
}

func TestCanonicalizeNormalizesIntegersAndRejectsFloats(t *testing.T) {
	got, err := Canonicalize([]byte(`{"positive":1,"negative_zero":-0,"ordered":[3,2,1]}`))
	if err != nil {
		t.Fatalf("Canonicalize(integer input) error = %v", err)
	}
	want := `{"negative_zero":0,"ordered":[3,2,1],"positive":1}`
	if string(got) != want {
		t.Fatalf("Canonicalize(integer input) = %s, want %s", got, want)
	}

	for _, raw := range []string{
		`{"value":1.5}`,
		`{"value":1e3}`,
		`{"value":9223372036854775808}`,
		`{"value":1,"value":2}`,
		`{"nested":{"value":1,"value":2}}`,
		`{"value":1}{"trailing":2}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Canonicalize([]byte(raw)); err == nil {
				t.Fatalf("Canonicalize(%s) error = nil, want rejection", raw)
			}
		})
	}
}

func TestMarshalRejectsUnsupportedTypedValues(t *testing.T) {
	for _, value := range []any{
		float64(1),
		map[int]string{1: "unsupported-key"},
		func() {},
	} {
		if _, err := Marshal(value); err == nil {
			t.Fatalf("Marshal(%T) error = nil, want rejection", value)
		}
	}
}
