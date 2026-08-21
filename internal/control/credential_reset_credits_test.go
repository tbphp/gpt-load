package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
)

const resetCreditTestKey = "9f0f4c32-89d2-4bcb-9e19-052940dc2f16"

func TestConsumeCredentialResetCreditRefreshesObservationAndRemainsIdempotent(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	consumeCalls := 0
	setCodexResetCreditConsume(t, fixture.service, func(_ context.Context, _ codex.Credential, redeemRequestID string) (codex.AccountObservation, error) {
		consumeCalls++
		if redeemRequestID != resetCreditTestKey {
			t.Fatalf("redeemRequestID = %q", redeemRequestID)
		}
		return codex.AccountObservation{Payload: []byte(`{
			"code":"reset",
			"credit":{"status":"redeemed","redeemed_at":"2026-08-14T11:00:00Z"},
			"windows_reset":1
		}`)}, nil
	})
	observationCalls := 0
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		observationCalls++
		return codex.AccountObservation{Payload: []byte(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":20,"reset_at":1786720000}},
			"rate_limit_reset_credits":{"available_count":0}
		}`)}, nil
	})

	first, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	if err != nil {
		t.Fatal(err)
	}
	if consumeCalls != 1 || observationCalls != 1 || first.Status != "succeeded" || first.WindowsReset != 1 ||
		first.Observation == nil || first.Observation.State != string(models.CredentialObservationFresh) ||
		first.ObservationPending || second.Status != first.Status || !second.Replayed {
		t.Fatalf("calls=%d/%d first=%#v second=%#v", consumeCalls, observationCalls, first, second)
	}
}

func TestConsumeCredentialResetCreditRefreshesObservationAfterClientCancellation(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 11, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	observationCalls := 0
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		observationCalls++
		usedPercent := 100
		if observationCalls > 1 {
			usedPercent = 0
		}
		return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":%d,"reset_at":%d}}
		}`, usedPercent, now.Add(5*time.Hour).Unix()))}, nil
	})
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		cancel()
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})

	result, err := fixture.service.ConsumeCredentialResetCredit(
		ctx,
		groupID,
		credentialID,
		resetCreditTestKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || result.ObservationPending || result.Observation == nil ||
		result.Observation.State != string(models.CredentialObservationFresh) || observationCalls != 2 {
		t.Fatalf("result/calls = %#v/%d, want succeeded with refreshed observation", result, observationCalls)
	}
	stored, err := fixture.service.GetCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil || stored.State != string(models.CredentialObservationFresh) || stored.Snapshot == nil ||
		len(stored.Snapshot.QuotaWindows) != 1 || stored.Snapshot.QuotaWindows[0].Utilization == nil ||
		*stored.Snapshot.QuotaWindows[0].Utilization != 0 {
		t.Fatalf("stored observation/error = %#v/%v", stored, err)
	}
}

