package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/claude"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestObservationPlanSummaryPreservesPresentationLevel(t *testing.T) {
	var snapshot CredentialObservationSnapshot
	if err := json.Unmarshal([]byte(`{"plan_summary":{"name":"Team","level":"premium"},"quota_windows":[]}`), &snapshot); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var projected struct {
		Plan struct {
			Level string `json:"level"`
		} `json:"plan_summary"`
	}
	if err := json.Unmarshal(encoded, &projected); err != nil || projected.Plan.Level != "premium" {
		t.Fatalf("plan level = %q, %v", projected.Plan.Level, err)
	}
}

func TestNormalizeCodexObservationKeepsDynamicWindowsAndStableOrder(t *testing.T) {
	t.Parallel()

	snapshot, err := normalizeCodexObservation([]byte(`{
		"plan_type":"pro",
		"rate_limit":{"allowed":true,"primary_window":{"limit_window_seconds":18000,"used_percent":12,"reset_at":1800000100},"secondary_window":{"limit_window_seconds":604800,"used_percent":90,"reset_at":1800000200}},
		"additional_rate_limits":[{"limit_name":"gpt-5.2","rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":100,"reset_at":1800000050}}}],
		"rate_limit_reset_credits":{"available_count":2}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Plan.Name != "Pro 20x" || snapshot.ResetCreditsAvailable == nil || *snapshot.ResetCreditsAvailable != 2 || len(snapshot.QuotaWindows) != 3 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.QuotaWindows[0].ID != "gpt-5-2-primary" || snapshot.QuotaWindows[0].State != "exhausted" || !snapshot.QuotaWindows[0].IsPrimary {
		t.Fatalf("primary = %#v", snapshot.QuotaWindows[0])
	}
	if snapshot.QuotaWindows[1].ID != "secondary" || snapshot.QuotaWindows[2].ID != "primary" {
		t.Fatalf("window order = %#v", snapshot.QuotaWindows)
	}
	if snapshot.QuotaWindows[0].Label != "GPT 5.2 · 7d" ||
		snapshot.QuotaWindows[1].Label != "Weekly · 7d" || snapshot.QuotaWindows[2].Label != "Session · 5h" {
		t.Fatalf("window labels = %#v", snapshot.QuotaWindows)
	}
}

func TestMapCredentialObservationKeepsSafeAccountSummary(t *testing.T) {
	createdAt := time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC).UnixMilli()
	row := models.CredentialObservation{
		CredentialID: 1, ObservationVersion: 1, State: models.CredentialObservationFresh,
		SnapshotJSON: models.JSON(fmt.Sprintf(`{
			"plan_summary":{"name":"Team"},
			"account_summary":{"display_name":"Owner","organization_name":"Example Org","seat_tier":"team_standard","account_created_at_ms":%d},
			"quota_windows":[]
		}`, createdAt)),
	}

	result := mapCredentialObservation(row)
	if result.Snapshot == nil || result.Snapshot.Account == nil ||
		result.Snapshot.Account.DisplayName != "Owner" ||
		result.Snapshot.Account.OrganizationName != "Example Org" ||
		result.Snapshot.Account.SeatTier != "team_standard" ||
		result.Snapshot.Account.AccountCreatedAtMS == nil ||
		*result.Snapshot.Account.AccountCreatedAtMS != createdAt {
		t.Fatalf("observation = %#v", result)
	}
}

func TestNormalizeCodexResetCreditDetailsKeepsOnlyAvailableCodexCredits(t *testing.T) {
	t.Parallel()

	count, credits, listPresent, err := normalizeCodexResetCreditDetails([]byte(`{
		"availableCount":"2",
		"credits":[
			{"reset_type":"codex_rate_limits","status":"redeemed","expires_at":"2026-09-01T00:00:00Z"},
			{"reset_type":"other","status":"available","expires_at":"2026-09-02T00:00:00Z"},
			{"resetType":"codex_rate_limits","status":"available","expiresAt":"2026-09-04T00:00:00Z"},
			{"reset_type":"codex_rate_limits","status":"available","expires_at":"2026-09-03T00:00:00Z"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if count == nil || *count != 2 || !listPresent || len(credits) != 2 {
		t.Fatalf("count/list/credits = %#v/%t/%#v", count, listPresent, credits)
	}
	if credits[0].ExpiresAtMS >= credits[1].ExpiresAtMS {
		t.Fatalf("credits are not sorted by expiration: %#v", credits)
	}
}

func TestNormalizeCodexResetCreditDetailsAcceptsTopLevelListAndCountsAvailable(t *testing.T) {
	t.Parallel()

	count, credits, listPresent, err := normalizeCodexResetCreditDetails([]byte(`[
		{"status":"available","expires_at":"2026-09-03T00:00:00Z"},
		{"status":"redeemed","expires_at":"2026-09-04T00:00:00Z"},
		{"status":"available"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if count == nil || *count != 2 || !listPresent || len(credits) != 1 {
		t.Fatalf("count/list/credits = %#v/%t/%#v", count, listPresent, credits)
	}
}

func TestNormalizeCodexObservationDoesNotInventMissingFiveHourWindow(t *testing.T) {
	t.Parallel()

	snapshot, err := normalizeCodexObservation([]byte(`{"plan_type":"plus","rate_limit":{"secondary_window":{"limit_window_seconds":604800,"used_percent":25}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QuotaWindows) != 1 || snapshot.QuotaWindows[0].WindowSeconds == nil || *snapshot.QuotaWindows[0].WindowSeconds != 604800 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestNormalizeCodexObservationDoesNotTreatMeterNameAsModelID(t *testing.T) {
	snapshot, err := normalizeCodexObservation([]byte(`{
		"additional_rate_limits":[
			{"metered_feature":"codex_other_models","rate_limit":{"primary_window":{"used_percent":12}}},
			{"limit_name":"explicit-models","model_ids":["gpt-5.2","gpt-5.2","gpt-5.1"],"rate_limit":{"primary_window":{"used_percent":20}}}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QuotaWindows) != 2 ||
		!reflect.DeepEqual(snapshot.QuotaWindows[0].ModelIDs, []string{"gpt-5.2", "gpt-5.1"}) ||
		len(snapshot.QuotaWindows[1].ModelIDs) != 0 {
		t.Fatalf("quota windows = %#v", snapshot.QuotaWindows)
	}
}

func TestRefreshCredentialObservationPersistsLKGAndAllowsImmediateManualRefresh(t *testing.T) {
	t.Parallel()

	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.UnixMilli(1_800_000_000_000)
	fixture.service.now = func() time.Time { return now }
	calls := 0
	setCodexAccountObservation(fixture.service, func(_ context.Context, credential codex.Credential) (codex.AccountObservation, error) {
		calls++
		if credential.AccountID != "account-observation" {
			t.Fatalf("credential = %#v", credential)
		}
		return codex.AccountObservation{Payload: []byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":40,"reset_at":1800001000}}}`)}, nil
	})
	first, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != string(models.CredentialObservationFresh) || first.Snapshot == nil || calls != 1 {
		t.Fatalf("first = %#v, calls=%d", first, calls)
	}
	second, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil || second.ObservationVersion != 2 {
		t.Fatalf("second = %#v, %v", second, err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		calls++
		return codex.AccountObservation{}, errors.New("upstream unavailable")
	})
	failed, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err == nil || failed.Snapshot == nil || failed.State != string(models.CredentialObservationFresh) ||
		failed.LastErrorCode != "observation_upstream_failed" {
		t.Fatalf("failed = %#v, %v", failed, err)
	}
	if !reflect.DeepEqual(failed.Snapshot, first.Snapshot) {
		t.Fatalf("LKG changed = %#v / %#v", first.Snapshot, failed.Snapshot)
	}
}

func TestRefreshCredentialObservationKeepsFreshQuotaWhenPartialUsageIsMissing(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	resetAt := now.Add(2 * time.Hour).Unix()
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":100,"reset_at":%d}}
		}`, resetAt))}, nil
	})
	first, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != string(models.CredentialObservationFresh) || first.Snapshot == nil || len(first.Snapshot.QuotaWindows) != 1 || first.FreshUntilMS == nil {
		t.Fatalf("initial observation = %#v", first)
	}
	if candidates := fixture.registry.CollectCredentialCandidates([]uint{groupID}, nil, now); len(candidates) != 0 {
		t.Fatalf("exhausted candidates = %#v", candidates)
	}

	now = now.Add(time.Minute)
	fixture.service.observeSubscriptionAccount = func(
		context.Context,
		channel.ID,
		subscriptionruntime.Credential,
	) (subscriptionruntime.Observation, error) {
		return subscriptionruntime.Observation{
			Payload:         []byte(`{"account_summary":{"display_name":"Updated owner"},"quota_windows":[]}`),
			Partial:         true,
			AccountObserved: true,
		}, nil
	}

	result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil || result.State != string(models.CredentialObservationFresh) ||
		result.LastErrorCode != "observation_partial" || result.Snapshot == nil ||
		result.Snapshot.Account == nil || result.Snapshot.Account.DisplayName != "Updated owner" ||
		len(result.Snapshot.QuotaWindows) != 1 {
		t.Fatalf("partial result = %#v / %#v / %v", result, result.Snapshot, err)
	}
	if candidates := fixture.registry.CollectCredentialCandidates([]uint{groupID}, nil, now); len(candidates) != 0 {
		t.Fatalf("partial observation cleared quota protection: %#v", candidates)
	}
}

func TestRefreshCredentialObservationMergesSparseAccountWithFreshQuota(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	fixture.service.observeSubscriptionAccount = func(
		context.Context,
		channel.ID,
		subscriptionruntime.Credential,
	) (subscriptionruntime.Observation, error) {
		return subscriptionruntime.Observation{Payload: []byte(`{
			"plan_summary":{"name":"Claude Team"},
			"account_summary":{
				"display_name":"Original owner",
				"organization_name":"Example Org",
				"seat_tier":"team_standard",
				"organization_role":"member",
				"extra_usage_enabled":true
			},
			"quota_windows":[{"id":"five_hour","label":"5h","scope":"account","unit":"percent","utilization":0.9,"state":"available"}]
		}`)}, nil
	}
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)
	fixture.service.observeSubscriptionAccount = func(
		context.Context,
		channel.ID,
		subscriptionruntime.Credential,
	) (subscriptionruntime.Observation, error) {
		return subscriptionruntime.Observation{
			Payload: []byte(`{
				"plan_summary":{},
				"account_summary":{"organization_role":"admin","extra_usage_enabled":false},
				"quota_windows":[{"id":"five_hour","label":"5h","scope":"account","unit":"percent","utilization":0.5,"state":"available"}]
			}`),
			Partial: true, AccountObserved: true, QuotaObserved: true,
		}, nil
	}

	result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil || result.Snapshot == nil || result.Snapshot.Account == nil {
		t.Fatalf("partial result = %#v / %v", result, err)
	}
	account := result.Snapshot.Account
	if result.Snapshot.Plan.Name != "Claude Team" || account.DisplayName != "Original owner" ||
		account.OrganizationName != "Example Org" || account.SeatTier != "team_standard" ||
		account.OrganizationRole != "admin" || account.ExtraUsageEnabled == nil || *account.ExtraUsageEnabled ||
		len(result.Snapshot.QuotaWindows) != 1 || result.Snapshot.QuotaWindows[0].Utilization == nil ||
		*result.Snapshot.QuotaWindows[0].Utilization != 0.5 {
		t.Fatalf("merged snapshot = %#v", result.Snapshot)
	}
}

func TestRefreshCredentialObservationRetriesOnceAfterUnauthorized(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	originalPrepare := fixture.service.prepareSubscriptionCredential
	forcedRefreshes := 0
	fixture.service.prepareSubscriptionCredential = func(
		ctx context.Context,
		channelID channel.ID,
		snapshot execution.CredentialSnapshot,
		force bool,
	) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
		if force {
			forcedRefreshes++
		}
		return originalPrepare(ctx, channelID, snapshot, false)
	}
	observationCalls := 0
	fixture.service.observeSubscriptionAccount = func(
		context.Context,
		channel.ID,
		subscriptionruntime.Credential,
	) (subscriptionruntime.Observation, error) {
		observationCalls++
		if observationCalls == 1 {
			return subscriptionruntime.Observation{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: 401}
		}
		return subscriptionruntime.Observation{Payload: []byte(`{"quota_windows":[]}`)}, nil
	}

	result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil || result.State != string(models.CredentialObservationFresh) ||
		forcedRefreshes != 1 || observationCalls != 2 {
		t.Fatalf("result/error/refreshes/calls = %#v / %v / %d / %d", result, err, forcedRefreshes, observationCalls)
	}
}

func TestRefreshCredentialObservationClassifiesRepeatedAuthorizationFailure(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	originalPrepare := fixture.service.prepareSubscriptionCredential
	forcedRefreshes := 0
	fixture.service.prepareSubscriptionCredential = func(
		ctx context.Context,
		channelID channel.ID,
		snapshot execution.CredentialSnapshot,
		force bool,
	) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
		if force {
			forcedRefreshes++
		}
		return originalPrepare(ctx, channelID, snapshot, false)
	}
	observationCalls := 0
	fixture.service.observeSubscriptionAccount = func(
		context.Context,
		channel.ID,
		subscriptionruntime.Credential,
	) (subscriptionruntime.Observation, error) {
		observationCalls++
		return subscriptionruntime.Observation{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: 401}
	}

	result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err == nil || result.State != string(models.CredentialObservationError) ||
		result.LastErrorCode != "observation_authorization_failed" ||
		forcedRefreshes != 1 || observationCalls != 2 {
		t.Fatalf("result/error/refreshes/calls = %#v / %v / %d / %d", result, err, forcedRefreshes, observationCalls)
	}
	var stored models.CredentialObservation
	if err := fixture.db.Where("credential_id = ?", credentialID).Take(&stored).Error; err != nil ||
		stored.LastAuthRefreshSecretVersion == nil || *stored.LastAuthRefreshSecretVersion != 1 {
		t.Fatalf("stored observation = %#v / %v", stored, err)
	}
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err == nil ||
		forcedRefreshes != 1 || observationCalls != 3 {
		t.Fatalf("second result/refreshes/calls = %v / %d / %d", err, forcedRefreshes, observationCalls)
	}
}

func TestRefreshCredentialObservationClassifiesNonRefreshableHTTPFailure(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errorCode  string
	}{
		{name: "forbidden", statusCode: 403, errorCode: "observation_access_denied"},
		{name: "server error", statusCode: 500, errorCode: "observation_upstream_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
			originalPrepare := fixture.service.prepareSubscriptionCredential
			forcedRefreshes := 0
			fixture.service.prepareSubscriptionCredential = func(
				ctx context.Context,
				channelID channel.ID,
				snapshot execution.CredentialSnapshot,
				force bool,
			) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
				if force {
					forcedRefreshes++
				}
				return originalPrepare(ctx, channelID, snapshot, false)
			}
			observationCalls := 0
			fixture.service.observeSubscriptionAccount = func(
				context.Context,
				channel.ID,
				subscriptionruntime.Credential,
			) (subscriptionruntime.Observation, error) {
				observationCalls++
				return subscriptionruntime.Observation{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: test.statusCode}
			}

			result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
			if err == nil || result.LastErrorCode != test.errorCode ||
				forcedRefreshes != 0 || observationCalls != 1 {
				t.Fatalf("result/error/refreshes/calls = %#v / %v / %d / %d", result, err, forcedRefreshes, observationCalls)
			}
		})
	}
}

func TestRefreshCredentialObservationPersistsPartialSnapshotAsStale(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	fixture.service.observeSubscriptionAccount = func(
		context.Context,
		channel.ID,
		subscriptionruntime.Credential,
	) (subscriptionruntime.Observation, error) {
		return subscriptionruntime.Observation{
			Payload: []byte(`{"plan_summary":{"name":"Claude Team"},"account_summary":{"display_name":"Owner"},"quota_windows":[]}`),
			Partial: true,
		}, nil
	}

	result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != string(models.CredentialObservationStale) || result.Snapshot == nil ||
		result.Snapshot.Account == nil || result.Snapshot.Account.DisplayName != "Owner" ||
		result.FreshUntilMS != nil || result.LastErrorCode != "observation_partial" {
		t.Fatalf("partial observation = %#v", result)
	}
}

func TestConnectingSubscriptionCredentialWakesObservationRefresh(t *testing.T) {
	fixture, groupID, _ := newSubscriptionCredentialFixture(t)
	observedAccounts := make(chan string, 4)
	setCodexAccountObservation(fixture.service, func(_ context.Context, credential codex.Credential) (codex.AccountObservation, error) {
		observedAccounts <- credential.AccountID
		return codex.AccountObservation{Payload: []byte(`{"plan_type":"plus","rate_limit":{}}`)}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go fixture.service.RunCredentialObservationRefresh(ctx)
	select {
	case accountID := <-observedAccounts:
		if accountID != "account-observation" {
			t.Fatalf("initial observed account = %q", accountID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initial observation sweep did not finish")
	}

	stage := mustImportSubscriptionStage(t, fixture, "account-immediate", "immediate@example.com")
	if _, err := fixture.service.ConnectGroupCredentialsIdempotent(
		t.Context(), "00000000-0000-4000-8000-000000000001", groupID, []string{stage.StageID},
	); err != nil {
		t.Fatal(err)
	}
	select {
	case accountID := <-observedAccounts:
		if accountID != "account-immediate" {
			t.Fatalf("connected observed account = %q", accountID)
		}
	case <-time.After(time.Second):
		t.Fatal("connecting a subscription credential did not wake observation refresh")
	}
}

func TestRefreshCredentialObservationEnrichesResetCreditDetails(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{
			"plan_type":"plus",
			"rate_limit_reset_credits":{"available_count":1}
		}`)}, nil
	})
	detailCalls := 0
	setCodexResetCreditObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		detailCalls++
		return codex.AccountObservation{Payload: []byte(`{
			"available_count":2,
			"credits":[
				{"status":"available","expires_at":"2026-09-03T00:00:00Z"},
				{"status":"available","expires_at":"2026-09-02T00:00:00Z"}
			]
		}`)}, nil
	})

	result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if detailCalls != 1 || result.Snapshot == nil || result.Snapshot.ResetCreditsAvailable == nil ||
		*result.Snapshot.ResetCreditsAvailable != 2 || len(result.Snapshot.ResetCredits) != 2 {
		t.Fatalf("detailCalls/result = %d/%#v", detailCalls, result)
	}
}

