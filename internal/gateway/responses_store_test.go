package gateway

import (
	"bytes"
	"testing"

	"gpt-load/internal/dialect"
)

func TestStatelessResponsesStoreRewriteDoesNotEscapeHTML(t *testing.T) {
	source := []byte(`{"input":"<tag>&","store":true}`)
	request, err := forceStatelessResponsesRequest(source)
	if err != nil {
		t.Fatal(err)
	}
	response, err := normalizeStatelessResponsesSuccess(source)
	if err != nil {
		t.Fatal(err)
	}
	for name, rewritten := range map[string][]byte{"request": request, "response": response} {
		if !bytes.Contains(rewritten, []byte(`<tag>&`)) ||
			bytes.Contains(rewritten, []byte(`\u003c`)) ||
			len(rewritten) > len(source)+maxResponsesStoreRewriteGrowthBytes {
			t.Fatalf("%s rewrite = %s", name, rewritten)
		}
	}

	event, err := normalizeStatelessResponsesSSEPayload(dialect.StreamEvent{
		Name: "response.completed",
		Payload: []byte(
			`{"type":"response.completed","response":{"output_text":"<tag>&"}}`,
		),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(event.body, []byte(`<tag>&`)) ||
		bytes.Contains(event.body, []byte(`\u003c`)) {
		t.Fatalf("SSE rewrite = %s", event.body)
	}
}

func TestStatelessResponsesStoreRewritePreservesUnrecognizedSSEPayload(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`not-json`),
		[]byte(`{"type":"response.queued","response":"opaque"}`),
		[]byte(`{"type":"response.output_text.delta","delta":"ok"}`),
	} {
		rewritten, err := normalizeStatelessResponsesSSEPayload(
			dialect.StreamEvent{Payload: payload},
			false,
		)
		if err != nil || !bytes.Equal(rewritten.body, payload) {
			t.Fatalf("rewrite(%s) = %s, %v", payload, rewritten.body, err)
		}
	}
}

func TestStatelessResponsesStoreRewriteRejectsInvalidRequestUTF8AndPreservesResponse(t *testing.T) {
	payload := []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}
	if _, err := forceStatelessResponsesRequest(payload); err == nil {
		t.Fatal("forceStatelessResponsesRequest() accepted invalid UTF-8")
	}
	if got, err := normalizeStatelessResponsesSuccess(payload); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("response = %q, want unchanged %q", got, payload)
	}
}
