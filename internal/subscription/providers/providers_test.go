package providers_test

import (
	"context"
	"reflect"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/channel/modules"
	subscriptionproviders "gpt-load/internal/subscription/providers"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestRuntimeCompilesAllSubscriptionProviderCapabilities(t *testing.T) {
	runtime, err := subscriptionruntime.NewRuntime(channel.NewRegistry(), subscriptionproviders.Implementations()...)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := runtime.ChannelIDs(), []channel.ID{channel.Antigravity, channel.Claude, channel.Codex, channel.Grok, channel.Kiro}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ChannelIDs() = %v, want %v", got, want)
	}
	tests := []struct {
		channelID     channel.ID
		driverID      string
		discoveryID   string
		observationID string
		actionID      string
	}{
		{channel.Codex, string(modules.CodexSubscriptionDriver), string(modules.CodexModelDiscovery), string(modules.CodexQuotaObservation), string(modules.CodexResetCreditAction)},
		{channel.Claude, string(modules.ClaudeSubscriptionDriver), string(modules.ClaudeModelDiscovery), string(modules.ClaudeQuotaObservation), ""},
		{channel.Antigravity, string(modules.AntigravitySubscriptionDriver), string(modules.AntigravityModelDiscovery), string(modules.AntigravityQuotaObservation), ""},
		{channel.Grok, string(modules.GrokSubscriptionDriver), string(modules.GrokModelDiscovery), string(modules.GrokQuotaObservation), ""},
		{channel.Kiro, string(modules.KiroSubscriptionDriver), string(modules.KiroModelDiscovery), string(modules.KiroQuotaObservation), ""},
	}

	for _, test := range tests {
		t.Run(string(test.channelID), func(t *testing.T) {
			driver, ok := runtime.Driver(test.channelID)
			if !ok || string(driver.ID()) != test.driverID {
				t.Fatalf("driver = %#v/%t", driver, ok)
			}
			discovery, ok := runtime.ModelDiscovery(test.channelID)
			if !ok || string(discovery.ID()) != test.discoveryID {
				t.Fatalf("discovery = %#v/%t", discovery, ok)
			}
			observation, ok := runtime.QuotaObservation(test.channelID)
			if test.observationID == "" && ok || test.observationID != "" && (!ok || string(observation.ID()) != test.observationID) {
				t.Fatalf("observation = %#v/%t", observation, ok)
			}
			action, ok := runtime.ResetCreditAction(test.channelID)
			if test.actionID == "" && ok || test.actionID != "" && (!ok || string(action.ID()) != test.actionID) {
				t.Fatalf("action = %#v/%t", action, ok)
			}
		})
	}
	if _, ok := runtime.Driver(channel.OpenAI); ok {
		t.Fatal("Driver(openai) unexpectedly resolved")
	}
	if device, ok := runtime.DeviceAuthorization(channel.Grok); !ok || device.ID() != modules.GrokSubscriptionDriver {
		t.Fatalf("Grok device authorization = %#v/%t", device, ok)
	}
	if _, ok := runtime.BrowserAuthorization(channel.Grok); ok {
		t.Fatal("Grok unexpectedly resolves browser OAuth")
	}
}

type browserlessDriver struct{ subscriptionruntime.Driver }

func TestRuntimeRejectsDeclaredBrowserAuthorizationWithoutDriverSupport(t *testing.T) {
	implementations := subscriptionproviders.Implementations()
	implementations[0].Drivers[0] = browserlessDriver{Driver: implementations[0].Drivers[0]}
	if _, err := subscriptionruntime.NewRuntime(channel.NewRegistry(), implementations...); err == nil {
		t.Fatal("NewRuntime() accepted browser OAuth without driver support")
	}
}

type importingDriver struct {
	subscriptionruntime.BrowserAuthorizationDriver
	imported bool
}

func (driver *importingDriver) ImportCredential(context.Context, []byte) (subscriptionruntime.Credential, error) {
	driver.imported = true
	return driver.BrowserAuthorizationDriver.Parse([]byte(`{
		"type":"antigravity",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_id":"google-account-one",
		"email":"owner@example.com",
		"project_id":"project-one",
		"expired":"2030-01-01T00:00:00Z"
	}`))
}

func TestRuntimeImportsCredentialThroughOptionalImporter(t *testing.T) {
	implementations := subscriptionproviders.Implementations()
	base, ok := implementations[2].Drivers[0].(subscriptionruntime.BrowserAuthorizationDriver)
	if !ok {
		t.Fatal("Antigravity driver has no browser authorization support")
	}
	importer := &importingDriver{BrowserAuthorizationDriver: base}
	implementations[2].Drivers[0] = importer
	runtime, err := subscriptionruntime.NewRuntime(channel.NewRegistry(), implementations...)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := runtime.ImportCredential(t.Context(), channel.Antigravity, []byte(`{"not":"canonical"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !importer.imported || credential.Identity() != "google-account-one" {
		t.Fatalf("imported=%t credential=%#v", importer.imported, credential)
	}
}