func TestRefreshCredentialObservationKeepsUsageWhenResetCreditDetailsFail(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"rate_limit_reset_credits":{"available_count":2}}`)}, nil
	})
	setCodexResetCreditObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{}, errors.New("details unavailable")
	})

	result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot == nil || result.Snapshot.ResetCreditsAvailable == nil ||
		*result.Snapshot.ResetCreditsAvailable != 2 || len(result.Snapshot.ResetCredits) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRefreshClaudeCredentialObservationPublishesAccountAndQuota(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	stage, err := fixture.service.ImportCredentialStage(t.Context(), channel.Claude, []byte(
		`{"type":"claude","access_token":"claude-access","refresh_token":"claude-refresh","account_uuid":"claude-account","email":"owner@example.com","expired":"2030-01-01T00:00:00Z"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("Claude observation"), ChannelID: channel.Claude,
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
	fixture.service.observeSubscriptionAccount = func(
		_ context.Context,
		channelID channel.ID,
		credential subscriptionruntime.Credential,
	) (subscriptionruntime.Observation, error) {
		if channelID != channel.Claude {
			return subscriptionruntime.Observation{}, errors.New("unexpected subscription channel")
		}
		parsed, parseErr := claude.ParseCredentialJSON(credential.Canonical())
		if parseErr != nil || parsed.AccountUUID != "claude-account" {
			t.Fatalf("Claude credential = %#v, %v", parsed, parseErr)
		}
		utilization := 25.0
		reset := now.Add(5 * time.Hour).Format(time.RFC3339)
		extraUsage := true
		payload, normalizeErr := claude.NormalizeObservation(claude.AccountObservation{
			Profile: claude.AccountProfile{
				DisplayName: "Owner", Email: "owner@example.com", OrganizationName: "Example Org",
				OrganizationType: "claude_team", SeatTier: "team_standard", ExtraUsageEnabled: &extraUsage,
			},
			Usage: claude.Usage{FiveHour: &claude.UsageWindow{Utilization: &utilization, ResetsAt: &reset}},
		})
		return subscriptionruntime.Observation{Payload: payload}, normalizeErr
	}

	result, err := fixture.service.RefreshCredentialObservation(t.Context(), created.GroupID, credential.ID)
	if err != nil || result.Snapshot == nil || result.Snapshot.Account == nil ||
		result.Snapshot.Account.DisplayName != "Owner" ||
		result.Snapshot.Account.OrganizationName != "Example Org" ||
		result.Snapshot.Plan.Name != "Team" || len(result.Snapshot.QuotaWindows) != 1 ||
		result.Snapshot.QuotaWindows[0].Utilization == nil ||
		*result.Snapshot.QuotaWindows[0].Utilization != 0.25 {
		t.Fatalf("Claude observation = %#v, %v", result, err)
	}
	views := fixture.registry.Snapshot()
	if len(views) != 1 || views[0].QuotaRemaining == nil || *views[0].QuotaRemaining != 0.75 {
		t.Fatalf("Claude quota runtime = %#v", views)
	}
}

