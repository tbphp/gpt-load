package codex

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

func TestCredentialRoundTripKeepsCPACompatibleSchema(t *testing.T) {
	raw := []byte(`{
		"type":"codex",
		"id_token":"id-token",
		"access_token":"access-token",
		"refresh_token":"refresh-token",
		"account_id":"account-1",
		"email":"owner@example.com",
		"expired":"2026-08-15T00:00:00Z",
		"last_refresh":"2026-08-14T00:00:00Z"
	}`)
	credential, err := ParseCredentialJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := MarshalCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Credential
	if err := json.Unmarshal(canonical, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, credential) {
		t.Fatalf("round trip = %#v, want %#v", roundTrip, credential)
	}
	if got, want := credential.SecretValues(), []string{"access-token", "refresh-token", "id-token"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("secret values = %#v, want %#v", got, want)
	}
}

func TestNormalizeUpstreamErrorPreservesObservationStatus(t *testing.T) {
	err := normalizeUpstreamError(&cpaembedded.UpstreamHTTPError{Operation: "usage", StatusCode: 401})
	var upstream *UpstreamHTTPError
	if !errors.As(err, &upstream) || upstream.StatusCode != 401 || upstream.Operation != "usage" {
		t.Fatalf("normalized error = %#v / %v", upstream, err)
	}
}

func TestExecutorCountsTokensWithoutCredential(t *testing.T) {
	response, err := NewExecutor().CountTokens(t.Context(), "local-token-count", Credential{}, ExecuteRequest{
		Model: "gpt-5", Format: "openai-response",
		Payload: []byte(`{"model":"gpt-5","input":"hello"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		InputTokens int64 `json:"input_tokens"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil || result.InputTokens <= 0 {
		t.Fatalf("token count = %s / %v", response.Payload, err)
	}
}
