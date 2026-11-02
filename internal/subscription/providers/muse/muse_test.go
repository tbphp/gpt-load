package muse

import (
	"testing"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestMuseDCAExpiryDoesNotExpireMintedKey(t *testing.T) {
	driver := &museDriver{}
	credential, err := driver.Parse([]byte(`{"type":"muse","access_token":"minted-key","dca_token":"dca:test","email":"MUSE@Example.com","dca_expired":"2000-01-01T00:00:00Z","plan_type":"High Usage","last_refresh":"2026-11-02T01:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, expires := credential.ExpiresAt(); expires || credential.Account().ExpiresAtKnown {
		t.Fatal("DCA expiry was applied to the request key")
	}
	if credential.Identity() != "muse@example.com" || credential.Account().PlanType != "High Usage" || !credential.Account().LastRefreshKnown {
		t.Fatal("account metadata missing")
	}
}

func TestMuseRefreshFailureClassification(t *testing.T) {
	driver := &museDriver{}
	for _, test := range []struct {
		err  error
		want subscriptionruntime.RefreshFailure
	}{
		{cpaembedded.ErrMuseCredentialIdentityChanged, subscriptionruntime.RefreshFailureIdentityChanged},
		{&cpaembedded.MuseHTTPError{StatusCode: 401}, subscriptionruntime.RefreshFailureReauthorizationRequired},
		{&cpaembedded.MuseHTTPError{StatusCode: 429, RetryAfter: time.Minute}, subscriptionruntime.RefreshFailureRetryable},
		{&cpaembedded.MuseHTTPError{StatusCode: 400, Code: "invalid_client"}, subscriptionruntime.RefreshFailureOutcomeUnknown},
	} {
		if got := driver.ClassifyRefreshFailure(test.err); got.Kind != test.want {
			t.Fatalf("classification = %v, want %v", got.Kind, test.want)
		}
	}
}