func TestEnrichCredentialObservationUsageUsesHourlyStatsForEveryWindow(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	shortReset := now.Add(2 * time.Hour).UnixMilli()
	longReset := now.Add(3 * 24 * time.Hour).UnixMilli()
	modelReset := now.Add(time.Hour).UnixMilli()
	freshUntil := now.Add(time.Hour).UnixMilli()
	shortSeconds := int64((5 * time.Hour) / time.Second)
	longSeconds := int64((7 * 24 * time.Hour) / time.Second)
	modelSeconds := int64(time.Hour / time.Second)
	response := CredentialObservationResponse{
		State:        string(models.CredentialObservationFresh),
		FreshUntilMS: &freshUntil,
		Snapshot: &CredentialObservationSnapshot{QuotaWindows: []ObservationQuotaWindow{
			{ID: "short", Scope: "account", ResetAtMS: &shortReset, WindowSeconds: &shortSeconds},
			{ID: "long", Scope: "account", ResetAtMS: &longReset, WindowSeconds: &longSeconds},
			{ID: "model", Scope: "spark", ResetAtMS: &modelReset, WindowSeconds: &modelSeconds},
		}},
	}
	reader := &recordingCredentialWindowUsageReader{}
	reader.result = requestlog.CredentialWindowUsage{
		UsageAggregate: requestlog.UsageAggregate{
			RequestCount: 3, UncachedInputTokens: 11, CacheReadTokens: 5,
			OutputTokens: 7, EstimatedCostNanoUSD: 29,
		},
		DataComplete: true,
	}
	fixture.service.credentialWindowUsage = reader

	fixture.service.enrichCredentialObservationUsage(t.Context(), 37, &response)

	if len(reader.queries) != 2 {
		t.Fatalf("usage queries = %#v, want two account windows", reader.queries)
	}
	if reader.queries[0].Source != requestlog.CredentialWindowUsageSourceHourlyStats ||
		reader.queries[0].FromMS != shortReset-shortSeconds*1000 ||
		reader.queries[0].ToMS != now.UnixMilli() {
		t.Fatalf("short window query = %#v", reader.queries[0])
	}
	if reader.queries[1].Source != requestlog.CredentialWindowUsageSourceHourlyStats ||
		reader.queries[1].FromMS != longReset-longSeconds*1000 {
		t.Fatalf("long window query = %#v", reader.queries[1])
	}
	shortUsage := response.Snapshot.QuotaWindows[0].ObservedUsage
	longUsage := response.Snapshot.QuotaWindows[1].ObservedUsage
	if shortUsage == nil || longUsage == nil ||
		shortUsage.RequestCount != 3 || shortUsage.InputTokens != 16 ||
		shortUsage.OutputTokens != 7 || shortUsage.TotalTokens != 23 ||
		shortUsage.EstimatedReferenceCostNanoUSD != "29" ||
		response.Snapshot.QuotaWindows[2].ObservedUsage != nil {
		t.Fatalf("enriched snapshot = %#v", response.Snapshot)
	}
}

