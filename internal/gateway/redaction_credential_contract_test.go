package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/subscription/providers/claude"
)

func TestRestorationCredentialBoundary(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	credential := claude.Credential{AccessToken: "synthetic-access-token", RefreshToken: "synthetic-refresh-token", IDToken: "synthetic-id-token", Email: "owner@example.invalid", AccountUUID: "synthetic-account", OrganizationUUID: "synthetic-org", DeviceIDs: []string{"synthetic-device"}}
	input := executionForwardInput()
	input.APIKey = ""
	input.CredentialSecrets = credential.SecretValues()
	input.RedactionCipher = c
	input.Credential = execution.NewCredentialSnapshot(1, 1, 1, redactionBoundaryJSON(t, credential))
	for _, value := range []string{credential.Email, credential.AccountUUID, credential.OrganizationUUID, credential.DeviceIDs[0], credential.AccessToken, credential.RefreshToken, credential.IDToken} {
		token, err := c.EncryptToken(value)
		if err != nil {
			t.Fatal(err)
		}
		isCredential := value == credential.AccessToken || value == credential.RefreshToken || value == credential.IDToken
		body := redactionBoundaryJSON(t, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": token}}}})
		_, err = (&responseProcessor{}).prepareSuccessRepresentation(input, 200, http.Header{}, body, input.CredentialSecrets)
		if (err != nil) != isCredential {
			t.Errorf("unary rejects metadata or accepts credential: shouldReject=%v err=%v", isCredential, err)
		}
		executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
			header := http.Header{"Content-Type": {"text/event-stream"}}
			if err := sink(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: 200, Header: header}); err != nil {
				t.Fatal(err)
			}
			for i, chunk := range [][]byte{redactionBoundaryChat(t, map[string]any{"content": token}, "stop"), []byte("data: [DONE]\n\n")} {
				if sink(execution.StreamEvent{Sequence: uint64(i + 2), Kind: execution.StreamEventData, Data: chunk}) != nil {
					break
				}
			}
			return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: header}
		}}
		writer := httptest.NewRecorder()
		result := NewExecutionForwarder(executor).ForwardStream(context.Background(), input, writer)
		if (result.Err != nil) != isCredential || strings.Contains(writer.Body.String(), value) == isCredential {
			t.Fatal("SSE credential and metadata boundary differs from unary")
		}
		ws := newWebsocketRedactionOutput(credentialSafeRestore(c.RestoreText, restorationCredentialSecrets(input)), false)
		_, err = ws.Push(websocketRedactionEvent(t, "response.output_text.delta", "", "delta", token))
		if (err != nil) != isCredential {
			t.Fatal("WebSocket credential and metadata boundary differs")
		}
	}
}
func TestCredentialSafeRestoreWithoutSecrets(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("business-value")
	if err != nil {
		t.Fatal(err)
	}
	got, err := credentialSafeRestore(c.RestoreText, nil)(token)
	if err != nil || got != "business-value" {
		t.Fatal("empty credential set broke restoration")
	}
}