func TestConsumeCredentialResetCreditRunsFollowUpAfterInFlightObservation(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var startOnce sync.Once
	var observationCalls atomic.Int32
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		call := observationCalls.Add(1)
		if call == 1 {
			startOnce.Do(func() { close(firstStarted) })
			<-releaseFirst
			return codex.AccountObservation{Payload: []byte(`{
				"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":90,"reset_at":1786720000}}
			}`)}, nil
		}
		return codex.AccountObservation{Payload: []byte(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":20,"reset_at":1786720000}}
		}`)}, nil
	})
	resetConsumed := make(chan struct{})
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		close(resetConsumed)
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})

	manualDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
		manualDone <- err
	}()
	<-firstStarted
	type resetResult struct {
		response ResetCreditConsumeResponse
		err      error
	}
	resetDone := make(chan resetResult, 1)
	go func() {
		response, err := fixture.service.ConsumeCredentialResetCredit(
			t.Context(), groupID, credentialID, resetCreditTestKey,
		)
		resetDone <- resetResult{response: response, err: err}
	}()
	<-resetConsumed
	close(releaseFirst)
	if err := <-manualDone; err != nil {
		t.Fatalf("manual observation refresh: %v", err)
	}
	result := <-resetDone
	if result.err != nil || result.response.ObservationPending || result.response.Observation == nil ||
		result.response.Observation.Snapshot == nil ||
		len(result.response.Observation.Snapshot.QuotaWindows) != 1 ||
		result.response.Observation.Snapshot.QuotaWindows[0].Utilization == nil ||
		*result.response.Observation.Snapshot.QuotaWindows[0].Utilization != 0.2 ||
		observationCalls.Load() != 2 {
		t.Fatalf("reset result/calls = %#v/%d, want post-reset observation", result, observationCalls.Load())
	}
}

func TestConsumeCredentialResetCreditRunsFollowUpAfterInFlightResetObservation(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	firstObservationStarted := make(chan struct{})
	releaseFirstObservation := make(chan struct{})
	var observationCalls atomic.Int32
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		call := observationCalls.Add(1)
		if call == 1 {
			close(firstObservationStarted)
			<-releaseFirstObservation
			return codex.AccountObservation{Payload: []byte(`{
				"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":90,"reset_at":1786720000}}
			}`)}, nil
		}
		return codex.AccountObservation{Payload: []byte(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":20,"reset_at":1786720000}}
		}`)}, nil
	})
	secondResetConsumed := make(chan struct{})
	var resetCalls atomic.Int32
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		if resetCalls.Add(1) == 2 {
			close(secondResetConsumed)
		}
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	type resetResult struct {
		response ResetCreditConsumeResponse
		err      error
	}
	firstDone := make(chan resetResult, 1)
	go func() {
		response, err := fixture.service.ConsumeCredentialResetCredit(
			t.Context(), groupID, credentialID, resetCreditTestKey,
		)
		firstDone <- resetResult{response: response, err: err}
	}()
	<-firstObservationStarted
	secondDone := make(chan resetResult, 1)
	go func() {
		response, err := fixture.service.ConsumeCredentialResetCredit(
			t.Context(), groupID, credentialID, "9f0f4c32-89d2-4bcb-9e19-052940dc2f20",
		)
		secondDone <- resetResult{response: response, err: err}
	}()
	<-secondResetConsumed
	close(releaseFirstObservation)
	first := <-firstDone
	second := <-secondDone
	if first.err != nil || second.err != nil || second.response.ObservationPending ||
		second.response.Observation == nil || second.response.Observation.Snapshot == nil ||
		len(second.response.Observation.Snapshot.QuotaWindows) != 1 ||
		second.response.Observation.Snapshot.QuotaWindows[0].Utilization == nil ||
		*second.response.Observation.Snapshot.QuotaWindows[0].Utilization != 0.2 ||
		resetCalls.Load() != 2 || observationCalls.Load() != 2 {
		t.Fatalf(
			"reset results/calls = %#v/%#v reset:%d observation:%d",
			first, second, resetCalls.Load(), observationCalls.Load(),
		)
	}
}