func TestEnrichCredentialObservationUsageSkipsNonCurrentObservation(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).UnixMilli()
	expired := now.UnixMilli()
	resetAt := now.Add(2 * time.Hour).UnixMilli()
	windowSeconds := int64((5 * time.Hour) / time.Second)
	tests := []struct {
		name         string
		state        string
		freshUntilMS *int64
	}{
		{name: "stale", state: string(models.CredentialObservationStale), freshUntilMS: &future},
		{name: "error", state: string(models.CredentialObservationError), freshUntilMS: &future},
		{name: "missing fresh until", state: string(models.CredentialObservationFresh)},
		{name: "expired", state: string(models.CredentialObservationFresh), freshUntilMS: &expired},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			fixture.service.now = func() time.Time { return now }
			reader := &recordingCredentialWindowUsageReader{}
			fixture.service.credentialWindowUsage = reader
			response := CredentialObservationResponse{
				State:        test.state,
				FreshUntilMS: test.freshUntilMS,
				Snapshot: &CredentialObservationSnapshot{QuotaWindows: []ObservationQuotaWindow{{
					ID: "account", Scope: "account", ResetAtMS: &resetAt, WindowSeconds: &windowSeconds,
				}}},
			}

			fixture.service.enrichCredentialObservationUsage(t.Context(), 37, &response)

			if len(reader.queries) != 0 || response.Snapshot.QuotaWindows[0].ObservedUsage != nil {
				t.Fatalf("non-current observation was enriched: queries=%#v response=%#v", reader.queries, response)
			}
		})
	}
}

