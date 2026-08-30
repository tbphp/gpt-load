package codex

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"gpt-load/internal/channel/modules"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestCodexDriverProducesProviderNeutralCredential(t *testing.T) {
	driver := newCodexDriver()
	credential, err := driver.Parse([]byte(`{
		"type":"codex",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_id":"account-one",
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
	canonical := credential.Canonical()
	canonical[0] = '['
	if reparsed, parseErr := driver.Parse(credential.Canonical()); parseErr != nil || reparsed.Identity() != "account-one" {
		t.Fatalf("credential canonical was mutable: %#v, %v", reparsed, parseErr)
	}
}

func TestCodexImplementationsExposeCompleteCapabilities(t *testing.T) {
	implementations := Implementations()
	if len(implementations.Drivers) != 1 || implementations.Drivers[0].ID() != modules.CodexSubscriptionDriver ||
		len(implementations.ModelDiscoveries) != 1 ||
		implementations.ModelDiscoveries[0].ID() != modules.CodexModelDiscovery ||
		len(implementations.QuotaObservations) != 1 ||
		implementations.QuotaObservations[0].ID() != modules.CodexQuotaObservation ||
		len(implementations.ResetCreditActions) != 1 ||
		implementations.ResetCreditActions[0].ID() != modules.CodexResetCreditAction {
		t.Fatalf("implementations = %#v", implementations)
	}
}

func TestCodexDriverClassifiesRefreshFailures(t *testing.T) {
	driver := newCodexDriver()
	if got := driver.ClassifyRefreshFailure(ErrCredentialIdentityChanged); got.Kind != subscriptionruntime.RefreshFailureIdentityChanged {
		t.Fatalf("identity failure = %#v", got)
	}
	if got := driver.ClassifyRefreshFailure(&TokenEndpointError{StatusCode: http.StatusBadRequest, Code: "invalid_grant"}); got.Kind != subscriptionruntime.RefreshFailureReauthorizationRequired {
		t.Fatalf("invalid grant = %#v", got)
	}
	if got := driver.ClassifyRefreshFailure(&TokenEndpointError{StatusCode: http.StatusTooManyRequests, Code: "rate_limit_exceeded"}); got.Kind != subscriptionruntime.RefreshFailureRetryable || got.StatusCode != http.StatusTooManyRequests ||
		got.OAuthCode != "rate_limit_exceeded" {
		t.Fatalf("temporary token endpoint failure = %#v", got)
	}
	if got := driver.ClassifyRefreshFailure(errors.New("connection reset")); got.Kind != subscriptionruntime.RefreshFailureOutcomeUnknown {
		t.Fatalf("ambiguous failure = %#v", got)
	}
}
