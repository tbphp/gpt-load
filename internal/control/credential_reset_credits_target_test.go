package control

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestConsumeCredentialResetCreditRejectsChangedTarget(t *testing.T) {
	for _, targets := range []struct{ name, before, after string }{
		{"host", "https://a.example/team", "https://b.example/team"},
		{"path", "https://relay.example/a", "https://relay.example/b"},
		{"official to custom", "", "https://relay.example"},
		{"custom to official", "https://relay.example", ""},
	} {
		for _, state := range []models.CredentialResetOperationState{
			models.CredentialResetOperationOutcomeUnknown,
			models.CredentialResetOperationPrepared,
		} {
			t.Run(targets.name+"/"+string(state), func(t *testing.T) {
				fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
				setSubscriptionTestTarget(t, fixture, groupID, targets.before)
				consumeCalls := 0
				fixture.service.consumeSubscriptionResetCredit = func(_ context.Context, _ channel.ID, _ subscriptionruntime.Credential, target subscriptionruntime.Target, requestID string) (subscriptionruntime.ResetCreditResult, error) {
					consumeCalls++
					root, err := target.BaseURL()
					if err != nil || root != targets.before || requestID != resetCreditTestKey {
						t.Errorf("reset target/request ID = %q/%q, error=%v", root, requestID, err)
					}
					return subscriptionruntime.ResetCreditResult{}, context.DeadlineExceeded
				}
				_, firstErr := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
				assertAPIErrorCode(t, firstErr, app_errors.ErrResetCreditOutcomeUnknown.Code)
				if state == models.CredentialResetOperationPrepared {
					// 同时覆盖服务中断后，超时的 prepared 操作被重新领取的路径。
					if err := fixture.db.Model(&models.CredentialResetOperation{}).
						Where("idempotency_key = ?", resetCreditTestKey).
						Updates(map[string]any{
							"state": state, "completed_at_ms": nil,
							"updated_at_ms": fixture.service.now().Add(-defaultSubscriptionControlTimeout - time.Second).UnixMilli(),
						}).Error; err != nil {
						t.Fatal(err)
					}
				}
				var before models.CredentialResetOperation
				if err := fixture.db.Take(&before, "idempotency_key = ?", resetCreditTestKey).Error; err != nil {
					t.Fatal(err)
				}
				setSubscriptionTestTarget(t, fixture, groupID, targets.after)
				_, retryErr := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
				assertAPIErrorCode(t, retryErr, app_errors.ErrIdempotencyKeyReused.Code)
				var after models.CredentialResetOperation
				if err := fixture.db.Take(&after, "idempotency_key = ?", resetCreditTestKey).Error; err != nil {
					t.Fatal(err)
				}
				if consumeCalls != 1 || !reflect.DeepEqual(before, after) {
					t.Fatalf("cross-target retry changed operation or dispatched again: calls=%d", consumeCalls)
				}
				// 被拒绝后，切回原目标仍能用原请求 ID 继续确认结果。
				setSubscriptionTestTarget(t, fixture, groupID, targets.before)
				_, sameTargetErr := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
				assertAPIErrorCode(t, sameTargetErr, app_errors.ErrResetCreditOutcomeUnknown.Code)
				if consumeCalls != 2 {
					t.Fatalf("same-target retry calls=%d, want 2", consumeCalls)
				}
			})
		}
	}
}

