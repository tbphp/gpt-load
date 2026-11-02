package devin

import (
	"encoding/json"
	"errors"
	"testing"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"

	providerobservation "gpt-load/internal/subscription/providers/observation"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestDevinCredentialAndObservationKeepUnknownValues(t *testing.T) {
	driver := &devinDriver{}
	current, err := driver.Parse([]byte(`{"type":"devin","session_token":"devin-session-token$secret","user_id":"account","email":"devin@example.test","plan":"Pro"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, known := current.ExpiresAt(); known {
		t.Fatal("session token must not invent expiry")
	}
	zero, remaining := int64(0), int64(75)
	for _, quotas := range []bool{false, true} {
		observation := cpaembedded.DevinAccountObservation{Credential: cpaembedded.DevinCredential{Plan: "Pro", Email: "devin@example.test"}}
		if quotas {
			observation.DailyRemaining, observation.WeeklyRemaining = &zero, &remaining
		}
		raw, err := normalizeObservation(observation)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot providerobservation.Snapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatal(err)
		}
		if len(snapshot.QuotaWindows) != 2 {
			t.Fatal("missing quota windows")
		}
		if !quotas && (snapshot.QuotaWindows[0].State != "unknown" || snapshot.QuotaWindows[0].Remaining != nil) {
			t.Fatal("unknown quota became exhausted")
		}
		if quotas && (snapshot.QuotaWindows[0].State != "exhausted" || *snapshot.QuotaWindows[1].Used != 25) {
			t.Fatal("incorrect quota normalization")
		}
	}
}

func TestDevinRefreshIdentityAndFailureClassification(t *testing.T) {
	driver := &devinDriver{}
	credential := func(org string) subscriptionruntime.Credential {
		value, err := driver.Parse([]byte(`{"type":"devin","session_token":"secret","user_id":"account","org_id":"` + org + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if !driver.MatchesRefreshIdentity(credential(""), credential("org")) || driver.MatchesRefreshIdentity(credential("org"), credential("")) || driver.MatchesRefreshIdentity(credential("org"), credential("other")) {
		t.Fatal("refresh identity rules changed")
	}
	for _, test := range []struct {
		err  error
		want subscriptionruntime.RefreshFailure
	}{
		{errors.New("temporary status read failure"), subscriptionruntime.RefreshFailureRetryable},
		{cpaembedded.ErrDevinCredentialIdentityChanged, subscriptionruntime.RefreshFailureIdentityChanged},
		{&cpaembedded.DevinHTTPError{StatusCode: 401}, subscriptionruntime.RefreshFailureReauthorizationRequired},
		{&cpaembedded.DevinHTTPError{StatusCode: 429}, subscriptionruntime.RefreshFailureRetryable},
		{&cpaembedded.DevinHTTPError{StatusCode: 503}, subscriptionruntime.RefreshFailureRetryable},
	} {
		if got := driver.ClassifyRefreshFailure(test.err); got.Kind != test.want {
			t.Fatalf("refresh failure=%s want=%s", got.Kind, test.want)
		}
	}
}
