package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
)

func TestSuccessResponsePreservesBusinessTextWithoutKnownCredential(t *testing.T) {
	processor := &responseProcessor{redactor: redact.New()}
	input := ForwardInput{Dialect: dialect.NewOpenAI(), ClientProtocol: protocol.OpenAICompletions}
	for _, body := range [][]byte{
		[]byte(`{"choices":[{"message":{"content":"https://example.com/s?q=go"}}]}`),
		[]byte(`{"choices":[{"message":{"content":"Use password: x"}}]}`),
	} {
		prepared, err := processor.prepareSuccessRepresentation(input, http.StatusOK, http.Header{}, body, nil)
		if err != nil {
			t.Fatalf("prepareSuccessRepresentation() error = %v", err)
		}
		if !json.Valid(prepared.downstream) || !bytes.Equal(prepared.downstream, body) {
			t.Fatalf("business response changed or invalid JSON: %s", prepared.downstream)
		}
		if !bytes.Equal(prepared.inspectable, body) {
			t.Fatalf("inspectable response changed: %s", prepared.inspectable)
		}
	}
}

func TestSuccessResponseRedactsOnlyKnownCredentialLiteral(t *testing.T) {
	processor := &responseProcessor{redactor: redact.New()}
	input := ForwardInput{Dialect: dialect.NewOpenAI(), ClientProtocol: protocol.OpenAICompletions}
	body := []byte(`{"choices":[{"message":{"content":"Visit https://example.com/s?q=go with password: x; infrastructure key is upstream-secret"}}]}`)

	prepared, err := processor.prepareSuccessRepresentation(input, http.StatusOK, http.Header{}, body, []string{"upstream-secret"})
	if err != nil {
		t.Fatalf("prepareSuccessRepresentation() error = %v", err)
	}
	if !json.Valid(prepared.downstream) || bytes.Contains(prepared.downstream, []byte("upstream-secret")) ||
		!bytes.Contains(prepared.downstream, []byte("https://example.com/s?q=go with password: x")) ||
		!bytes.Contains(prepared.downstream, []byte(redact.Placeholder)) {
		t.Fatalf("downstream credential handling = %s", prepared.downstream)
	}
	if bytes.Contains(prepared.inspectable, []byte("upstream-secret")) {
		t.Fatalf("inspectable response leaked known credential: %s", prepared.inspectable)
	}
}