func TestConsumeCredentialResetCreditReplaysOnlySameNormalizedTarget(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	setSubscriptionTestTarget(t, fixture, groupID, "https://relay.example/team")
	consumeCalls := 0
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		consumeCalls++
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{}`)}, nil
	})
	if _, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey); err != nil {
		t.Fatal(err)
	}
	setSubscriptionTestTarget(t, fixture, groupID, "https://relay.example/other")
	_, changedErr := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	assertAPIErrorCode(t, changedErr, app_errors.ErrIdempotencyKeyReused.Code)
	setSubscriptionTestTarget(t, fixture, groupID, " HTTPS://RELAY.EXAMPLE:443/team/ ")
	replayed, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	if err != nil || !replayed.Replayed || replayed.Status != "succeeded" || consumeCalls != 1 {
		t.Fatalf("normalized-target replay=%#v, error=%v, upstream calls=%d", replayed, err, consumeCalls)
	}
}

func TestConsumeCredentialResetCreditPreservesLegacyOfficialOperations(t *testing.T) {
	for _, state := range []models.CredentialResetOperationState{
		models.CredentialResetOperationPrepared,
		models.CredentialResetOperationOutcomeUnknown,
		models.CredentialResetOperationSucceeded,
		models.CredentialResetOperationRejected,
	} {
		t.Run(string(state), func(t *testing.T) {
			fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
			var credential models.Credential
			if err := fixture.db.Take(&credential, credentialID).Error; err != nil {
				t.Fatal(err)
			}
			// 固定旧版摘要格式，避免测试跟随新实现一起改变而失去兼容性断言。
			digest := sha256.Sum256([]byte(fmt.Sprintf("gpt-load/credential-reset/v1/%d/%d/%s", groupID, credentialID, credential.IdentityFingerprint)))
			staleMS := fixture.service.now().Add(-defaultSubscriptionControlTimeout - time.Second).UnixMilli()
			operation := models.CredentialResetOperation{
				IdempotencyKey: resetCreditTestKey, RequestDigest: digest[:], GroupID: groupID,
				CredentialID: credentialID, RedeemRequestID: resetCreditTestKey,
				State: state, CreatedAtMS: staleMS, UpdatedAtMS: staleMS,
			}
			if state == models.CredentialResetOperationSucceeded {
				operation.ResultJSON = models.JSON(`{"status":"succeeded","windows_reset":1}`)
			}
			if state == models.CredentialResetOperationRejected {
				operation.ErrorCode = app_errors.ErrResetCreditUnavailable.Code
			}
			if err := fixture.db.Create(&operation).Error; err != nil {
				t.Fatal(err)
			}
			consumeCalls := 0
			setCodexResetCreditConsume(t, fixture.service, func(_ context.Context, _ codex.Credential, requestID string) (codex.AccountObservation, error) {
				consumeCalls++
				if requestID != resetCreditTestKey {
					t.Fatalf("legacy request ID changed: %q", requestID)
				}
				return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
			})
			setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
				return codex.AccountObservation{Payload: []byte(`{}`)}, nil
			})
			// 旧记录只属于官方目标，不能被当前配置的自定义上游领取。
			setSubscriptionTestTarget(t, fixture, groupID, "https://relay.example")
			_, customErr := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
			assertAPIErrorCode(t, customErr, app_errors.ErrIdempotencyKeyReused.Code)
			if consumeCalls != 0 {
				t.Fatalf("legacy operation dispatched to custom target: %d", consumeCalls)
			}
			setSubscriptionTestTarget(t, fixture, groupID, "")
			result, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
			switch state {
			case models.CredentialResetOperationRejected:
				assertAPIErrorCode(t, err, app_errors.ErrResetCreditUnavailable.Code)
			case models.CredentialResetOperationSucceeded:
				if err != nil || !result.Replayed || result.Status != "succeeded" {
					t.Fatalf("legacy success replay=%#v, error=%v", result, err)
				}
			default:
				if err != nil || result.Replayed || result.Status != "succeeded" || consumeCalls != 1 {
					t.Fatalf("legacy retry=%#v, error=%v, calls=%d", result, err, consumeCalls)
				}
			}
			if (state == models.CredentialResetOperationSucceeded || state == models.CredentialResetOperationRejected) && consumeCalls != 0 {
				t.Fatalf("legacy terminal result dispatched again: %d", consumeCalls)
			}
		})
	}
}
