package codexrouting

import (
	"testing"
	"time"
)

func TestNormalizeGateway(t *testing.T) {
	t.Parallel()
	if got := normalizeGateway("88"); got != "unified-88" {
		t.Fatalf("88 -> %q", got)
	}
	if got := normalizeGateway("unified_88"); got != "unified-88" {
		t.Fatalf("unified_88 -> %q", got)
	}
	if got := normalizeGateway("any"); got != "" {
		t.Fatalf("any -> %q", got)
	}
}

func TestCreatedModelRequiresResponseCreated(t *testing.T) {
	t.Parallel()
	body := []byte(`data: {"type":"response.output_text.delta","delta":"gpt-6-luna"}

data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-6-astra"}}

`)
	if got := createdModel(body); got != "gpt-6-astra" {
		t.Fatalf("createdModel = %q", got)
	}
}

func TestFernetIssuedAt(t *testing.T) {
	t.Parallel()
	fallback := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !fernetIssuedAt("not-a-ticket", fallback).Equal(fallback) {
		t.Fatal("invalid ticket should fall back")
	}
}
