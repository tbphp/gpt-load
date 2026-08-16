package subscriptionruntime

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/claude"
)

func TestClaudeDriverProducesProviderNeutralCredential(t *testing.T) {
	driver := newClaudeDriver()
	credential, err := driver.Parse([]byte(`{
		"type":"claude",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_uuid":"account-one",
		"organization_uuid":"org-one",
		"email":"owner@example.com"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if credential.Identity() != "account-one" || credential.Account().Email != "owner@example.com" {
		t.Fatalf("credential metadata = %q %#v", credential.Identity(), credential.Account())
	}
	if got := credential.SecretValues(); !reflect.DeepEqual(got[:2], []string{"access-secret", "refresh-secret"}) {
		t.Fatalf("SecretValues() = %q", got)
	}
	if !strings.Contains(string(credential.Canonical()), `"claude_device_ids":["`) {
		t.Fatalf("canonical credential has no durable device ID: %s", credential.Canonical())
	}
}

func TestClaudeDriverDeclaresFixedCallbackAndEncryptedPKCEState(t *testing.T) {
	driver := newClaudeDriver()
	authorization, err := driver.BeginAuthorization()
	if err != nil {
		t.Fatal(err)
	}
	callback, local := driver.LocalCallback()
	if !local || callback.RedirectURI != claude.RedirectURI || authorization.State == "" || len(authorization.DriverState) == 0 {
		t.Fatalf("authorization = %#v callback=%#v/%t", authorization, callback, local)
	}
	if strings.Contains(authorization.URL, string(authorization.DriverState)) {
		t.Fatal("authorization URL exposed serialized driver state")
	}
}

func TestClaudeDriverClassifiesRefreshFailures(t *testing.T) {
	driver := newClaudeDriver()
	if got := driver.ClassifyRefreshFailure(claude.ErrCredentialIdentityChanged); got != RefreshFailureIdentityChanged {
		t.Fatalf("identity failure = %v", got)
	}
	if got := driver.ClassifyRefreshFailure(claude.ErrOrganizationIdentityChanged); got != RefreshFailureIdentityChanged {
		t.Fatalf("organization failure = %v", got)
	}
	if got := driver.ClassifyRefreshFailure(&claude.TokenEndpointError{StatusCode: 400, Code: "invalid_grant"}); got != RefreshFailureReauthorizationRequired {
		t.Fatalf("invalid grant = %v", got)
	}
	if got := driver.ClassifyRefreshFailure(errors.New("temporary failure")); got != RefreshFailureOutcomeUnknown {
		t.Fatalf("temporary failure = %v", got)
	}
}

func TestClaudeImplementationsExposeCompleteReadOnlyCapabilities(t *testing.T) {
	implementations := ClaudeImplementations()
	if len(implementations.Drivers) != 1 || implementations.Drivers[0].ID() != modules.ClaudeSubscriptionDriver ||
		len(implementations.ModelDiscoveries) != 1 ||
		implementations.ModelDiscoveries[0].ID() != modules.ClaudeModelDiscovery ||
		len(implementations.QuotaObservations) != 1 ||
		implementations.QuotaObservations[0].ID() != modules.ClaudeQuotaObservation ||
		len(implementations.ResetCreditActions) != 0 {
		t.Fatalf("implementations = %#v", implementations)
	}
}
