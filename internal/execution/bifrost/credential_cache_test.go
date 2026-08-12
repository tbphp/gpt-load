package bifrost

import (
	"encoding/json"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
)

func TestBedrockAssumeRoleSessionSeparatesCredentialIdentityGeneration(t *testing.T) {
	registry := channel.NewRegistry()
	firstCredential, err := registry.ValidateCredential(channel.AWSBedrock, json.RawMessage(
		`{"access_key":"AKIA_TEST","secret_key":"secret-one","role_arn":"arn:aws:iam::123456789012:role/test","session_name":"audit-session"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	secondCredential, err := registry.ValidateCredential(channel.AWSBedrock, json.RawMessage(
		`{"access_key":"AKIA_TEST","secret_key":"secret-two","role_arn":"arn:aws:iam::123456789012:role/test","session_name":"audit-session"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	target := json.RawMessage(`{"region":"us-east-1"}`)
	first, firstSecrets, err := directKeyForAttempt(execution.AttemptSpec{
		Credential: execution.NewCredentialSnapshot(1, 10, 100, nil),
	}, channel.ProviderAWSBedrock, target, firstCredential)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := directKeyForAttempt(execution.AttemptSpec{
		Credential: execution.NewCredentialSnapshot(1, 20, 200, nil),
	}, channel.ProviderAWSBedrock, target, secondCredential)
	if err != nil {
		t.Fatal(err)
	}
	firstName := first.BedrockKeyConfig.RoleSessionName.GetValue()
	secondName := second.BedrockKeyConfig.RoleSessionName.GetValue()
	if firstName == secondName {
		t.Fatalf("role session names = %q, want distinct credential cache identities", firstName)
	}
	if len(firstName) > 64 || !strings.HasPrefix(firstName, "audit-session-") {
		t.Fatalf("first role session name = %q", firstName)
	}
	for _, secret := range []string{"secret-one", "secret-two"} {
		if strings.Contains(firstName, secret) || strings.Contains(secondName, secret) {
			t.Fatalf("role session name contains credential secret %q", secret)
		}
	}
	if !containsString(firstSecrets, firstName) {
		t.Fatalf("generated role session name missing from redaction secrets: %#v", firstSecrets)
	}
}

func TestBedrockAssumeRoleSessionUsesValidBoundedDefault(t *testing.T) {
	credential, err := channel.NewRegistry().ValidateCredential(channel.AWSBedrock, json.RawMessage(
		`{"access_key":"AKIA_TEST","secret_key":"secret","role_arn":"arn:aws:iam::123456789012:role/test"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := directKeyForAttempt(execution.AttemptSpec{
		Credential: execution.NewCredentialSnapshot(7, 1, 9, nil),
	}, channel.ProviderAWSBedrock, json.RawMessage(`{"region":"us-east-1"}`), credential)
	if err != nil {
		t.Fatal(err)
	}
	name := key.BedrockKeyConfig.RoleSessionName.GetValue()
	if len(name) > 64 || !strings.HasPrefix(name, "bifrost-session-gl-") {
		t.Fatalf("generated default role session name = %q", name)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
