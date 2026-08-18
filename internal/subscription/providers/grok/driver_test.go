package grok

import (
	"errors"
	"reflect"
	"testing"

	"gpt-load/internal/channel/modules"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestGrokDriverProducesProviderNeutralCredential(t *testing.T) {
	driver := newGrokDriver()
	credential, err := driver.Parse([]byte(`{
		"type":"grok","access_token":"access-secret","refresh_token":"refresh-secret",
		"id_token":"id-secret","account_id":"account-1","email":"owner@example.com",
		"expired":"2030-01-01T00:00:00Z","token_endpoint":"https://auth.x.ai/oauth/token"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if credential.Identity() != "account-1" || credential.Account().Email != "owner@example.com" ||
		!credential.Account().ExpiresAtKnown {
		t.Fatalf("credential = %#v / %#v", credential.Identity(), credential.Account())
	}
	if got := credential.SecretValues(); !reflect.DeepEqual(got, []string{
		"access-secret", "refresh-secret", "id-secret", "account-1", "owner@example.com",
	}) {
		t.Fatalf("SecretValues() = %q", got)
	}
}

func TestGrokDriverDeclaresDeviceOAuthImporterAndModelDiscovery(t *testing.T) {
	driver := newGrokDriver()
	if _, ok := any(driver).(subscriptionruntime.DeviceAuthorizationDriver); !ok {
		t.Fatal("Grok driver does not implement DeviceAuthorizationDriver")
	}
	if _, ok := any(driver).(subscriptionruntime.CredentialFileImporter); !ok {
		t.Fatal("Grok driver does not implement CredentialFileImporter")
	}
	implementations := Implementations()
	if len(implementations.Drivers) != 1 || implementations.Drivers[0].ID() != modules.GrokSubscriptionDriver ||
		len(implementations.ModelDiscoveries) != 1 || implementations.ModelDiscoveries[0].ID() != modules.GrokModelDiscovery ||
		len(implementations.QuotaObservations) != 0 || len(implementations.ResetCreditActions) != 0 {
		t.Fatalf("implementations = %#v", implementations)
	}
}

func TestGrokDriverClassifiesRefreshFailures(t *testing.T) {
	driver := newGrokDriver()
	if got := driver.ClassifyRefreshFailure(ErrCredentialIdentityChanged); got != subscriptionruntime.RefreshFailureIdentityChanged {
		t.Fatalf("identity classification = %v", got)
	}
	if got := driver.ClassifyRefreshFailure(&TokenEndpointError{StatusCode: 400, Code: "invalid_grant"}); got != subscriptionruntime.RefreshFailureReauthorizationRequired {
		t.Fatalf("invalid_grant classification = %v", got)
	}
	if got := driver.ClassifyRefreshFailure(errors.New("network unavailable")); got != subscriptionruntime.RefreshFailureOutcomeUnknown {
		t.Fatalf("network classification = %v", got)
	}
}

func TestGrokDeviceStateRejectsUnknownOrIncompleteFields(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"device_code":"code","token_endpoint":"https://auth.x.ai/token","userinfo_endpoint":"https://auth.x.ai/userinfo","poll_interval_seconds":5,"proxy":"bad"}`),
		[]byte(`{"device_code":"code","poll_interval_seconds":5}`),
	} {
		if _, err := unmarshalDeviceState(raw); err == nil {
			t.Fatalf("unmarshalDeviceState(%s) succeeded", raw)
		}
	}
}
