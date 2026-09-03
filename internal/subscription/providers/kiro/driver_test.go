package kiro

import (
	"testing"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	"gpt-load/internal/channel/modules"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestKiroDriverDeclaresDeviceOAuthImporterAndSelfDiscovery(t *testing.T) {
	driver := newKiroDriver()
	if _, ok := any(driver).(subscriptionruntime.DeviceAuthorizationDriver); !ok {
		t.Fatal("Kiro driver does not implement DeviceAuthorizationDriver")
	}
	if _, ok := any(driver).(subscriptionruntime.CredentialFileImporter); !ok {
		t.Fatal("Kiro driver does not implement CredentialFileImporter")
	}
	if _, ok := any(driver).(subscriptionruntime.SelfDiscoveryDriver); !ok {
		t.Fatal("Kiro driver does not implement SelfDiscoveryDriver")
	}
	implementations := Implementations()
	if len(implementations.Drivers) != 1 || implementations.Drivers[0].ID() != modules.KiroSubscriptionDriver ||
		len(implementations.ModelDiscoveries) != 1 || implementations.ModelDiscoveries[0].ID() != modules.KiroModelDiscovery ||
		len(implementations.QuotaObservations) != 1 || implementations.QuotaObservations[0].ID() != modules.KiroQuotaObservation {
		t.Fatalf("implementations = %#v", implementations)
	}
}

// TestKiroDriverSelfDiscoveryIsResilientAndProducesWellFormedCredential verifies
// the self-discovery wiring. A machine without a signed-in Kiro account must
// report no-account gracefully (a nil error and found=false) so the control
// layer can fall through to OAuth; a machine with an account must yield a ready
// runtime credential with a non-empty canonical payload.
func TestKiroDriverSelfDiscoveryIsResilientAndProducesWellFormedCredential(t *testing.T) {
	driver := newKiroDriver()
	credential, found, err := driver.DiscoverLocalCredential(t.Context())
	if err != nil {
		t.Fatalf("DiscoverLocalCredential() error = %v", err)
	}
	if !found {
		return
	}
	if len(credential.Canonical()) == 0 {
		t.Fatal("discovered credential has empty canonical payload")
	}
	if _, err := cpaembedded.ParseKiroCredentialJSON(credential.Canonical()); err != nil {
		t.Fatalf("discovered credential canonical is not parseable language: %v", err)
	}
	// A locally discovered Kiro token surfaces its stable SSO client binding
	// (clientIdHash) as the account identity, so the runtime credential must
	// carry a non-empty identity and known expiry for provisioning/consumption.
	if credential.Identity() == "" {
		t.Fatal("discovered credential has no stable account identity")
	}
	if !credential.Account().ExpiresAtKnown {
		t.Fatal("discovered credential has no known expiry")
	}
	// A locally discovered Kiro token resolves its management-plane profileArn
	// from the Kiro desktop app's persisted profile (when present). If the
	// profile file exists with an ARN, the discovered credential must carry it
	// so model discovery can reach the management plane.
	if arn := cpaembedded.ReadKiroProfileArn(cpaembedded.KiroProfileArnPath()); arn != "" {
		parsed, err := ParseCredentialJSON(credential.Canonical())
		if err != nil {
			t.Fatalf("ParseCredentialJSON error = %v", err)
		}
		if parsed.ProfileARN == "" {
			t.Fatal("discovered credential has an available profileArn but did not resolve it")
		}
	}
}

// TestKiroDriverModelDiscoveryDegradesGracefullyWithoutProfile verifies that a
// self-discovered Kiro credential (empty profileArn, tokens only) no longer turns
// model discovery into a 502. The driver must fall back to the static Kiro model
// catalog and return a nil error instead of surfacing ErrModelDiscoveryUnavailable.
func TestKiroDriverModelDiscoveryDegradesGracefullyWithoutProfile(t *testing.T) {
	driver := newKiroDriver()
	canonical, err := MarshalCredential(Credential{
		Type:         string(cpaembedded.ProviderKiro),
		AuthKind:     string(cpaembedded.KiroAuthSocial),
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		Expire:       time.Now().Add(time.Hour).Format(time.RFC3339),
		AccountID:    "stable-client-id-hash",
		Region:       cpaembedded.DefaultKiroRegion,
	})
	if err != nil {
		t.Fatalf("MarshalCredential error = %v", err)
	}
	value, err := ParseCredentialJSON(canonical)
	if err != nil {
		t.Fatalf("ParseCredentialJSON error = %v", err)
	}
	credential := kiroRuntimeCredential(value, canonical)
	discovered, err := driver.DiscoverModels(t.Context(), credential)
	if err != nil {
		t.Fatalf("DiscoverModels with empty profileArn returned an error: %v", err)
	}
	if discovered == nil {
		t.Fatal("DiscoverModels returned nil catalog")
	}
}
