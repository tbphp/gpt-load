package antigravity

import (
	"reflect"
	"testing"
)

func TestParseCredentialJSONProducesCanonicalAntigravityCredential(t *testing.T) {
	credential, err := ParseCredentialJSON([]byte(`{
		"type":"antigravity",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_id":"google-account-one",
		"email":"owner@example.com",
		"project_id":"project-one",
		"expired":"2030-01-01T00:00:00Z"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccountID != "google-account-one" || credential.ProjectID != "project-one" {
		t.Fatalf("credential = %#v", credential)
	}
	if got := credential.SecretValues(); !reflect.DeepEqual(got, []string{
		"access-secret", "refresh-secret", "google-account-one", "owner@example.com", "project-one",
	}) {
		t.Fatalf("SecretValues() = %q", got)
	}
}