func TestRefreshCredentialObservationPublishesAndClearsQuotaRoutingState(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 15, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	resetAt := now.Add(7 * 24 * time.Hour).Unix()
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":100,"reset_at":%d}}
		}`, resetAt))}, nil
	})
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err != nil {
		t.Fatal(err)
	}
	if candidates := fixture.registry.CollectCredentialCandidates([]uint{groupID}, nil, now); len(candidates) != 0 {
		t.Fatalf("exhausted candidates = %#v", candidates)
	}

	now = now.Add(time.Minute)
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{}, errors.New("upstream unavailable")
	})
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err == nil {
		t.Fatal("forced failed refresh error = nil")
	}
	if candidates := fixture.registry.CollectCredentialCandidates([]uint{groupID}, nil, now); len(candidates) != 0 {
		t.Fatalf("failed observation lost quota protection = %#v", candidates)
	}
}

func TestApplyCredentialQuotaObservationUsesBottleneckReset(t *testing.T) {
	fixture, _, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 15, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	bottleneckReset := now.Add(20 * time.Minute).UnixMilli()
	otherReset := now.Add(7 * 24 * time.Hour).UnixMilli()
	freshUntil := now.Add(time.Hour).UnixMilli()
	exhausted := 1.0
	available := 0.5
	response := CredentialObservationResponse{
		State:        string(models.CredentialObservationFresh),
		FreshUntilMS: &freshUntil,
		Snapshot: &CredentialObservationSnapshot{QuotaWindows: []ObservationQuotaWindow{
			{ID: "short", Scope: "account", Utilization: &exhausted, ResetAtMS: &bottleneckReset, State: "exhausted"},
			{ID: "long", Scope: "account", Utilization: &available, ResetAtMS: &otherReset, State: "available"},
		}},
	}

	fixture.service.applyCredentialQuotaObservation(credentialID, &response)
	views := fixture.registry.Snapshot()
	if len(views) != 1 || !views[0].QuotaResetAt.Equal(time.UnixMilli(bottleneckReset)) {
		t.Fatalf("quota runtime views = %#v, want bottleneck reset %v", views, time.UnixMilli(bottleneckReset))
	}
}

func TestApplyCredentialQuotaObservationDoesNotBlockAccountForModelGroupExhaustion(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 15, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	resetAt := now.Add(7 * 24 * time.Hour).UnixMilli()
	freshUntil := now.Add(time.Hour).UnixMilli()
	exhausted := 1.0
	response := CredentialObservationResponse{
		State:        string(models.CredentialObservationFresh),
		FreshUntilMS: &freshUntil,
		Snapshot: &CredentialObservationSnapshot{QuotaWindows: []ObservationQuotaWindow{{
			ID: "gemini-weekly", Scope: "model", Utilization: &exhausted,
			ResetAtMS: &resetAt, State: "exhausted",
		}}},
	}

	fixture.service.applyCredentialQuotaObservation(credentialID, &response)
	if candidates := fixture.registry.CollectCredentialCandidates([]uint{groupID}, nil, now); len(candidates) != 1 {
		t.Fatalf("model-group quota blocked whole credential: %#v", candidates)
	}
}

func TestDrainCommittedOperationsRestoresFreshQuotaRoutingState(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 16, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	resetAt := now.Add(7 * 24 * time.Hour).Unix()
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":100,"reset_at":%d}}
		}`, resetAt))}, nil
	})
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err != nil {
		t.Fatal(err)
	}
	if !fixture.registry.SetCredentialQuotaObservation(credentialID, nil, time.Time{}, time.Time{}) {
		t.Fatal("clear quota observation")
	}
	if err := fixture.service.DrainCommittedOperations(t.Context()); err != nil {
		t.Fatal(err)
	}
	if candidates := fixture.registry.CollectCredentialCandidates([]uint{groupID}, nil, now); len(candidates) != 0 {
		t.Fatalf("restored exhausted candidates = %#v", candidates)
	}
}

