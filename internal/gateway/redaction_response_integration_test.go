package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/testutil/encryptiontest"
)

func TestSuccessResponseRestoresBusinessFieldsOnlyAfterObservation(t *testing.T) {
	service := encryptiontest.Service(t, "redaction-response-integration-test")
	cipher, err := service.NewRedactionCipher(7)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"model": "public", "choices": []any{map[string]any{"message": map[string]any{
			"content": "Send to " + token,
			"tool_calls": []any{map[string]any{"function": map[string]any{
				"name": "send_email", "arguments": `{"recipient":"` + token + `"}`,
			}}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := ForwardInput{Dialect: dialect.NewOpenAI(), ClientProtocol: protocol.OpenAICompletions, RedactionCipher: cipher}
	processor := &responseProcessor{redactor: redact.New()}
	prepared, err := processor.prepareSuccessRepresentation(input, http.StatusOK, http.Header{}, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(prepared.inspectable, []byte(token)) || bytes.Contains(prepared.inspectable, []byte("alice@example.com")) {
		t.Fatal("upstream observation was altered by downstream restore")
	}
	if !json.Valid(prepared.downstream) || bytes.Contains(prepared.downstream, []byte(token)) ||
		!bytes.Contains(prepared.downstream, []byte("alice@example.com")) ||
		prepared.headers.Get("Content-Length") != strconv.Itoa(len(prepared.downstream)) {
		t.Fatal("downstream business response was not restored consistently")
	}
	var restored struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(prepared.downstream, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Choices[0].Message.Content != "Send to alice@example.com" ||
		restored.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"recipient":"alice@example.com"}` {
		t.Fatal("content and tool arguments differ after restoration")
	}
}

func TestResponsesInputItemsRestoresClientTextAfterObservation(t *testing.T) {
	service := encryptiontest.Service(t, "redaction-input-items-integration-test")
	cipher, err := service.NewRedactionCipher(7)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"object":"list","data":[{"id":"item_1","type":"message","content":[{"type":"input_text","text":"` + token + `"}]}]}`)
	input := ForwardInput{Dialect: dialect.NewOpenAIResponses(), ClientProtocol: protocol.OpenAIResponses,
		Operation: execution.OperationResponsesInputItems, RedactionCipher: cipher}
	prepared, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(input, http.StatusOK, http.Header{}, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(prepared.inspectable, []byte(token)) || bytes.Contains(prepared.inspectable, []byte("alice@example.com")) ||
		bytes.Contains(prepared.downstream, []byte(token)) || !bytes.Contains(prepared.downstream, []byte("alice@example.com")) ||
		!json.Valid(prepared.downstream) {
		t.Fatal("input_items observation or downstream restoration changed incorrectly")
	}
}

func TestUnaryCorruptCiphertextFailsAsLocalRestoreError(t *testing.T) {
	service := encryptiontest.Service(t, "redaction-unary-corruption-test")
	cipher, err := service.NewRedactionCipher(7)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	last := "A"
	if strings.HasSuffix(token, last) {
		last = "B"
	}
	broken := token[:len(token)-1] + last
	body := []byte(`{"choices":[{"message":{"content":"` + broken + `"}}]}`)
	executor := fakeExecutionExecutor{unary: func(context.Context, execution.AttemptSpec) execution.AttemptResult {
		return execution.AttemptResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}
	}}
	input := executionForwardInput()
	input.RedactionCipher = cipher
	result := NewExecutionForwarder(executor).Forward(context.Background(), input)
	if !errors.Is(result.Err, errUnaryRestore) || result.ExecutionError == nil ||
		result.ExecutionError.Code != "response_redaction_failed" ||
		result.ExecutionError.OriginHint != execution.ErrorOriginInternal ||
		transportReason(result).Code != reasonResponseRedactionFailed.Code || len(result.Body) != 0 {
		t.Fatalf("invalid response was not rejected locally: %#v", result)
	}
}

func TestErrorResponseDoesNotExposeRedactionToken(t *testing.T) {
	service := encryptiontest.Service(t, "redaction-error-response-test")
	cipher, err := service.NewRedactionCipher(7)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"error":{"message":"upstream echoed ` + token + `"}}`)
	processor := &responseProcessor{redactor: redact.New()}
	input := ForwardInput{Dialect: dialect.NewOpenAI(), ClientProtocol: protocol.OpenAICompletions, RedactionCipher: cipher}
	prepared := processor.prepareErrorRepresentation(input, http.Header{}, body, nil)
	if !json.Valid(prepared.downstream) || bytes.Contains(prepared.downstream, []byte(token)) ||
		bytes.Contains(prepared.classification, []byte(token)) ||
		!bytes.Contains(prepared.downstream, []byte(redact.Placeholder)) {
		t.Fatal("error response or classification exposed a complete redaction token")
	}
}

func TestResponseHeadersDoNotExposeRedactionToken(t *testing.T) {
	service := encryptiontest.Service(t, "redaction-response-header-test")
	cipher, err := service.NewRedactionCipher(7)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	headers := sanitizeForwardResponseHeaders(http.Header{
		"X-Trace": {token}, "Content-Type": {"application/json"},
	}, ForwardInput{ClientProtocol: protocol.OpenAICompletions})
	if headers.Get("X-Trace") != "" || headers.Get("Content-Type") != "application/json" {
		t.Fatalf("redaction token survived in response headers: %#v", headers)
	}
}
