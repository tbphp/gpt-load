package utils

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNormalizeJSONRequestBodyValidJSONNoChange(t *testing.T) {
	original := []byte(`{"model":"mistral-large-latest","max_tokens":16}`)
	got, normalized := NormalizeJSONRequestBody(original, "application/json")

	if normalized {
		t.Fatalf("expected normalized=false, got true")
	}
	if string(got) != string(original) {
		t.Fatalf("expected body unchanged, got %s", string(got))
	}
}

func TestNormalizeJSONRequestBodyLenientObject(t *testing.T) {
	original := []byte(`{messages:[{content:ping,role:user}],model:mistral-large-latest,max_tokens:16}`)
	got, normalized := NormalizeJSONRequestBody(original, "application/json")

	if !normalized {
		t.Fatalf("expected normalized=true, got false")
	}

	var parsed map[string]any
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("normalized output is not valid JSON: %v", err)
	}

	expected := map[string]any{
		"model":      "mistral-large-latest",
		"max_tokens": float64(16),
		"messages": []any{
			map[string]any{
				"content": "ping",
				"role":    "user",
			},
		},
	}

	if !reflect.DeepEqual(parsed, expected) {
		t.Fatalf("unexpected parsed JSON.\nexpected: %#v\ngot: %#v", expected, parsed)
	}
}

func TestNormalizeJSONRequestBodySkipsNonJSONContentType(t *testing.T) {
	original := []byte(`{messages:[{content:ping,role:user}],model:mistral-large-latest,max_tokens:16}`)
	got, normalized := NormalizeJSONRequestBody(original, "text/plain")

	if normalized {
		t.Fatalf("expected normalized=false for text/plain")
	}
	if string(got) != string(original) {
		t.Fatalf("expected body unchanged, got %s", string(got))
	}
}

func TestNormalizeJSONRequestBodyInvalidGarbage(t *testing.T) {
	original := []byte(`{this is not parseable:::`)
	got, normalized := NormalizeJSONRequestBody(original, "application/json")

	if normalized {
		t.Fatalf("expected normalized=false for invalid garbage")
	}
	if string(got) != string(original) {
		t.Fatalf("expected body unchanged, got %s", string(got))
	}
}