func TestRecoverCommittedRuntimeRestoresFreshQuotaRoutingState(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 16, 30, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	resetAt := now.Add(7 * 24 * time.Hour).Unix()
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":100,"reset_at":%d}}
		}`, resetAt))}, nil
	})
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.recoverCommittedRuntime(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if candidates := fixture.registry.CollectCredentialCandidates([]uint{groupID}, nil, now); len(candidates) != 0 {
		t.Fatalf("recovered exhausted candidates = %#v", candidates)
	}
}

func TestRefreshDueCredentialObservationsPollsOnlyMissingOrExpiringAccounts(t *testing.T) {
	fixture, _, _ := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 17, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	calls := 0
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		calls++
		return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":20,"reset_at":%d}}
		}`, now.Add(4*time.Hour).Unix()))}, nil
	})

	fixture.service.refreshDueCredentialObservations(t.Context())
	fixture.service.refreshDueCredentialObservations(t.Context())
	if calls != 1 {
		t.Fatalf("immediate sweep calls = %d, want 1", calls)
	}
	now = now.Add(51 * time.Minute)
	fixture.service.refreshDueCredentialObservations(t.Context())
	if calls != 2 {
		t.Fatalf("near-expiry sweep calls = %d, want 2", calls)
	}
}

func TestDueCredentialObservationTargetsRefreshesChangedAccountIdentity(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 17, 30, 0, 0, time.UTC)
	freshUntil := now.Add(time.Hour).UnixMilli()
	nextAllowed := now.Add(4 * time.Minute).UnixMilli()
	observedAt := now.Add(-time.Minute).UnixMilli()
	if err := fixture.db.Create(&models.CredentialObservation{
		CredentialID: credentialID, IdentityFingerprint: "previous-account-identity",
		SchemaVersion: 1, ObservationVersion: 1, SnapshotJSON: models.JSON(`{"quota_windows":[]}`),
		State: models.CredentialObservationFresh, ObservedAtMS: &observedAt,
		FreshUntilMS: &freshUntil, NextAllowedAtMS: &nextAllowed, UpdatedAtMS: observedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}

	targets, err := fixture.service.dueCredentialObservationTargets(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].groupID != groupID || targets[0].credentialID != credentialID {
		t.Fatalf("targets = %#v", targets)
	}
	upstreamCalls := 0
	fixture.service.now = func() time.Time { return now }
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		upstreamCalls++
		return codex.AccountObservation{Payload: []byte(`{}`)}, nil
	})
	fixture.service.refreshDueCredentialObservations(t.Context())
	if upstreamCalls != 1 {
		t.Fatalf("upstream calls = %d, want identity change to bypass old throttle", upstreamCalls)
	}
}

type recordingCredentialWindowUsageReader struct {
	queries []requestlog.CredentialWindowUsageQuery
	result  requestlog.CredentialWindowUsage
}

func (reader *recordingCredentialWindowUsageReader) QueryCredentialWindowUsage(
	_ context.Context,
	query requestlog.CredentialWindowUsageQuery,
) (requestlog.CredentialWindowUsage, error) {
	reader.queries = append(reader.queries, query)
	result := reader.result
	result.Source = query.Source
	return result, nil
}

type recordingCredentialActivityReader struct {
	queries []requestlog.CredentialActivityQuery
	result  map[uint]requestlog.CredentialActivity
	err     error
}

func (reader *recordingCredentialActivityReader) QueryCredentialActivity(
	_ context.Context,
	query requestlog.CredentialActivityQuery,
) (map[uint]requestlog.CredentialActivity, error) {
	reader.queries = append(reader.queries, query)
	return reader.result, reader.err
}