func TestConsumeCredentialResetCreditReplayReportsPendingWhileObservationRefreshRuns(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	observationStarted := make(chan struct{})
	releaseObservation := make(chan struct{})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		close(observationStarted)
		<-releaseObservation
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{}}`)}, nil
	})
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	type resetResult struct {
		response ResetCreditConsumeResponse
		err      error
	}
	firstDone := make(chan resetResult, 1)
	go func() {
		response, err := fixture.service.ConsumeCredentialResetCredit(
			t.Context(), groupID, credentialID, resetCreditTestKey,
		)
		firstDone <- resetResult{response: response, err: err}
	}()
	<-observationStarted
	replayed, err := fixture.service.ConsumeCredentialResetCredit(
		t.Context(), groupID, credentialID, resetCreditTestKey,
	)
	if err != nil || !replayed.Replayed || !replayed.ObservationPending {
		t.Fatalf("replayed response/error = %#v/%v, want pending replay", replayed, err)
	}
	close(releaseObservation)
	first := <-firstDone
	if first.err != nil || first.response.ObservationPending || first.response.Observation == nil ||
		first.response.Observation.State != string(models.CredentialObservationFresh) {
		t.Fatalf("first response/error = %#v/%v", first.response, first.err)
	}
}

func TestConsumeCredentialResetCreditRestoresRuntimeHealth(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 11, 30, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	fixture.stats.RecordFailure(credentialID, health.FailureCategoryRateLimited, http.StatusTooManyRequests, now)
	if !fixture.registry.SetCooldown(credentialID, now.Add(time.Hour)) ||
		!fixture.registry.SetBlacklisted(credentialID) {
		t.Fatal("seed credential runtime health state")
	}
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":0,"reset_at":1786720000}}
		}`)}, nil
	})

	if _, err := fixture.service.ConsumeCredentialResetCredit(
		t.Context(),
		groupID,
		credentialID,
		resetCreditTestKey,
	); err != nil {
		t.Fatal(err)
	}
	view, exists := findRuntimeCredential(fixture.registry.Snapshot(), credentialID)
	if !exists {
		t.Fatal("credential missing from runtime registry")
	}
	if view.Blacklisted || !view.CooldownUntil.IsZero() || view.FailureCount != 0 ||
		view.Status != "active" {
		t.Fatalf("runtime credential after reset = %#v", view)
	}
	stats := fixture.stats.Snapshot(credentialID, now)
	if stats.ConsecutiveFailure != 0 || stats.ConsecutiveProblem != 0 ||
		stats.LastStatusCode != 0 {
		t.Fatalf("runtime stats after reset = %#v", stats)
	}
}

func TestConsumeCredentialResetCreditRetriesAmbiguousOutcomeOnlyWithSameKey(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	consumeCalls := 0
	setCodexResetCreditConsume(t, fixture.service, func(_ context.Context, _ codex.Credential, redeemRequestID string) (codex.AccountObservation, error) {
		consumeCalls++
		if redeemRequestID != resetCreditTestKey {
			t.Fatalf("redeem request id = %q", redeemRequestID)
		}
		if consumeCalls == 1 {
			return codex.AccountObservation{}, errors.New("connection closed after write")
		}
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{}`)}, nil
	})

	_, firstErr := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	var apiErr *app_errors.APIError
	if !errors.As(firstErr, &apiErr) || apiErr.Code != app_errors.ErrResetCreditOutcomeUnknown.Code {
		t.Fatalf("first error = %#v", firstErr)
	}
	otherKey := "9f0f4c32-89d2-4bcb-9e19-052940dc2f17"
	_, otherErr := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, otherKey)
	var otherAPIError *app_errors.APIError
	if !errors.As(otherErr, &otherAPIError) ||
		otherAPIError.Code != app_errors.ErrResetCreditOutcomeUnknown.Code || consumeCalls != 1 {
		t.Fatalf("different key error/calls = %#v/%d", otherErr, consumeCalls)
	}
	second, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	if err != nil || second.Status != "succeeded" || second.Replayed {
		t.Fatalf("second/error = %#v / %v", second, err)
	}
	third, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	if err != nil || !third.Replayed {
		t.Fatalf("third/error = %#v / %v", third, err)
	}
	if consumeCalls != 2 {
		t.Fatalf("consume calls = %d, want 2", consumeCalls)
	}
}

func TestConsumeCredentialResetCreditRecoversStalePreparedOperationWithSameKey(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	var credential models.Credential
	if err := fixture.db.Take(&credential, credentialID).Error; err != nil {
		t.Fatal(err)
	}
	digest := resetCreditRequestDigest(groupID, credentialID, credential.IdentityFingerprint)
	staleMS := now.Add(-defaultSubscriptionControlTimeout - time.Second).UnixMilli()
	if err := fixture.db.Create(&models.CredentialResetOperation{
		IdempotencyKey: resetCreditTestKey, RequestDigest: digest[:], GroupID: groupID,
		CredentialID: credentialID, RedeemRequestID: resetCreditTestKey,
		State: models.CredentialResetOperationPrepared, CreatedAtMS: staleMS, UpdatedAtMS: staleMS,
	}).Error; err != nil {
		t.Fatal(err)
	}
	consumeCalls := 0
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		consumeCalls++
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{}`)}, nil
	})

	result, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	if err != nil || result.Status != "succeeded" || consumeCalls != 1 {
		t.Fatalf("result/error/calls = %#v / %v / %d", result, err, consumeCalls)
	}
}

