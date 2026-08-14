package codex

import (
	"encoding/json"
	"reflect"
	"testing"
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
