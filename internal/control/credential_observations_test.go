package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
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
	fixture.service.observeCodexAccount = func(_ context.Context, credential cpaembedded.CodexCredential) (cpaembedded.AccountObservation, error) {
		calls++
		if credential.AccountID != "account-observation" {
			t.Fatalf("credential = %#v", credential)
		}
		return cpaembedded.AccountObservation{Payload: []byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":40,"reset_at":1800001000}}}`)}, nil
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
	fixture.service.observeCodexAccount = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.AccountObservation, error) {
		calls++
		return cpaembedded.AccountObservation{}, errors.New("upstream unavailable")
	}
	failed, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
	if err == nil || failed.Snapshot == nil || failed.State != string(models.CredentialObservationError) {
		t.Fatalf("failed = %#v, %v", failed, err)
	}
	if !reflect.DeepEqual(failed.Snapshot, first.Snapshot) {
		t.Fatalf("LKG changed = %#v / %#v", first.Snapshot, failed.Snapshot)
	}
}

func TestRefreshCredentialObservationPersistsNormalizationFailure(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.UnixMilli(1_800_000_000_000)
	fixture.service.now = func() time.Time { return now }
	calls := 0
	fixture.service.observeCodexAccount = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.AccountObservation, error) {
		calls++
		return cpaembedded.AccountObservation{Payload: []byte(`[]`)}, nil
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
	fixture.service.observeCodexAccount = func(ctx context.Context, _ cpaembedded.CodexCredential) (cpaembedded.AccountObservation, error) {
		deadline, ok := ctx.Deadline()
		hasBoundedDeadline = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 31*time.Second
		return cpaembedded.AccountObservation{}, errors.New("upstream unavailable")
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
	fixture.service.observeCodexAccount = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.AccountObservation, error) {
		calls++
		return cpaembedded.AccountObservation{}, nil
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
	fixture.service.observeCodexAccount = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.AccountObservation, error) {
		calls++
		once.Do(func() { close(started) })
		<-release
		return cpaembedded.AccountObservation{Payload: []byte(`{"rate_limit":{}}`)}, nil
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

func TestObservationSingleflightKeepsGroupAndCredentialBoundTogether(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	fixture.service.observeCodexAccount = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.AccountObservation, error) {
		close(started)
		<-release
		return cpaembedded.AccountObservation{Payload: []byte(`{"rate_limit":{}}`)}, nil
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
	fixture.service.observeCodexAccount = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.AccountObservation, error) {
		return cpaembedded.AccountObservation{Payload: []byte(`{"plan_type":"pro","rate_limit":{"secondary_window":{"limit_window_seconds":604800,"used_percent":65}}}`)}, nil
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