func TestEnrichCredentialDailyUsageQueriesAttemptActivity(t *testing.T) {
	fixture, _, _ := newSubscriptionCredentialFixture(t)
	now := time.UnixMilli(1_800_000_000_000)
	fixture.service.now = func() time.Time { return now }
	lastUsedAtMS := now.Add(-time.Minute).UnixMilli()
	reader := &recordingCredentialActivityReader{result: map[uint]requestlog.CredentialActivity{
		37: {
			CredentialID: 37, LastUsedAtMS: &lastUsedAtMS,
			SuccessCount: 15, FailureCount: 3, DataComplete: true,
		},
	}}
	fixture.service.credentialActivity = reader
	item := CredentialItemResponse{
		CredentialID: 37, ConnectionType: string(models.ConnectionTypeSubscription),
	}

	fixture.service.enrichCredentialDailyUsage(t.Context(), 37, &item)

	if len(reader.queries) != 1 {
		t.Fatalf("usage queries = %#v, want exactly one day window", reader.queries)
	}
	query := reader.queries[0]
	if !reflect.DeepEqual(query.CredentialIDs, []uint{37}) ||
		query.ToMS != now.UnixMilli() ||
		query.FromMS != now.Add(-24*time.Hour).UnixMilli() {
		t.Fatalf("day window query = %#v", query)
	}
	if item.DailyUsage == nil || item.DailyUsage.WindowSeconds != 86_400 ||
		item.DailyUsage.SuccessCount != 15 || item.DailyUsage.FailureCount != 3 ||
		!item.DailyUsage.DataComplete || item.LastUsedAtMS == nil ||
		*item.LastUsedAtMS != lastUsedAtMS {
		t.Fatalf("credential activity = %#v / %#v", item.LastUsedAtMS, item.DailyUsage)
	}
}

func TestEnrichCredentialDailyUsageLeavesItemUntouchedOnQueryFailure(t *testing.T) {
	fixture, _, _ := newSubscriptionCredentialFixture(t)
	fixture.service.now = func() time.Time { return time.UnixMilli(1_800_000_000_000) }
	fixture.service.credentialActivity = &recordingCredentialActivityReader{err: errors.New("activity unavailable")}
	item := CredentialItemResponse{
		CredentialID: 37, ConnectionType: string(models.ConnectionTypeSubscription),
	}

	fixture.service.enrichCredentialDailyUsage(t.Context(), 37, &item)

	if item.DailyUsage != nil {
		t.Fatalf("daily usage = %#v, want nil when the aggregate is unavailable", item.DailyUsage)
	}
}

func TestEnrichCredentialActivitiesBatchesSubscriptionItems(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.UnixMilli(1_800_000_000_000)
	fixture.service.now = func() time.Time { return now }
	reader := &recordingCredentialActivityReader{result: map[uint]requestlog.CredentialActivity{
		37: {CredentialID: 37, SuccessCount: 4, DataComplete: true},
		39: {CredentialID: 39, FailureCount: 2, DataComplete: true},
	}}
	fixture.service.credentialActivity = reader
	items := []CredentialItemResponse{
		{CredentialID: 37, ConnectionType: string(models.ConnectionTypeSubscription)},
		{CredentialID: 38, ConnectionType: string(models.ConnectionTypeAPIKey)},
		{CredentialID: 39, ConnectionType: string(models.ConnectionTypeSubscription)},
	}

	fixture.service.enrichCredentialActivities(t.Context(), items)

	if len(reader.queries) != 1 ||
		!reflect.DeepEqual(reader.queries[0].CredentialIDs, []uint{37, 39}) {
		t.Fatalf("activity queries = %#v, want one subscription batch", reader.queries)
	}
	if items[0].DailyUsage == nil || items[0].DailyUsage.SuccessCount != 4 ||
		items[1].DailyUsage != nil || items[2].DailyUsage == nil ||
		items[2].DailyUsage.FailureCount != 2 {
		t.Fatalf("enriched items = %#v", items)
	}
}

func TestRefreshCredentialObservationPersistsNormalizationFailure(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.UnixMilli(1_800_000_000_000)
	fixture.service.now = func() time.Time { return now }
	calls := 0
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		calls++
		return codex.AccountObservation{Payload: []byte(`[]`)}, nil
	})

	failed, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err == nil || failed.State != string(models.CredentialObservationError) ||
		failed.LastErrorCode != "observation_payload_invalid" {
		t.Fatalf("failed observation = %#v, %v", failed, err)
	}
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err == nil {
		t.Fatal("second refresh error = nil")
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}

func TestRefreshCredentialObservationUsesBoundedUpstreamContext(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	hasBoundedDeadline := false
	setCodexAccountObservation(fixture.service, func(ctx context.Context, _ codex.Credential) (codex.AccountObservation, error) {
		deadline, ok := ctx.Deadline()
		hasBoundedDeadline = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 31*time.Second
		return codex.AccountObservation{}, errors.New("upstream unavailable")
	})

	_, _ = fixture.service.RefreshCredentialObservation(context.Background(), groupID, credentialID)
	if !hasBoundedDeadline {
		t.Fatal("observation refresh did not receive a bounded upstream context")
	}
}

func TestRefreshCredentialObservationRejectsNonReadyCredentialBeforeUpstream(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	if err := fixture.db.Model(&models.Credential{}).Where("id = ?", credentialID).
		Update("auth_state", models.CredentialAuthStateOutcomeUnknown).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		calls++
		return codex.AccountObservation{}, nil
	})

	_, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if !errors.Is(err, app_errors.ErrCredentialAuthOutcomeUnknown) || calls != 0 {
		t.Fatalf("RefreshCredentialObservation() error/calls = %v/%d", err, calls)
	}
}

