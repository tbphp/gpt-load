package control

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
)

const resetCreditTestKey = "9f0f4c32-89d2-4bcb-9e19-052940dc2f16"

func TestConsumeCredentialResetCreditIsDurablyIdempotentAndForcesObservation(t *testing.T) {
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
			"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":0,"reset_at":1786720000}},
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
		first.Observation == nil || second.Status != first.Status || !second.Replayed {
		t.Fatalf("calls=%d/%d first=%#v second=%#v", consumeCalls, observationCalls, first, second)
	}
}

func TestConsumeCredentialResetCreditOmitsInvalidObservationAfterSuccess(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
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
	if result.Status != "succeeded" || !result.ObservationPending || result.Observation != nil {
		t.Fatalf("result = %#v, want succeeded with pending omitted observation", result)
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

func TestConsumeCredentialResetCreditBlocksNewKeyUntilSuccessIsObserved(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 13, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	consumeCalls := 0
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		consumeCalls++
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	observationHealthy := false
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		if !observationHealthy {
			return codex.AccountObservation{}, errors.New("observation unavailable")
		}
		return codex.AccountObservation{Payload: []byte(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":0,"reset_at":1786720000}}
		}`)}, nil
	})

	first, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	if err != nil || !first.ObservationPending {
		t.Fatalf("first response/error = %#v / %v", first, err)
	}
	secondKey := "9f0f4c32-89d2-4bcb-9e19-052940dc2f17"
	_, blockedErr := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, secondKey)
	var blockedAPIError *app_errors.APIError
	if !errors.As(blockedErr, &blockedAPIError) ||
		blockedAPIError.Code != app_errors.ErrResetCreditOutcomeUnknown.Code || consumeCalls != 1 {
		t.Fatalf("blocked error/calls = %#v / %d", blockedErr, consumeCalls)
	}

	now = now.Add(time.Minute)
	observationHealthy = true
	if _, err := fixture.service.refreshCredentialObservation(
		t.Context(),
		groupID,
		credentialID,
		observationRefreshAfterMutation,
	); err != nil {
		t.Fatalf("force observation refresh: %v", err)
	}
	thirdKey := "9f0f4c32-89d2-4bcb-9e19-052940dc2f18"
	if _, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, thirdKey); err != nil {
		t.Fatalf("consume after observation: %v", err)
	}
	if consumeCalls != 2 {
		t.Fatalf("consume calls = %d, want 2", consumeCalls)
	}
}

func TestConsumeCredentialResetCreditAllowsNextKeyAfterSameMillisecondObservation(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	fixedNow := time.Date(2026, time.August, 14, 13, 30, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return fixedNow }
	consumeCalls := 0
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		consumeCalls++
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{}}`)}, nil
	})

	if _, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey); err != nil {
		t.Fatal(err)
	}
	secondKey := "9f0f4c32-89d2-4bcb-9e19-052940dc2f19"
	if _, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, secondKey); err != nil {
		t.Fatalf("second consume after durable fresh observation: %v", err)
	}
	if consumeCalls != 2 {
		t.Fatalf("consume calls = %d, want 2", consumeCalls)
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
