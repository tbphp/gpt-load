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
	"gpt-load/internal/codex"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
)

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
	if snapshot.Plan.Name != "pro" || snapshot.ResetCreditsAvailable == nil || *snapshot.ResetCreditsAvailable != 2 || len(snapshot.QuotaWindows) != 3 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.QuotaWindows[0].ID != "gpt-5-2-primary" || snapshot.QuotaWindows[0].State != "exhausted" || !snapshot.QuotaWindows[0].IsPrimary {
		t.Fatalf("primary = %#v", snapshot.QuotaWindows[0])
	}
	if snapshot.QuotaWindows[1].ID != "secondary" || snapshot.QuotaWindows[2].ID != "primary" {
		t.Fatalf("window order = %#v", snapshot.QuotaWindows)
	}
	if snapshot.QuotaWindows[0].Label != "gpt-5.2 · 7d" ||
		snapshot.QuotaWindows[1].Label != "7d" || snapshot.QuotaWindows[2].Label != "5h" {
		t.Fatalf("window labels = %#v", snapshot.QuotaWindows)
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

func TestRefreshCredentialObservationPersistsLKGAndThrottles(t *testing.T) {
	t.Parallel()

	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.UnixMilli(1_800_000_000_000)
	fixture.service.now = func() time.Time { return now }
	calls := 0
	fixture.service.observeCodexAccount = func(_ context.Context, credential codex.Credential) (codex.AccountObservation, error) {
		calls++
		if credential.AccountID != "account-observation" {
			t.Fatalf("credential = %#v", credential)
		}
		return codex.AccountObservation{Payload: []byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":40,"reset_at":1800001000}}}`)}, nil
	}
	first, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != string(models.CredentialObservationFresh) || first.Snapshot == nil || first.NextAllowedAtMS == nil || calls != 1 {
		t.Fatalf("first = %#v, calls=%d", first, calls)
	}
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err == nil {
		t.Fatal("second refresh should be throttled")
	} else {
		var apiErr *app_errors.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrObservationRefreshThrottled.Code {
			t.Fatalf("second error = %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}

	now = now.Add(6 * time.Minute)
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		calls++
		return codex.AccountObservation{}, errors.New("upstream unavailable")
	}
	failed, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err == nil || failed.Snapshot == nil || failed.State != string(models.CredentialObservationError) {
		t.Fatalf("failed = %#v, %v", failed, err)
	}
	if !reflect.DeepEqual(failed.Snapshot, first.Snapshot) {
		t.Fatalf("LKG changed = %#v / %#v", first.Snapshot, failed.Snapshot)
	}
}

func TestRefreshCredentialObservationEnrichesResetCreditDetails(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{
			"plan_type":"plus",
			"rate_limit_reset_credits":{"available_count":1}
		}`)}, nil
	}
	detailCalls := 0
	fixture.service.observeCodexResetCredits = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		detailCalls++
		return codex.AccountObservation{Payload: []byte(`{
			"available_count":2,
			"credits":[
				{"status":"available","expires_at":"2026-09-03T00:00:00Z"},
				{"status":"available","expires_at":"2026-09-02T00:00:00Z"}
			]
		}`)}, nil
	}

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
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"rate_limit_reset_credits":{"available_count":2}}`)}, nil
	}
	fixture.service.observeCodexResetCredits = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{}, errors.New("details unavailable")
	}

	result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot == nil || result.Snapshot.ResetCreditsAvailable == nil ||
		*result.Snapshot.ResetCreditsAvailable != 2 || len(result.Snapshot.ResetCredits) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestEnrichCredentialObservationUsageSelectsExactAndHourlySources(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	shortReset := now.Add(2 * time.Hour).UnixMilli()
	longReset := now.Add(3 * 24 * time.Hour).UnixMilli()
	modelReset := now.Add(time.Hour).UnixMilli()
	shortSeconds := int64((5 * time.Hour) / time.Second)
	longSeconds := int64((7 * 24 * time.Hour) / time.Second)
	modelSeconds := int64(time.Hour / time.Second)
	response := CredentialObservationResponse{
		State: string(models.CredentialObservationFresh),
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
	if reader.queries[0].Source != requestlog.CredentialWindowUsageSourceRequestLogs ||
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

func TestRefreshCredentialObservationPublishesAndClearsQuotaRoutingState(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 15, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	resetAt := now.Add(7 * 24 * time.Hour).Unix()
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":100,"reset_at":%d}}
		}`, resetAt))}, nil
	}
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err != nil {
		t.Fatal(err)
	}
	if candidates := fixture.registry.CollectCredentialCandidates([]uint{groupID}, nil, now); len(candidates) != 0 {
		t.Fatalf("exhausted candidates = %#v", candidates)
	}

	now = now.Add(time.Minute)
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{}, errors.New("upstream unavailable")
	}
	if _, err := fixture.service.refreshCredentialObservation(t.Context(), groupID, credentialID, true); err == nil {
		t.Fatal("forced failed refresh error = nil")
	}
	if candidates := fixture.registry.CollectCredentialCandidates([]uint{groupID}, nil, now); len(candidates) != 1 {
		t.Fatalf("failed observation fallback candidates = %#v", candidates)
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

func TestDrainCommittedOperationsRestoresFreshQuotaRoutingState(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.August, 14, 16, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	resetAt := now.Add(7 * 24 * time.Hour).Unix()
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":100,"reset_at":%d}}
		}`, resetAt))}, nil
	}
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
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":100,"reset_at":%d}}
		}`, resetAt))}, nil
	}
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
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		calls++
		return codex.AccountObservation{Payload: []byte(fmt.Sprintf(`{
			"rate_limit":{"primary_window":{"limit_window_seconds":18000,"used_percent":20,"reset_at":%d}}
		}`, now.Add(4*time.Hour).Unix()))}, nil
	}

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
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		upstreamCalls++
		return codex.AccountObservation{Payload: []byte(`{}`)}, nil
	}
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

func TestRefreshCredentialObservationPersistsNormalizationFailure(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.UnixMilli(1_800_000_000_000)
	fixture.service.now = func() time.Time { return now }
	calls := 0
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		calls++
		return codex.AccountObservation{Payload: []byte(`[]`)}, nil
	}

	failed, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err == nil || failed.State != string(models.CredentialObservationError) ||
		failed.NextAllowedAtMS == nil || failed.LastErrorCode != "observation_payload_invalid" {
		t.Fatalf("failed observation = %#v, %v", failed, err)
	}
	if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err == nil {
		t.Fatal("second refresh error = nil")
	} else {
		var apiErr *app_errors.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrObservationRefreshThrottled.Code {
			t.Fatalf("second refresh error = %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
	}
}

func TestRefreshCredentialObservationUsesBoundedUpstreamContext(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	hasBoundedDeadline := false
	fixture.service.observeCodexAccount = func(ctx context.Context, _ codex.Credential) (codex.AccountObservation, error) {
		deadline, ok := ctx.Deadline()
		hasBoundedDeadline = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 31*time.Second
		return codex.AccountObservation{}, errors.New("upstream unavailable")
	}

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
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		calls++
		return codex.AccountObservation{}, nil
	}

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
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		calls++
		once.Do(func() { close(started) })
		<-release
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{}}`)}, nil
	}
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

func TestForcedObservationRefreshRunsAfterExistingNonForcedFlight(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		call := calls.Add(1)
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
			return codex.AccountObservation{Payload: []byte(`{"rate_limit":{"primary_window":{"used_percent":90}}}`)}, nil
		}
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{"primary_window":{"used_percent":20}}}`)}, nil
	}

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
		result, err := fixture.service.refreshCredentialObservation(t.Context(), groupID, credentialID, true)
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
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		close(started)
		<-release
		return codex.AccountObservation{Payload: []byte(`{"rate_limit":{}}`)}, nil
	}
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
	fixture.service.observeCodexAccount = func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"plan_type":"pro","rate_limit":{"secondary_window":{"limit_window_seconds":604800,"used_percent":65}}}`)}, nil
	}
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
		detail.Observation.Snapshot.Plan.Name != "pro" {
		t.Fatalf("detail = %#v", detail)
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
