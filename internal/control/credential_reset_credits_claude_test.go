package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func newClaudeResetCreditFixture(t *testing.T) (serviceFixture, uint, uint) {
	t.Helper()
	fixture := newServiceFixture(t)
	stage, err := fixture.service.ImportCredentialStage(t.Context(), channel.Claude, []byte(
		`{"type":"claude","access_token":"test-access","refresh_token":"test-refresh","account_uuid":"account-one","organization_uuid":"org-one","expired":"2030-01-01T00:00:00Z"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("Claude reset"), ChannelID: channel.Claude,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "claude-sonnet-4-6"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var credential models.Credential
	if err := fixture.db.Take(&credential, "group_id = ?", created.GroupID).Error; err != nil {
		t.Fatal(err)
	}
	return fixture, created.GroupID, credential.ID
}

func TestClaudeResetCreditRetryKeepsDurableSelection(t *testing.T) {
	for _, state := range []models.CredentialResetOperationState{
		models.CredentialResetOperationOutcomeUnknown, models.CredentialResetOperationPrepared,
	} {
		t.Run(string(state), func(t *testing.T) {
			fixture, groupID, credentialID := newClaudeResetCreditFixture(t)
			prepareCalls, consumeCalls := 0, 0
			fixture.service.prepareSubscriptionResetCredit = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (json.RawMessage, error) {
				prepareCalls++
				if prepareCalls != 1 {
					t.Fatal("retried operation selected a new grant")
				}
				return json.RawMessage(`{"organization_id":"org-one","grant_id":"original-grant"}`), nil
			}
			fixture.service.consumeSubscriptionResetCredit = func(_ context.Context, _ channel.ID, _ subscriptionruntime.Credential, _ subscriptionruntime.Target, request subscriptionruntime.ResetCreditRequest) (subscriptionruntime.ResetCreditResult, error) {
				consumeCalls++
				var stored models.CredentialResetOperation
				if err := fixture.db.Take(&stored, "idempotency_key = ?", resetCreditTestKey).Error; err != nil {
					t.Fatal(err)
				}
				if request.ID != resetCreditTestKey || !strings.Contains(string(request.Data), "original-grant") ||
					!strings.Contains(string(stored.ResultJSON), "original-grant") {
					t.Fatal("reset was dispatched without its durable selection")
				}
				if consumeCalls == 1 {
					return subscriptionruntime.ResetCreditResult{}, context.DeadlineExceeded
				}
				return subscriptionruntime.ResetCreditResult{Status: "succeeded", WindowsReset: 2}, nil
			}
			fixture.service.observeSubscriptionAccount = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
				return subscriptionruntime.Observation{Payload: []byte(`{"plan_summary":{},"quota_windows":[],"reset_credits_available":0}`), QuotaObserved: true}, nil
			}
			_, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
			assertAPIErrorCode(t, err, app_errors.ErrResetCreditOutcomeUnknown.Code)
			_, err = fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, "9f0f4c32-89d2-4bcb-9e19-052940dc2f17")
			assertAPIErrorCode(t, err, app_errors.ErrResetCreditOutcomeUnknown.Code)
			if prepareCalls != 1 || consumeCalls != 1 {
				t.Fatal("unknown redemption accepted a new operation")
			}
			if state == models.CredentialResetOperationPrepared {
				if err := fixture.db.Model(&models.CredentialResetOperation{}).Where("idempotency_key = ?", resetCreditTestKey).
					Updates(map[string]any{"state": state, "updated_at_ms": fixture.service.now().Add(-defaultSubscriptionControlTimeout - time.Second).UnixMilli()}).Error; err != nil {
					t.Fatal(err)
				}
			}
			result, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
			if err != nil || result.Status != "succeeded" || prepareCalls != 1 || consumeCalls != 2 {
				t.Fatalf("retried reset = %#v, %v; prepare/consume = %d/%d", result, err, prepareCalls, consumeCalls)
			}
		})
	}
}

func TestClaudeResetCreditPreparationFailureAllowsRetry(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		code   string
		newKey bool
	}{
		{name: "timeout with same key", err: context.DeadlineExceeded, code: "RESET_CREDIT_PREPARATION_FAILED"},
		{name: "timeout after page reload", err: context.DeadlineExceeded, code: "RESET_CREDIT_PREPARATION_FAILED", newKey: true},
		{name: "invalid selection", err: errors.New("invalid reset selection"), code: "RESET_CREDIT_PREPARATION_FAILED"},
		{name: "upstream unavailable", err: &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusServiceUnavailable}, code: "RESET_CREDIT_PREPARATION_FAILED"},
		{name: "profile not found", err: &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusNotFound}, code: "RESET_CREDIT_PREPARATION_FAILED"},
		{name: "usage rate limited", err: &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusTooManyRequests}, code: "RESET_CREDIT_PREPARATION_FAILED"},
		{name: "reauthorization required", err: &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusUnauthorized}, code: app_errors.ErrCredentialReauthorizationRequired.Code, newKey: true},
		{name: "no available credit", err: &subscriptionruntime.UpstreamHTTPError{StatusCode: http.StatusConflict}, code: app_errors.ErrResetCreditUnavailable.Code, newKey: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, groupID, credentialID := newClaudeResetCreditFixture(t)
			prepareCalls, consumeCalls := 0, 0
			fixture.service.prepareSubscriptionResetCredit = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (json.RawMessage, error) {
				prepareCalls++
				if prepareCalls == 1 {
					return nil, test.err
				}
				return json.RawMessage(`{"organization_id":"org-one","grant_id":"grant-one"}`), nil
			}
			fixture.service.consumeSubscriptionResetCredit = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target, subscriptionruntime.ResetCreditRequest) (subscriptionruntime.ResetCreditResult, error) {
				consumeCalls++
				return subscriptionruntime.ResetCreditResult{Status: "succeeded", WindowsReset: 1}, nil
			}
			fixture.service.observeSubscriptionAccount = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
				return subscriptionruntime.Observation{Payload: []byte(`{"plan_summary":{},"quota_windows":[]}`), QuotaObserved: true}, nil
			}
			_, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
			assertAPIErrorCode(t, err, test.code)
			var operation models.CredentialResetOperation
			if err := fixture.db.Take(&operation, "idempotency_key = ?", resetCreditTestKey).Error; err != nil {
				t.Fatal(err)
			}
			if operation.State != models.CredentialResetOperationRejected || consumeCalls != 0 {
				t.Fatalf("preparation failure state/calls = %s/%d", operation.State, consumeCalls)
			}
			key := resetCreditTestKey
			if test.newKey {
				key = "9f0f4c32-89d2-4bcb-9e19-052940dc2f17"
			}
			result, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, key)
			if err != nil || result.Status != "succeeded" || prepareCalls != 2 || consumeCalls != 1 {
				t.Fatalf("retry result/error/calls = %#v/%v/%d/%d", result, err, prepareCalls, consumeCalls)
			}
		})
	}
}

func TestClaudeResetCreditPreparationFailurePreservesNewerAttempt(t *testing.T) {
	fixture, groupID, credentialID := newClaudeResetCreditFixture(t)
	fixture.service.prepareSubscriptionResetCredit = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (json.RawMessage, error) {
		if err := fixture.db.Model(&models.CredentialResetOperation{}).
			Where("idempotency_key = ?", resetCreditTestKey).
			Update("updated_at_ms", fixture.service.now().Add(time.Second).UnixMilli()).Error; err != nil {
			t.Fatal(err)
		}
		return nil, context.DeadlineExceeded
	}
	fixture.service.consumeSubscriptionResetCredit = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target, subscriptionruntime.ResetCreditRequest) (subscriptionruntime.ResetCreditResult, error) {
		t.Fatal("failed preparation dispatched a redemption")
		return subscriptionruntime.ResetCreditResult{}, nil
	}
	_, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	assertAPIErrorCode(t, err, app_errors.ErrResetCreditOutcomeUnknown.Code)
	var operation models.CredentialResetOperation
	if err := fixture.db.Take(&operation, "idempotency_key = ?", resetCreditTestKey).Error; err != nil {
		t.Fatal(err)
	}
	if operation.State != models.CredentialResetOperationPrepared {
		t.Fatalf("older preparation overwrote the newer attempt: %s", operation.State)
	}
}

func TestClaudeResetCreditPreparationRetryDoesNotBypassPendingOperation(t *testing.T) {
	fixture, groupID, credentialID := newClaudeResetCreditFixture(t)
	prepareCalls := 0
	fixture.service.prepareSubscriptionResetCredit = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (json.RawMessage, error) {
		prepareCalls++
		return nil, context.DeadlineExceeded
	}
	_, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	assertAPIErrorCode(t, err, app_errors.ErrResetCreditPreparationFailed.Code)
	var pending models.CredentialResetOperation
	if err := fixture.db.Take(&pending, "idempotency_key = ?", resetCreditTestKey).Error; err != nil {
		t.Fatal(err)
	}
	pending.IdempotencyKey = "9f0f4c32-89d2-4bcb-9e19-052940dc2f17"
	pending.RedeemRequestID = pending.IdempotencyKey
	pending.State = models.CredentialResetOperationPrepared
	pending.ErrorCode = ""
	pending.CompletedAtMS = nil
	if err := fixture.db.Create(&pending).Error; err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	assertAPIErrorCode(t, err, app_errors.ErrResetCreditOutcomeUnknown.Code)
	if prepareCalls != 1 {
		t.Fatal("preparation retry bypassed another pending operation")
	}
}

func TestClaudeResetCreditInvalidStoredSelectionStaysUnknown(t *testing.T) {
	fixture, groupID, credentialID := newClaudeResetCreditFixture(t)
	var credential models.Credential
	if err := fixture.db.Take(&credential, credentialID).Error; err != nil {
		t.Fatal(err)
	}
	digest := resetCreditRequestDigest(groupID, credentialID, credential.IdentityFingerprint, "")
	stored := models.JSON(`{}`)
	if err := fixture.db.Create(&models.CredentialResetOperation{
		IdempotencyKey: resetCreditTestKey, RedeemRequestID: resetCreditTestKey, RequestDigest: digest[:],
		GroupID: groupID, CredentialID: credentialID, State: models.CredentialResetOperationOutcomeUnknown,
		ResultJSON: stored,
	}).Error; err != nil {
		t.Fatal(err)
	}
	fixture.service.consumeSubscriptionResetCredit = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target, subscriptionruntime.ResetCreditRequest) (subscriptionruntime.ResetCreditResult, error) {
		t.Fatal("invalid saved selection dispatched a redemption")
		return subscriptionruntime.ResetCreditResult{}, nil
	}
	for _, key := range []string{resetCreditTestKey, "9f0f4c32-89d2-4bcb-9e19-052940dc2f17"} {
		_, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, key)
		assertAPIErrorCode(t, err, app_errors.ErrResetCreditOutcomeUnknown.Code)
	}
	var operation models.CredentialResetOperation
	if err := fixture.db.Take(&operation, "idempotency_key = ?", resetCreditTestKey).Error; err != nil {
		t.Fatal(err)
	}
	if operation.State != models.CredentialResetOperationOutcomeUnknown || string(operation.ResultJSON) != string(stored) {
		t.Fatalf("unknown redemption lost its saved state: %#v", operation)
	}
}

func TestClaudeResetCreditUsesSharedOperationAndRefresh(t *testing.T) {
	fixture, groupID, credentialID := newClaudeResetCreditFixture(t)
	consumeCalls, observationCalls := 0, 0
	fixture.service.prepareSubscriptionResetCredit = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (json.RawMessage, error) {
		return json.RawMessage(`{"grant_id":"grant-one","organization_id":"org-one"}`), nil
	}
	fixture.service.consumeSubscriptionResetCredit = func(_ context.Context, channelID channel.ID, _ subscriptionruntime.Credential, _ subscriptionruntime.Target, request subscriptionruntime.ResetCreditRequest) (subscriptionruntime.ResetCreditResult, error) {
		consumeCalls++
		if channelID != channel.Claude || request.ID != resetCreditTestKey {
			t.Fatal("reset dispatched to the wrong channel or operation")
		}
		return subscriptionruntime.ResetCreditResult{Status: "succeeded", WindowsReset: 2}, nil
	}
	fixture.service.observeSubscriptionAccount = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
		observationCalls++
		return subscriptionruntime.Observation{Payload: []byte(`{"plan_summary":{},"quota_windows":[],"reset_credits_available":0}`), QuotaObserved: true}, nil
	}
	for index := range 2 {
		result, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "succeeded" || result.WindowsReset != 2 || result.Replayed != (index == 1) ||
			result.Observation == nil || result.ObservationPending {
			t.Fatalf("reset result = %#v", result)
		}
	}
	if consumeCalls != 1 || observationCalls != 1 {
		t.Fatalf("consume/refresh calls = %d/%d", consumeCalls, observationCalls)
	}
}
