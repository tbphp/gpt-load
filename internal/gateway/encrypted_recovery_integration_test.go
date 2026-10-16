package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/protocol"
)

// encryptedRecoveryBodies records the request bodies an upstream observed.
type encryptedRecoveryBodies struct {
	mu     sync.Mutex
	bodies []string
	keys   []string
}

func (recorder *encryptedRecoveryBodies) add(key, body string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.keys = append(recorder.keys, key)
	recorder.bodies = append(recorder.bodies, body)
}

func (recorder *encryptedRecoveryBodies) snapshot() ([]string, []string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.keys...), append([]string(nil), recorder.bodies...)
}

// TestEncryptedContentRecoveryReplaysSameCredentialWithSanitizedBody verifies
// the recovery contract: the rejected encrypted state is stripped and the
// request is replayed on the credential that received the rejection, instead
// of failing over to a credential that could never decrypt the state.
func TestEncryptedContentRecoveryReplaysSameCredentialWithSanitizedBody(t *testing.T) {
	observed := &encryptedRecoveryBodies{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAllString(t, r)
		observed.add(r.Header.Get("Authorization"), body)
		w.Header().Set("Content-Type", "application/json")
		// The first attempt carries encrypted reasoning and is rejected; the
		// replay has the encrypted state stripped and succeeds.
		if strings.Contains(body, "encrypted_content") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"The encrypted content for item rs_1 could not be verified. Reason: Encrypted content could not be decrypted or parsed.","type":"invalid_request_error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp_ok","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer upstream.Close()

	engine, _ := newDialectGatewayEngine(t, protocol.OpenAIResponses, "model-a",
		dialect.NewSet(dialect.NewOpenAIResponses()),
		dialectGatewayGroup{
			id: 1, name: "codex", channelID: channel.OpenAI,
			params: openAIBaseURLParams(t, upstream.URL), apiKeys: []string{"sk-one", "sk-two"},
		},
	)
	requestBody := `{"model":"model-a","stream":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},{"type":"reasoning","id":"rs_1","encrypted_content":"AAAA"}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	keys, bodies := observed.snapshot()
	if response.Code != http.StatusOK {
		t.Fatalf("response=%d body=%s attempts=%d", response.Code, response.Body.String(), len(keys))
	}
	if len(keys) != 2 {
		t.Fatalf("expected exactly two attempts (reject + sanitized replay), got %d: %v", len(keys), keys)
	}
	if keys[0] != keys[1] {
		t.Fatalf("replay must pin the same credential, got %v", keys)
	}
	if !strings.Contains(bodies[0], "encrypted_content") {
		t.Fatalf("first attempt should carry the encrypted state: %s", bodies[0])
	}
	if strings.Contains(bodies[1], "encrypted_content") {
		t.Fatalf("replay must strip the encrypted state: %s", bodies[1])
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(bodies[1]), &decoded); err != nil {
		t.Fatalf("replay body is not valid JSON: %v", err)
	}
	items, ok := decoded["input"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected the emptied reasoning item to be dropped, got %#v", decoded["input"])
	}
}

// TestEncryptedContentRecoveryExhaustionReturnsTerminal verifies that a
// second rejection of the same signal ends the request with the explicit
// recovery terminal instead of another deterministic retry.
func TestEncryptedContentRecoveryExhaustionReturnsTerminal(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = readAllString(t, r)
		mu.Lock()
		attempts++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"The encrypted content for item rs_1 could not be verified.","type":"invalid_request_error"}}`))
	}))
	defer upstream.Close()

	engine, _ := newDialectGatewayEngine(t, protocol.OpenAIResponses, "model-a",
		dialect.NewSet(dialect.NewOpenAIResponses()),
		dialectGatewayGroup{
			id: 1, name: "codex", channelID: channel.OpenAI,
			params: openAIBaseURLParams(t, upstream.URL), apiKeys: []string{"sk-one", "sk-two"},
		},
	)
	requestBody := `{"model":"model-a","stream":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},{"type":"reasoning","id":"rs_1","encrypted_content":"AAAA"}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	mu.Lock()
	attempted := attempts
	mu.Unlock()
	if attempted != 2 {
		t.Fatalf("expected reject + one sanitized replay, got %d attempts", attempted)
	}
	if !strings.Contains(response.Body.String(), "encrypted_content_recovery_exhausted") {
		t.Fatalf("expected the recovery terminal, got %d %s", response.Code, response.Body.String())
	}
}

func readAllString(t *testing.T, request *http.Request) string {
	t.Helper()
	var builder strings.Builder
	buffer := make([]byte, 4096)
	for {
		read, err := request.Body.Read(buffer)
		if read > 0 {
			builder.Write(buffer[:read])
		}
		if err != nil {
			break
		}
	}
	return builder.String()
}

func openAIBaseURLParams(t *testing.T, baseURL string) json.RawMessage {
	t.Helper()
	params, err := json.Marshal(map[string]string{"base_url": baseURL})
	if err != nil {
		t.Fatalf("encode channel params: %v", err)
	}
	return params
}