func TestConcurrentObservationRefreshIsSingleflight(t *testing.T) {
	t.Parallel()

	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	calls := 0
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		calls++
		once.Do(func() { close(started) })
		<-release
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{}}`)}, nil
	})
	results := make(chan error, 2)
	go func() {
		_, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
		results <- err
	}()
	<-started
	go func() {
		_, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
		results <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		fixture.service.observationMu.Lock()
		flight := fixture.service.observationFlights[observationFlightKey{groupID: groupID, credentialID: credentialID}]
		joined := flight != nil && flight.joined > 0
		fixture.service.observationMu.Unlock()
		if joined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second refresh did not join the in-flight request")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("refresh error = %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestPostMutationObservationRefreshRunsAfterExistingFlight(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		call := calls.Add(1)
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
			return codex.AccountObservation{Payload: []byte(`{"rate_limit":{"primary_window":{"used_percent":90}}}`)}, nil
		}
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{"primary_window":{"used_percent":20}}}`)}, nil
	})

	normalResult := make(chan CredentialObservationResponse, 1)
	normalError := make(chan error, 1)
	go func() {
		result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
		normalResult <- result
		normalError <- err
	}()
	<-firstStarted
	forcedResult := make(chan CredentialObservationResponse, 1)
	forcedError := make(chan error, 1)
	go func() {
		result, err := fixture.service.refreshCredentialObservation(
			t.Context(),
			groupID,
			credentialID,
			observationRefreshAfterMutation,
		)
		forcedResult <- result
		forcedError <- err
	}()
	close(releaseFirst)

	if err := <-normalError; err != nil {
		t.Fatalf("normal refresh: %v", err)
	}
	if err := <-forcedError; err != nil {
		t.Fatalf("forced refresh: %v", err)
	}
	first := <-normalResult
	second := <-forcedResult
	if calls.Load() != 2 || first.ObservationVersion != 1 || second.ObservationVersion != 2 ||
		second.Snapshot == nil || second.Snapshot.QuotaWindows[0].Utilization == nil ||
		*second.Snapshot.QuotaWindows[0].Utilization != 0.2 {
		t.Fatalf("calls/results = %d / %#v / %#v", calls.Load(), first, second)
	}
}

func TestObservationSingleflightKeepsGroupAndCredentialBoundTogether(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		close(started)
		<-release
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{}}`)}, nil
	})
	validDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.RefreshCredentialObservation(context.Background(), groupID, credentialID)
		validDone <- err
	}()
	<-started

	_, invalidErr := fixture.service.RefreshCredentialObservation(t.Context(), groupID+999, credentialID)
	if !errors.Is(invalidErr, app_errors.ErrResourceNotFound) {
		t.Fatalf("cross-group refresh error = %v", invalidErr)
	}
	close(release)
	if err := <-validDone; err != nil {
		t.Fatalf("valid refresh error = %v", err)
	}
}

func TestSubscriptionCredentialCollectionAndDetailIncludeCachedObservation(t *testing.T) {
	t.Parallel()

	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"plan_type":"pro","rate_limit":{"secondary_window":{"limit_window_seconds":604800,"used_percent":65}}}`)}, nil
	})
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err != nil {
		t.Fatal(err)
	}

	collection, err := fixture.service.ListGroupCredentials(t.Context(), groupID, CredentialCollectionQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 1 || collection.Items[0].SecretVersion != 1 ||
		collection.Items[0].Observation == nil || collection.Items[0].Observation.State != string(models.CredentialObservationFresh) ||
		collection.Items[0].Observation.Snapshot == nil {
		t.Fatalf("collection = %#v", collection)
	}

	detail, err := fixture.service.GetCredentialDetail(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Credential.CredentialID != credentialID || detail.Observation.Snapshot == nil ||
		detail.Observation.Snapshot.Plan.Name != "Pro 20x" {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestSubscriptionCredentialCollectionDefersActivityUntilDetailAndExposesEmail(t *testing.T) {
	t.Parallel()

	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.UnixMilli(1_800_000_000_000)
	fixture.service.now = func() time.Time { return now }
	lastUsedAtMS := now.Add(-2 * time.Minute).UnixMilli()
	reader := &recordingCredentialActivityReader{result: map[uint]requestlog.CredentialActivity{
		credentialID: {
			CredentialID: credentialID, LastUsedAtMS: &lastUsedAtMS,
			SuccessCount: 12, FailureCount: 3, DataComplete: true,
		},
	}}
	fixture.service.credentialActivity = reader

	collection, err := fixture.service.ListGroupCredentials(
		t.Context(), groupID, CredentialCollectionQuery{Page: 1, PageSize: 20},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 1 {
		t.Fatalf("collection items = %#v", collection.Items)
	}
	if len(reader.queries) != 0 || collection.Items[0].LastUsedAtMS != nil ||
		collection.Items[0].DailyUsage != nil {
		t.Fatalf("collection queried or exposed lazy activity = %#v / %#v", reader.queries, collection.Items[0])
	}
	encodedAccount, err := json.Marshal(collection.Items[0].Account)
	if err != nil {
		t.Fatal(err)
	}
	var account map[string]any
	if err := json.Unmarshal(encodedAccount, &account); err != nil {
		t.Fatal(err)
	}
	if account["email"] != "observation@example.com" {
		t.Fatalf("collection account = %s, want full subscription email", encodedAccount)
	}

	detail, err := fixture.service.GetCredentialDetail(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.queries) != 1 || detail.Credential.LastUsedAtMS == nil ||
		*detail.Credential.LastUsedAtMS != lastUsedAtMS || detail.Credential.DailyUsage == nil ||
		detail.Credential.DailyUsage.SuccessCount != 12 || detail.Credential.DailyUsage.FailureCount != 3 {
		t.Fatalf("detail activity = %#v / %#v", reader.queries, detail.Credential)
	}
}

func newSubscriptionCredentialFixture(t *testing.T) (serviceFixture, uint, uint) {
	t.Helper()
	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-observation", "observation@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription observation"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var row models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	return fixture, created.GroupID, row.ID
}

func observationJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
