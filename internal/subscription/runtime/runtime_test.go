package subscriptionruntime

import (
	"context"
	"reflect"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
)

func TestRuntimeCompilesSubscriptionCapabilitiesFromChannelBindings(t *testing.T) {
	runtime, err := NewRuntime(channel.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := runtime.ChannelIDs(), []channel.ID{channel.Codex}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ChannelIDs() = %v, want %v", got, want)
	}
	driver, ok := runtime.Driver(channel.Codex)
	if !ok || driver.ID() != modules.CodexSubscriptionDriver {
		t.Fatalf("Driver(codex) = %#v, %t", driver, ok)
	}
	if _, ok := runtime.Driver(channel.OpenAI); ok {
		t.Fatal("Driver(openai) unexpectedly resolved")
	}
	discovery, ok := runtime.ModelDiscovery(channel.Codex)
	if !ok || discovery.ID() != modules.CodexModelDiscovery {
		t.Fatalf("ModelDiscovery(codex) = %#v, %t", discovery, ok)
	}
	observation, ok := runtime.QuotaObservation(channel.Codex)
	if !ok || observation.ID() != modules.CodexQuotaObservation {
		t.Fatalf("QuotaObservation(codex) = %#v, %t", observation, ok)
	}
	action, ok := runtime.ResetCreditAction(channel.Codex)
	if !ok || action.ID() != modules.CodexResetCreditAction {
		t.Fatalf("ResetCreditAction(codex) = %#v, %t", action, ok)
	}
}

func TestRuntimeFailsClosedWhenBoundImplementationIsMissing(t *testing.T) {
	_, err := compileRuntime(channel.NewRegistry(), nil, nil, nil, nil)
	if err == nil {
		t.Fatal("compileRuntime() succeeded without the Codex driver")
	}
}

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

type duplicateDriver struct{ id spec.SubscriptionDriverID }

func (driver duplicateDriver) ID() spec.SubscriptionDriverID { return driver.id }
func (duplicateDriver) Parse([]byte) (Credential, error)     { return Credential{}, nil }
func (duplicateDriver) Refresh(context.Context, Credential) (Credential, error) {
	return Credential{}, nil
}
func (duplicateDriver) ClassifyRefreshFailure(error) RefreshFailure {
	return RefreshFailureOutcomeUnknown
}

func TestRuntimeRejectsDuplicateImplementationIDs(t *testing.T) {
	_, err := compileRuntime(
		channel.NewRegistry(),
		[]Driver{
			duplicateDriver{id: modules.CodexSubscriptionDriver},
			duplicateDriver{id: modules.CodexSubscriptionDriver},
		},
		nil,
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("compileRuntime() accepted duplicate driver IDs")
	}
}