func TestConsumeCredentialResetCreditAllowsNewKeyAfterSuccessWhenObservationRefreshFails(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	consumeCalls := 0
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		consumeCalls++
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	observationCalls := 0
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		observationCalls++
		return codex.AccountObservation{}, errors.New("observation unavailable")
	})

	first, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	if err != nil || !first.ObservationPending {
		t.Fatalf("first response/error = %#v / %v", first, err)
	}
	replayed, err := fixture.service.ConsumeCredentialResetCredit(
		t.Context(), groupID, credentialID, resetCreditTestKey,
	)
	if err != nil || !replayed.Replayed || !replayed.ObservationPending {
		t.Fatalf("replayed response/error = %#v / %v", replayed, err)
	}
	secondKey := "9f0f4c32-89d2-4bcb-9e19-052940dc2f17"
	second, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, secondKey)
	if err != nil || !second.ObservationPending || second.Status != "succeeded" {
		t.Fatalf("second response/error = %#v / %v", second, err)
	}
	if consumeCalls != 2 || observationCalls != 2 {
		t.Fatalf("consume/observation calls = %d/%d, want 2/2", consumeCalls, observationCalls)
	}
}

func TestConsumeCredentialResetCreditAllowsNewKeyImmediatelyAfterSuccess(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	fixedNow := time.Date(2026, time.August, 14, 13, 30, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return fixedNow }
	consumeCalls := 0
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		consumeCalls++
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	observationCalls := 0
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		observationCalls++
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{}}`)}, nil
	})

	if _, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey); err != nil {
		t.Fatal(err)
	}
	secondKey := "9f0f4c32-89d2-4bcb-9e19-052940dc2f19"
	if _, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, secondKey); err != nil {
		t.Fatalf("second consume after durable fresh observation: %v", err)
	}
	if consumeCalls != 2 || observationCalls != 2 {
		t.Fatalf("consume/observation calls = %d/%d, want 2/2", consumeCalls, observationCalls)
	}
}

func TestConsumeCredentialResetCreditRejectsReusedKeyForAnotherCredential(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{}`)}, nil
	})
	if _, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey); err != nil {
		t.Fatal(err)
	}

	stage := mustImportSubscriptionStage(t, fixture, "reset-credit-other", "other-reset@example.com")
	if _, err := fixture.service.ConnectGroupCredentials(t.Context(), groupID, []string{stage.StageID}); err != nil {
		t.Fatal(err)
	}
	var other models.Credential
	if err := fixture.db.Where("group_id = ? AND id <> ?", groupID, credentialID).Take(&other).Error; err != nil {
		t.Fatal(err)
	}
	_, reuseErr := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, other.ID, resetCreditTestKey)
	var apiErr *app_errors.APIError
	if !errors.As(reuseErr, &apiErr) || apiErr.Code != app_errors.ErrIdempotencyKeyReused.Code {
		t.Fatalf("error = %#v", reuseErr)
	}
}

func TestConsumeCredentialResetCreditRejectsReusedKeyAfterIdentityChanges(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{}`)}, nil
	})
	if _, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Credential{}).Where("id = ?", credentialID).
		Update("identity_fingerprint", "changed-account-identity").Error; err != nil {
		t.Fatal(err)
	}

	_, reuseErr := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	var apiErr *app_errors.APIError
	if !errors.As(reuseErr, &apiErr) || apiErr.Code != app_errors.ErrIdempotencyKeyReused.Code {
		t.Fatalf("error = %#v", reuseErr)
	}
}

func TestConsumeCredentialResetCreditReplaysSuccessWithoutPreparingCredential(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{}`)}, nil
	})
	if _, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Credential{}).Where("id = ?", credentialID).
		Update("auth_state", models.CredentialAuthStateReauthorizationRequired).Error; err != nil {
		t.Fatal(err)
	}

	replayed, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	if err != nil || !replayed.Replayed || replayed.Status != "succeeded" {
		t.Fatalf("replayed/error = %#v / %v", replayed, err)
	}
}
