package claude

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/channel/modules"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestClaudeDriverProducesProviderNeutralCredential(t *testing.T) {
	driver := newClaudeDriver()
	credential, err := driver.Parse([]byte(`{
		"type":"claude",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_uuid":"account-one",
		"organization_uuid":"org-one",
		"email":"owner@example.com",
		"expired":"2030-01-01T00:00:00Z"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if credential.Identity() != "account-one" || credential.Account().Email != "owner@example.com" ||
		!credential.Account().ExpiresAtKnown {
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
	if !local || callback.RedirectURI != RedirectURI || authorization.State == "" || len(authorization.DriverState) == 0 {
		t.Fatalf("authorization = %#v callback=%#v/%t", authorization, callback, local)
	}
	if strings.Contains(authorization.URL, string(authorization.DriverState)) {
		t.Fatal("authorization URL exposed serialized driver state")
	}
}

func TestClaudeDriverClassifiesRefreshFailures(t *testing.T) {
	driver := newClaudeDriver()
	if got := driver.ClassifyRefreshFailure(ErrCredentialIdentityChanged); got.Kind != subscriptionruntime.RefreshFailureIdentityChanged {
		t.Fatalf("identity failure = %#v", got)
	}
	if got := driver.ClassifyRefreshFailure(ErrOrganizationIdentityChanged); got.Kind != subscriptionruntime.RefreshFailureIdentityChanged {
		t.Fatalf("organization failure = %#v", got)
	}
	if got := driver.ClassifyRefreshFailure(&TokenEndpointError{StatusCode: 400, Code: "invalid_grant"}); got.Kind != subscriptionruntime.RefreshFailureReauthorizationRequired {
		t.Fatalf("invalid grant = %#v", got)
	}
	if got := driver.ClassifyRefreshFailure(&TokenEndpointError{StatusCode: http.StatusServiceUnavailable, Code: "temporarily_unavailable"}); got.Kind != subscriptionruntime.RefreshFailureRetryable || got.StatusCode != http.StatusServiceUnavailable ||
		got.OAuthCode != "temporarily_unavailable" {
		t.Fatalf("temporary token endpoint failure = %#v", got)
	}
	if got := driver.ClassifyRefreshFailure(errors.New("temporary failure")); got.Kind != subscriptionruntime.RefreshFailureOutcomeUnknown {
		t.Fatalf("ambiguous failure = %#v", got)
	}
}

func TestClaudeImplementationsExposeCompleteReadOnlyCapabilities(t *testing.T) {
	implementations := Implementations()
	if len(implementations.Drivers) != 1 || implementations.Drivers[0].ID() != modules.ClaudeSubscriptionDriver ||
		len(implementations.ModelDiscoveries) != 1 ||
		implementations.ModelDiscoveries[0].ID() != modules.ClaudeModelDiscovery ||
		len(implementations.QuotaObservations) != 1 ||
		implementations.QuotaObservations[0].ID() != modules.ClaudeQuotaObservation ||
		len(implementations.ResetCreditActions) != 0 {
		t.Fatalf("implementations = %#v", implementations)
	}
}
