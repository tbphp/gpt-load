package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/catalog"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

type catalogSyncClientFunc func(context.Context, catalog.Metadata) (catalog.SyncResult, error)

func (function catalogSyncClientFunc) Sync(
	ctx context.Context,
	metadata catalog.Metadata,
) (catalog.SyncResult, error) {
	return function(ctx, metadata)
}

func TestCatalogSyncEmitsLifecycleLogsWithoutLeakingFailureDetails(t *testing.T) {
	fixture := newServiceFixture(t)
	var logs bytes.Buffer
	standardLogger := logrus.StandardLogger()
	previousOutput := standardLogger.Out
	previousFormatter := standardLogger.Formatter
	previousLevel := standardLogger.Level
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logrus.SetLevel(logrus.InfoLevel)
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
		logrus.SetLevel(previousLevel)
	})

	success := newCatalogSyncCoordinator(
		fixture.service,
		catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
			return catalogResultFixture(3000, "success", nil), nil
		}),
		"unused",
		catalog.Metadata{},
		false,
	)
	success.storeCache = func(string, catalog.SyncResult) error { return nil }
	success.applySnapshot = func(context.Context, *catalog.Snapshot) error { return nil }
	if _, err := success.Sync(t.Context(), CatalogSyncManual); err != nil {
		t.Fatal(err)
	}

	disabled := false
	fixture.service.modelsDevAutoSyncOverride = &disabled
	if _, err := success.Sync(t.Context(), CatalogSyncPeriodic); err != nil {
		t.Fatal(err)
	}

	const rawFailure = "secret upstream response body"
	failure := newCatalogSyncCoordinator(
		fixture.service,
		catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
			return catalog.SyncResult{}, errors.New(rawFailure)
		}),
		"unused",
		catalog.Metadata{},
		false,
	)
	if _, err := failure.Sync(t.Context(), CatalogSyncManual); err == nil {
		t.Fatal("failed catalog sync error = nil")
	}

	if strings.Contains(logs.String(), rawFailure) {
		t.Fatalf("catalog sync logs leaked upstream failure details: %s", logs.String())
	}
	events := controlEventsNamed(decodeControlJSONLogs(t, logs.Bytes()), "models_dev_catalog_sync")
	if len(events) != 5 {
		t.Fatalf("catalog sync lifecycle events = %d, want 5: %#v", len(events), events)
	}
	wantOutcomes := []string{"started", "succeeded", "skipped", "started", "failed"}
	for index, want := range wantOutcomes {
		if got := events[index]["outcome"]; got != want {
			t.Fatalf("catalog sync event %d outcome = %#v, want %q", index, got, want)
		}
		if _, exists := events[index]["plane"]; exists {
			t.Fatalf("catalog sync event %d contains redundant plane field: %#v", index, events[index])
		}
	}
	if events[2]["skip_reason"] != "auto_sync_disabled" {
		t.Fatalf("catalog sync skipped event = %#v", events[2])
	}
	if events[4]["error_code"] != "catalog_sync_failed" {
		t.Fatalf("catalog sync failed event = %#v", events[4])
	}
}

func TestApplyCatalogSnapshotLogsMissingAutomaticPricePriorityProviders(t *testing.T) {
	fixture := newServiceFixture(t)
	var logs bytes.Buffer
	standardLogger := logrus.StandardLogger()
	previousOutput := standardLogger.Out
	previousFormatter := standardLogger.Formatter
	previousLevel := standardLogger.Level
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logrus.SetLevel(logrus.WarnLevel)
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
		logrus.SetLevel(previousLevel)
	})

	priority := catalog.AutomaticPriceProviderPriority()
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		priority[0]: {ID: priority[0]},
	}}
	before := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		priority[0]: {ID: priority[0]},
	}}
	if err := fixture.service.applyCatalogSnapshot(t.Context(), snapshot); err != nil {
		t.Fatalf("applyCatalogSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(snapshot, before) {
		t.Fatalf("applyCatalogSnapshot() mutated snapshot:\n got %#v\nwant %#v", snapshot, before)
	}

	events := controlEventsNamed(
		decodeControlJSONLogs(t, logs.Bytes()),
		"models_dev_catalog_price_priority_missing",
	)
	if len(events) != 1 {
		t.Fatalf("priority-missing events = %d, want one: %#v", len(events), events)
	}
	event := events[0]
	if event["level"] != "warning" ||
		event["msg"] != "[CONTROL] Catalog missing automatic price priority providers" {
		t.Fatalf("priority-missing event metadata = %#v", event)
	}
	missing, ok := event["missing_provider_ids"].([]any)
	if !ok {
		t.Fatalf("missing_provider_ids = %#v, want []any", event["missing_provider_ids"])
	}
	if len(missing) != len(priority)-1 {
		t.Fatalf("missing_provider_ids length = %d, want %d: %#v", len(missing), len(priority)-1, missing)
	}
	for index, want := range priority[1:] {
		if missing[index] != want {
			t.Fatalf("missing_provider_ids[%d] = %#v, want %q", index, missing[index], want)
		}
	}
}

func TestApplyCatalogSnapshotDoesNotLogCompletePriorityAndLogsEachSuccess(t *testing.T) {
	t.Run("complete priority", func(t *testing.T) {
		fixture := newServiceFixture(t)
		var logs bytes.Buffer
		standardLogger := logrus.StandardLogger()
		previousOutput := standardLogger.Out
		previousFormatter := standardLogger.Formatter
		previousLevel := standardLogger.Level
		logrus.SetOutput(&logs)
		logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
		logrus.SetLevel(logrus.WarnLevel)
		t.Cleanup(func() {
			logrus.SetOutput(previousOutput)
			logrus.SetFormatter(previousFormatter)
			logrus.SetLevel(previousLevel)
		})

		providers := make(map[string]catalog.Provider)
		for _, providerID := range catalog.AutomaticPriceProviderPriority() {
			providers[providerID] = catalog.Provider{ID: providerID}
		}
		if err := fixture.service.applyCatalogSnapshot(t.Context(), &catalog.Snapshot{Providers: providers}); err != nil {
			t.Fatalf("applyCatalogSnapshot() error = %v", err)
		}
		if events := controlEventsNamed(
			decodeControlJSONLogs(t, logs.Bytes()),
			"models_dev_catalog_price_priority_missing",
		); len(events) != 0 {
			t.Fatalf("priority-missing events = %#v, want none", events)
		}
	})

	t.Run("each successful application", func(t *testing.T) {
		fixture := newServiceFixture(t)
		var logs bytes.Buffer
		standardLogger := logrus.StandardLogger()
		previousOutput := standardLogger.Out
		previousFormatter := standardLogger.Formatter
		previousLevel := standardLogger.Level
		logrus.SetOutput(&logs)
		logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
		logrus.SetLevel(logrus.WarnLevel)
		t.Cleanup(func() {
			logrus.SetOutput(previousOutput)
			logrus.SetFormatter(previousFormatter)
			logrus.SetLevel(previousLevel)
		})

		if err := fixture.service.applyCatalogSnapshot(t.Context(), &catalog.Snapshot{}); err != nil {
			t.Fatalf("first applyCatalogSnapshot() error = %v", err)
		}
		if err := fixture.service.applyCatalogSnapshot(t.Context(), &catalog.Snapshot{}); err != nil {
			t.Fatalf("second applyCatalogSnapshot() error = %v", err)
		}
		if events := controlEventsNamed(
			decodeControlJSONLogs(t, logs.Bytes()),
			"models_dev_catalog_price_priority_missing",
		); len(events) != 2 {
			t.Fatalf("priority-missing events = %d, want two: %#v", len(events), events)
		}
	})
}

func TestApplyCatalogSnapshotFailureDoesNotLogPriorityWarningOrPublishRuntime(t *testing.T) {
	fixture := newServiceFixture(t)
	oldPrice := int64(1)
	if err := fixture.db.Create(&models.ModelPrice{
		PriceScopeKey:                     "provider:openai",
		ModelID:                           "gpt",
		InputPriceNanoUSDPerMillionTokens: &oldPrice,
	}).Error; err != nil {
		t.Fatal(err)
	}
	oldTable, err := loadPriceTable(t.Context(), fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	fixture.priceRuntime.Publish(oldTable)
	oldSnapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": catalogProviderFixture("openai", "Old", "gpt", 1),
	}}
	fixture.catalogRuntime.Publish(oldSnapshot)
	if err := fixture.db.Exec(`
		CREATE TRIGGER reject_priority_warning_reconcile
		BEFORE UPDATE ON model_prices
		BEGIN
			SELECT RAISE(ABORT, 'forced catalog reconciliation failure');
		END
	`).Error; err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	standardLogger := logrus.StandardLogger()
	previousOutput := standardLogger.Out
	previousFormatter := standardLogger.Formatter
	previousLevel := standardLogger.Level
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logrus.SetLevel(logrus.WarnLevel)
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
		logrus.SetLevel(previousLevel)
	})

	newSnapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": catalogProviderFixture("openai", "New", "gpt", 9),
	}}
	if err := fixture.service.applyCatalogSnapshot(t.Context(), newSnapshot); err == nil {
		t.Fatal("applyCatalogSnapshot() error = nil, want reconciliation failure")
	}
	if events := controlEventsNamed(
		decodeControlJSONLogs(t, logs.Bytes()),
		"models_dev_catalog_price_priority_missing",
	); len(events) != 0 {
		t.Fatalf("priority-missing events after failure = %#v, want none", events)
	}
	if fixture.priceRuntime.Load() != oldTable || !reflect.DeepEqual(fixture.catalogRuntime.Load(), oldSnapshot) {
		t.Fatal("failed application published a new runtime")
	}
}

func TestCatalogBootstrapLoadsLKGAndTreatsMissingOrCorruptAsEmpty(t *testing.T) {
	missing := loadCatalogBootstrap(filepath.Join(t.TempDir(), "missing.json"))
	if missing.HasLKG || missing.Runtime == nil || missing.Runtime.Load() != nil {
		t.Fatalf("missing bootstrap = %#v, want empty runtime", missing)
	}

	corruptPath := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(corruptPath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := loadCatalogBootstrap(corruptPath)
	if corrupt.HasLKG || corrupt.Runtime == nil || corrupt.Runtime.Load() != nil {
		t.Fatalf("corrupt bootstrap = %#v, want empty runtime", corrupt)
	}
	if got, err := os.ReadFile(corruptPath); err != nil || string(got) != "not-json" {
		t.Fatalf("corrupt cache was changed during bootstrap: %q, %v", got, err)
	}

	validPath := filepath.Join(t.TempDir(), "catalog.json")
	result := catalogResultFixture(2000, "v2", map[string]catalog.Provider{
		"openai": catalogProviderFixture("openai", "OpenAI", "gpt-4o", 2_000_000_000),
	})
	if err := catalog.StoreCache(validPath, result); err != nil {
		t.Fatal(err)
	}
	valid := loadCatalogBootstrap(validPath)
	if !valid.HasLKG || valid.Metadata.ETag != "v2" || valid.Runtime.Load() == nil {
		t.Fatalf("valid bootstrap = %#v, want cached generation", valid)
	}
}

func TestCatalogStartupReconcilesDurableLKGBeforeAnyNetworkSync(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	seedCatalogPriceGroup(t, fixture, "startup-lkg", &providerID, []string{"gpt"})
	old := int64(1)
	if err := fixture.db.Create(&models.ModelPrice{
		PriceScopeKey: "provider:openai", ModelID: "gpt",
		InputPriceNanoUSDPerMillionTokens: &old,
	}).Error; err != nil {
		t.Fatal(err)
	}

	cachePath := filepath.Join(t.TempDir(), modelsDevCatalogCacheName)
	result := catalogResultFixture(100, "startup", map[string]catalog.Provider{
		"openai": catalogProviderFixture("openai", "OpenAI", "gpt", 9),
	})
	if err := catalog.StoreCache(cachePath, result); err != nil {
		t.Fatal(err)
	}
	bootstrap := loadCatalogBootstrap(cachePath)
	fixture.service.catalogRuntime = bootstrap.Runtime

	if err := fixture.service.EnsureInitialState(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertCatalogPriceRow(t, fixture, "provider:openai", "gpt", func(row models.ModelPrice) {
		if row.InputPriceNanoUSDPerMillionTokens == nil ||
			*row.InputPriceNanoUSDPerMillionTokens != 9 {
			t.Fatalf("startup reconciled row = %#v", row)
		}
	})
	if fixture.priceRuntime.Load() == nil || bootstrap.Runtime.Load() == nil {
		t.Fatal("startup did not publish local LKG price/catalog runtimes")
	}
}

func TestCatalogSyncSingleFlightJoinsManualStartupAndGroupTriggers(t *testing.T) {
	fixture := newServiceFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	client := catalogSyncClientFunc(func(ctx context.Context, _ catalog.Metadata) (catalog.SyncResult, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return catalogResultFixture(3000, "joined", nil), nil
		case <-ctx.Done():
			return catalog.SyncResult{}, ctx.Err()
		}
	})
	coordinator := newCatalogSyncCoordinator(
		fixture.service,
		client,
		filepath.Join(t.TempDir(), "catalog.json"),
		catalog.Metadata{},
		false,
	)
	coordinator.storeCache = func(string, catalog.SyncResult) error { return nil }

	type syncResult struct{ err error }
	results := make(chan syncResult, 4)
	for _, trigger := range []CatalogSyncTrigger{
		CatalogSyncManual,
		CatalogSyncStartup,
		CatalogSyncPeriodic,
		CatalogSyncGroup,
	} {
		trigger := trigger
		go func() {
			_, err := coordinator.Sync(t.Context(), trigger)
			results <- syncResult{err: err}
		}()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("catalog sync did not start")
	}
	waitForCatalogJoiners(t, coordinator, 3)
	if got := calls.Load(); got != 1 {
		t.Fatalf("network calls = %d, want one shared operation", got)
	}
	close(release)
	for range 4 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("joined sync error = %v", result.err)
			}
		case <-time.After(time.Second):
			t.Fatal("joined sync did not return")
		}
	}
}

func TestCatalogSyncCallerCancellationStopsWaitingWithoutCancelingSharedOperation(t *testing.T) {
	fixture := newServiceFixture(t)
	operationContexts := make(chan context.Context, 1)
	release := make(chan struct{})
	client := catalogSyncClientFunc(func(ctx context.Context, _ catalog.Metadata) (catalog.SyncResult, error) {
		operationContexts <- ctx
		select {
		case <-release:
			return catalogResultFixture(100, "shared", nil), nil
		case <-ctx.Done():
			return catalog.SyncResult{}, ctx.Err()
		}
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{}, false)
	coordinator.storeCache = func(string, catalog.SyncResult) error { return nil }
	coordinator.applySnapshot = func(context.Context, *catalog.Snapshot) error { return nil }

	callerCtx, cancelCaller := context.WithCancel(t.Context())
	firstDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Sync(callerCtx, CatalogSyncManual)
		firstDone <- err
	}()
	var operationCtx context.Context
	select {
	case operationCtx = <-operationContexts:
	case <-time.After(time.Second):
		t.Fatal("shared operation did not start")
	}
	joinedDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Sync(context.Background(), CatalogSyncStartup)
		joinedDone <- err
	}()
	waitForCatalogJoiners(t, coordinator, 1)

	cancelCaller()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller error = %v, want context canceled", err)
	}
	if err := operationCtx.Err(); err != nil {
		t.Fatalf("caller cancellation propagated to shared operation: %v", err)
	}
	close(release)
	if err := <-joinedDone; err != nil {
		t.Fatalf("joined waiter error = %v", err)
	}
}

func TestCatalogSync304PreservesPublishedGenerationsAndDoesNotStoreCache(t *testing.T) {
	fixture := newServiceFixture(t)
	oldSnapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": catalogProviderFixture("openai", "Old", "old", 1_000_000_000),
	}}
	fixture.catalogRuntime.Publish(oldSnapshot)
	oldTable, err := pricing.NewTable([]pricing.Rule{{
		Identity: pricing.Identity{ScopeKey: "provider:openai", ModelID: "old"},
		Prices:   pricing.Prices{Input: pricing.Price{Set: true, NanoUSDPerMillion: 1}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fixture.priceRuntime.Publish(oldTable)
	previous := catalog.Metadata{ETag: "old-etag", LastModified: "old-date", CheckedAtMillis: 10, SuccessfulFetchAtMillis: 10}
	client := catalogSyncClientFunc(func(_ context.Context, got catalog.Metadata) (catalog.SyncResult, error) {
		if !reflect.DeepEqual(got, previous) {
			t.Fatalf("conditional metadata = %#v, want %#v", got, previous)
		}
		updated := previous
		updated.CheckedAtMillis = 20
		return catalog.SyncResult{Metadata: updated, NotModified: true}, nil
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", previous, true)
	var stores atomic.Int32
	coordinator.storeCache = func(string, catalog.SyncResult) error {
		stores.Add(1)
		return nil
	}

	status, err := coordinator.Sync(t.Context(), CatalogSyncManual)
	if err != nil {
		t.Fatal(err)
	}
	if !status.NotModified || status.CheckedAtMS != 20 || status.SuccessfulFetchAtMS != 10 {
		t.Fatalf("status = %#v", status)
	}
	if stores.Load() != 0 {
		t.Fatalf("304 cache stores = %d, want 0", stores.Load())
	}
	if !reflect.DeepEqual(fixture.catalogRuntime.Load(), oldSnapshot) {
		t.Fatal("304 replaced CatalogRuntime")
	}
	if fixture.priceRuntime.Load() != oldTable {
		t.Fatal("304 replaced PriceRuntime")
	}
}

func TestCatalogSyncRejects304WithoutLastKnownGoodGeneration(t *testing.T) {
	fixture := newServiceFixture(t)
	previous := catalog.Metadata{}
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		return catalog.SyncResult{
			Metadata:    catalog.Metadata{CheckedAtMillis: 200},
			NotModified: true,
		}, nil
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", previous, false)
	coordinator.now = func() time.Time { return time.UnixMilli(250) }

	status, err := coordinator.Sync(t.Context(), CatalogSyncManual)
	if err == nil {
		t.Fatal("Sync() error = nil, want invalid 304 failure")
	}
	if status.Trigger != CatalogSyncManual || status.CheckedAtMS != 250 ||
		status.SuccessfulFetchAtMS != 0 || status.ErrorCode != "catalog_sync_failed" ||
		status.NotModified {
		t.Fatalf("invalid 304 status = %#v", status)
	}
	if coordinator.metadata != previous || coordinator.hasLastKnownGood() ||
		fixture.catalogRuntime.Load() != nil {
		t.Fatalf("invalid 304 advanced state: metadata=%#v hasLKG=%t runtime=%#v",
			coordinator.metadata, coordinator.hasLastKnownGood(), fixture.catalogRuntime.Load())
	}
}

func TestCatalogSyncFailuresRefreshLastCheckAndPreserveLastSuccessfulFetch(t *testing.T) {
	fixture := newServiceFixture(t)
	previous := catalog.Metadata{
		ETag:                    "last-good",
		CheckedAtMillis:         100,
		SuccessfulFetchAtMillis: 90,
	}
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		return catalog.SyncResult{}, errors.New("upstream response was invalid")
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", previous, true)
	coordinator.now = func() time.Time { return time.UnixMilli(250) }

	status, err := coordinator.Sync(t.Context(), CatalogSyncManual)
	if err == nil {
		t.Fatal("Sync() error = nil, want failure")
	}
	if status.CheckedAtMS != 250 || status.SuccessfulFetchAtMS != 90 ||
		status.ErrorCode != "catalog_sync_failed" {
		t.Fatalf("failure status = %#v", status)
	}
	if coordinator.metadata != previous {
		t.Fatalf("failure advanced conditional metadata: got %#v, want %#v", coordinator.metadata, previous)
	}
	if coordinator.last.CheckedAtMS != 250 || coordinator.last.SuccessfulFetchAtMS != 90 {
		t.Fatalf("last failure status = %#v", coordinator.last)
	}
}

func TestCatalogSyncCacheFailureUsesCurrentCheckWithoutAdvancingSuccessfulFetch(t *testing.T) {
	fixture := newServiceFixture(t)
	previous := catalog.Metadata{SuccessfulFetchAtMillis: 90, CheckedAtMillis: 100}
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		return catalogResultFixture(200, "new", nil), nil
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", previous, true)
	coordinator.now = func() time.Time { return time.UnixMilli(250) }
	coordinator.storeCache = func(string, catalog.SyncResult) error { return errors.New("disk full") }

	status, err := coordinator.Sync(t.Context(), CatalogSyncManual)
	if err == nil {
		t.Fatal("Sync() error = nil, want cache failure")
	}
	if status.CheckedAtMS != 250 || status.SuccessfulFetchAtMS != 90 ||
		status.ErrorCode != "catalog_sync_failed" {
		t.Fatalf("cache failure status = %#v", status)
	}
}

func TestCatalogSyncDisabledAutomaticTriggersDoNotCallNetworkButManualStillWorks(t *testing.T) {
	fixture := newServiceFixture(t)
	disabled := false
	fixture.service.modelsDevAutoSyncOverride = &disabled
	var calls atomic.Int32
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		calls.Add(1)
		return catalogResultFixture(100, "manual", nil), nil
	})
	coordinator := newCatalogSyncCoordinator(
		fixture.service,
		client,
		filepath.Join(t.TempDir(), "catalog.json"),
		catalog.Metadata{},
		false,
	)
	coordinator.storeCache = func(string, catalog.SyncResult) error { return nil }

	for _, trigger := range []CatalogSyncTrigger{CatalogSyncStartup, CatalogSyncPeriodic, CatalogSyncGroup} {
		status, err := coordinator.Sync(t.Context(), trigger)
		if err != nil || !status.Skipped {
			t.Fatalf("Sync(%s) = %#v, %v, want skipped", trigger, status, err)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("disabled automatic calls = %d, want 0", got)
	}
	status, err := coordinator.Sync(t.Context(), CatalogSyncManual)
	if err != nil || status.Skipped {
		t.Fatalf("manual Sync() = %#v, %v", status, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("manual calls = %d, want 1", got)
	}
}

func TestCatalogSyncSchedulerRetriesNoLKGAfterOneHourAndKeepsLKGOnDailyCadence(t *testing.T) {
	for _, test := range []struct {
		name             string
		hasLKG           bool
		wantRetryTimer   bool
		secondTriggerVia string
	}{
		{name: "no LKG", wantRetryTimer: true, secondTriggerVia: "retry"},
		{name: "has LKG", hasLKG: true, secondTriggerVia: "periodic"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			if test.hasLKG {
				fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{}})
			}
			calls := make(chan struct{}, 4)
			client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
				calls <- struct{}{}
				return catalog.SyncResult{}, errors.New("offline")
			})
			coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{}, test.hasLKG)
			periodic := newFakeRuntimeTicker()
			coordinator.newTicker = func(interval time.Duration) runtimeTicker {
				if interval != 24*time.Hour {
					t.Fatalf("periodic interval = %v, want 24h", interval)
				}
				return periodic
			}
			timers := make(chan *fakeCatalogTimer, 4)
			coordinator.newTimer = func(interval time.Duration) catalogSyncTimer {
				timer := newFakeCatalogTimer(interval)
				timers <- timer
				return timer
			}

			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				coordinator.Run(ctx)
				close(done)
			}()
			awaitCatalogCall(t, calls)
			waitForCatalogIdle(t, coordinator)

			if test.wantRetryTimer {
				var retry *fakeCatalogTimer
				select {
				case retry = <-timers:
				case <-time.After(time.Second):
					t.Fatal("no-LKG failure did not schedule retry")
				}
				if retry.interval != time.Hour {
					t.Fatalf("retry interval = %v, want 1h", retry.interval)
				}
				retry.ticks <- time.Now()
			} else {
				select {
				case timer := <-timers:
					t.Fatalf("LKG failure created retry timer %v", timer.interval)
				default:
				}
				periodic.ticks <- time.Now()
			}
			awaitCatalogCall(t, calls)
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("catalog scheduler did not stop")
			}
		})
	}
}

func TestCatalogSyncSchedulerDebouncesGroupChangeBursts(t *testing.T) {
	fixture := newServiceFixture(t)
	calls := make(chan struct{}, 4)
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		calls <- struct{}{}
		return catalog.SyncResult{
			Metadata:    catalog.Metadata{CheckedAtMillis: 10, SuccessfulFetchAtMillis: 1},
			NotModified: true,
		}, nil
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{
		CheckedAtMillis: 1, SuccessfulFetchAtMillis: 1,
	}, true)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{}})
	periodic := newFakeRuntimeTicker()
	coordinator.newTicker = func(time.Duration) runtimeTicker { return periodic }
	timers := make(chan *fakeCatalogTimer, 4)
	coordinator.newTimer = func(interval time.Duration) catalogSyncTimer {
		timer := newFakeCatalogTimer(interval)
		timers <- timer
		return timer
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		coordinator.Run(ctx)
		close(done)
	}()
	awaitCatalogCall(t, calls)
	waitForCatalogIdle(t, coordinator)

	coordinator.RequestGroupSync()
	first := awaitCatalogTimer(t, timers)
	coordinator.RequestGroupSync()
	second := awaitCatalogTimer(t, timers)
	if first.interval != modelsDevGroupDebounce || second.interval != modelsDevGroupDebounce ||
		!first.stopped.Load() {
		t.Fatalf("debounce timers = %#v/%#v", first, second)
	}
	second.ticks <- time.Now()
	awaitCatalogCall(t, calls)
	select {
	case <-calls:
		t.Fatal("group change burst caused more than one debounced sync")
	default:
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("catalog scheduler did not stop")
	}
}

func TestCatalogSyncSchedulerRunsImmediateSettingsTrigger(t *testing.T) {
	fixture := newServiceFixture(t)
	calls := make(chan struct{}, 2)
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		calls <- struct{}{}
		return catalog.SyncResult{
			Metadata:    catalog.Metadata{CheckedAtMillis: 10, SuccessfulFetchAtMillis: 1},
			NotModified: true,
		}, nil
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{
		CheckedAtMillis: 1, SuccessfulFetchAtMillis: 1,
	}, true)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{}})
	periodic := newFakeRuntimeTicker()
	coordinator.newTicker = func(time.Duration) runtimeTicker { return periodic }

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		coordinator.Run(ctx)
		close(done)
	}()
	awaitCatalogCall(t, calls)
	waitForCatalogIdle(t, coordinator)

	coordinator.RequestImmediateSync()
	awaitCatalogCall(t, calls)
	waitForCatalogIdle(t, coordinator)
	if coordinator.last.Trigger != CatalogSyncSettings {
		t.Fatalf("immediate trigger = %q, want %q", coordinator.last.Trigger, CatalogSyncSettings)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("catalog scheduler did not stop")
	}
}

func TestCatalogSyncSchedulerRetries304WithoutLKGAfterOneHour(t *testing.T) {
	fixture := newServiceFixture(t)
	calls := make(chan struct{}, 2)
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		calls <- struct{}{}
		return catalog.SyncResult{Metadata: catalog.Metadata{CheckedAtMillis: 20}, NotModified: true}, nil
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{}, false)
	periodic := newFakeRuntimeTicker()
	coordinator.newTicker = func(time.Duration) runtimeTicker { return periodic }
	timers := make(chan *fakeCatalogTimer, 2)
	coordinator.newTimer = func(interval time.Duration) catalogSyncTimer {
		timer := newFakeCatalogTimer(interval)
		timers <- timer
		return timer
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		coordinator.Run(ctx)
		close(done)
	}()
	awaitCatalogCall(t, calls)
	waitForCatalogIdle(t, coordinator)
	retry := awaitCatalogTimer(t, timers)
	if retry.interval != modelsDevInitialRetry {
		t.Fatalf("invalid 304 retry = %v, want 1h", retry.interval)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("catalog scheduler did not stop")
	}
}

func TestCatalogSyncShutdownWaitsForBlockedCacheAndPreventsLateReconcile(t *testing.T) {
	fixture := newServiceFixture(t)
	result := catalogResultFixture(100, "shutdown", nil)
	var calls atomic.Int32
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		calls.Add(1)
		return result, nil
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{}, false)
	periodic := newFakeRuntimeTicker()
	coordinator.newTicker = func(time.Duration) runtimeTicker { return periodic }
	cacheStarted := make(chan struct{})
	releaseCache := make(chan struct{})
	coordinator.storeCache = func(string, catalog.SyncResult) error {
		close(cacheStarted)
		<-releaseCache
		return nil
	}
	var reconcileCalls atomic.Int32
	coordinator.applySnapshot = func(context.Context, *catalog.Snapshot) error {
		reconcileCalls.Add(1)
		return nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		coordinator.Run(ctx)
		close(done)
	}()
	select {
	case <-cacheStarted:
	case <-time.After(time.Second):
		t.Fatal("catalog cache write did not start")
	}
	cancel()
	waitForCatalogShutdown(t, coordinator)
	select {
	case <-done:
		t.Fatal("Run returned while durable cache write was still blocked")
	default:
	}
	if _, err := coordinator.Sync(context.Background(), CatalogSyncManual); err == nil {
		t.Fatal("manual sync started after coordinator shutdown")
	}
	if calls.Load() != 1 {
		t.Fatalf("network calls after shutdown = %d, want 1", calls.Load())
	}
	close(releaseCache)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not join cache-blocked execute")
	}
	if reconcileCalls.Load() != 0 {
		t.Fatalf("reconcile calls after canceled cache = %d, want 0", reconcileCalls.Load())
	}
}

func TestCatalogSyncShutdownWaitsForBlockedReconcile(t *testing.T) {
	fixture := newServiceFixture(t)
	result := catalogResultFixture(100, "shutdown", nil)
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		return result, nil
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{}, false)
	coordinator.newTicker = func(time.Duration) runtimeTicker { return newFakeRuntimeTicker() }
	coordinator.storeCache = func(string, catalog.SyncResult) error { return nil }
	reconcileStarted := make(chan struct{})
	releaseReconcile := make(chan struct{})
	coordinator.applySnapshot = func(context.Context, *catalog.Snapshot) error {
		close(reconcileStarted)
		<-releaseReconcile
		return nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		coordinator.Run(ctx)
		close(done)
	}()
	select {
	case <-reconcileStarted:
	case <-time.After(time.Second):
		t.Fatal("catalog reconcile did not start")
	}
	cancel()
	waitForCatalogShutdown(t, coordinator)
	select {
	case <-done:
		t.Fatal("Run returned while reconcile was still blocked")
	default:
	}
	close(releaseReconcile)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not join reconcile-blocked execute")
	}
}

func TestCatalogSyncRunShutdownCancelsPreRuntimeManualOperation(t *testing.T) {
	fixture := newServiceFixture(t)
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var calls atomic.Int32
	client := catalogSyncClientFunc(func(ctx context.Context, _ catalog.Metadata) (catalog.SyncResult, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			close(canceled)
			return catalog.SyncResult{}, ctx.Err()
		case <-release:
			return catalog.SyncResult{}, errors.New("test released uncanceled operation")
		}
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{}, false)
	coordinator.newTicker = func(time.Duration) runtimeTicker { return newFakeRuntimeTicker() }
	var reconcileCalls atomic.Int32
	coordinator.applySnapshot = func(context.Context, *catalog.Snapshot) error {
		reconcileCalls.Add(1)
		return nil
	}

	manualDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Sync(context.Background(), CatalogSyncManual)
		manualDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("pre-runtime manual sync did not start")
	}

	runCtx, cancelRun := context.WithCancel(t.Context())
	runDone := make(chan struct{})
	go func() {
		coordinator.Run(runCtx)
		close(runDone)
	}()
	waitForCatalogJoiners(t, coordinator, 1)
	cancelRun()

	select {
	case <-canceled:
	case <-time.After(time.Second):
		releaseOnce.Do(func() { close(release) })
		<-runDone
		<-manualDone
		t.Fatal("runtime shutdown did not cancel the pre-runtime manual operation")
	}
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not join the canceled pre-runtime manual operation")
	}
	if err := <-manualDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("manual sync error = %v, want context canceled", err)
	}
	if calls.Load() != 1 || reconcileCalls.Load() != 0 {
		t.Fatalf("post-shutdown work = network %d, reconcile %d", calls.Load(), reconcileCalls.Load())
	}
}

func TestCatalogSyncReconcilesSlotsLKGManualCleanupAndStableTimestamps(t *testing.T) {
	fixture := newServiceFixture(t)
	openAI := "openai"
	missingProvider := "missing-provider"
	seedCatalogPriceGroup(t, fixture, "openai", &openAI, []string{"changed", "missing-model", "manual", "unchanged"})
	seedCatalogPriceGroup(t, fixture, "missing-provider", &missingProvider, []string{"missing-provider-model"})

	oldInput, oldOutput, oldRead, oldWrite := int64(1), int64(2), int64(3), int64(4)
	oldTiers := models.JSON(`[{"threshold_tokens":128000,"input_price_nano_usd_per_million_tokens":5}]`)
	rows := []models.ModelPrice{
		{PriceScopeKey: "provider:openai", ModelID: "changed", InputPriceNanoUSDPerMillionTokens: &oldInput, OutputPriceNanoUSDPerMillionTokens: &oldOutput, CacheReadPriceNanoUSDPerMillionTokens: &oldRead, CacheWritePriceNanoUSDPerMillionTokens: &oldWrite, UpdatedAtMS: 111},
		{PriceScopeKey: "provider:openai", ModelID: "missing-model", InputPriceNanoUSDPerMillionTokens: &oldInput, OutputPriceNanoUSDPerMillionTokens: &oldOutput, CacheReadPriceNanoUSDPerMillionTokens: &oldRead, CacheWritePriceNanoUSDPerMillionTokens: &oldWrite, ContextPriceTiers: oldTiers, UpdatedAtMS: 112},
		{PriceScopeKey: "provider:missing-provider", ModelID: "missing-provider-model", InputPriceNanoUSDPerMillionTokens: &oldInput, OutputPriceNanoUSDPerMillionTokens: &oldOutput, CacheReadPriceNanoUSDPerMillionTokens: &oldRead, CacheWritePriceNanoUSDPerMillionTokens: &oldWrite, ContextPriceTiers: oldTiers, UpdatedAtMS: 113},
		{PriceScopeKey: "provider:openai", ModelID: "manual", IsManual: true, UpdatedAtMS: 114},
		{PriceScopeKey: "provider:openai", ModelID: "stale", InputPriceNanoUSDPerMillionTokens: &oldInput, UpdatedAtMS: 115},
		{PriceScopeKey: "provider:openai", ModelID: "manual-stale", IsManual: true, UpdatedAtMS: 116},
		{PriceScopeKey: "provider:openai", ModelID: "unchanged", InputPriceNanoUSDPerMillionTokens: &oldInput, UpdatedAtMS: 117},
	}
	for index := range rows {
		if err := fixture.db.Create(&rows[index]).Error; err != nil {
			t.Fatal(err)
		}
	}

	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai", Name: "OpenAI", Models: map[string]catalog.Model{
				"changed": {
					ID: "changed", Name: "Changed", Cost: &catalog.ModelCost{Prices: pricing.Prices{
						Input: pricing.Price{Set: true, NanoUSDPerMillion: 9},
					}},
				},
				"manual":    {ID: "manual", Name: "Manual", Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: pricing.Price{Set: true, NanoUSDPerMillion: 99}}}},
				"unchanged": {ID: "unchanged", Name: "Unchanged", Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: pricing.Price{Set: true, NanoUSDPerMillion: 1}}}},
			},
		},
	}}
	if err := fixture.service.applyCatalogSnapshot(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}

	assertCatalogPriceRow(t, fixture, "provider:openai", "changed", func(row models.ModelPrice) {
		if row.InputPriceNanoUSDPerMillionTokens == nil || *row.InputPriceNanoUSDPerMillionTokens != 9 ||
			row.OutputPriceNanoUSDPerMillionTokens != nil || row.CacheReadPriceNanoUSDPerMillionTokens != nil || row.CacheWritePriceNanoUSDPerMillionTokens != nil {
			t.Fatalf("changed row = %#v, want exact replacement and cleared missing slots", row)
		}
	})
	assertCatalogPriceRow(t, fixture, "provider:openai", "missing-model", func(row models.ModelPrice) {
		if priceTestRowHasValue(row) {
			t.Fatalf("missing model retained automatic catalog values: %#v", row)
		}
	})
	assertCatalogPriceRow(t, fixture, "provider:missing-provider", "missing-provider-model", func(row models.ModelPrice) {
		if priceTestRowHasValue(row) {
			t.Fatalf("missing provider retained automatic catalog values: %#v", row)
		}
	})
	assertCatalogPriceRow(t, fixture, "provider:openai", "manual", func(row models.ModelPrice) {
		if !row.IsManual || row.InputPriceNanoUSDPerMillionTokens != nil || row.UpdatedAtMS != 114 {
			t.Fatalf("manual all-null row changed: %#v", row)
		}
	})
	assertCatalogPriceRow(t, fixture, "provider:openai", "unchanged", func(row models.ModelPrice) {
		if row.UpdatedAtMS != 117 {
			t.Fatalf("unchanged row timestamp = %d, want 117", row.UpdatedAtMS)
		}
	})
	assertCatalogPriceMissing(t, fixture, "provider:openai", "stale")
	assertCatalogPriceRow(t, fixture, "provider:openai", "manual-stale", func(row models.ModelPrice) {
		if !row.IsManual {
			t.Fatal("unreferenced manual row lost ownership")
		}
	})
}

func TestCatalogSyncReconcilesGroupAutomaticPricesAndPreservesManualRows(t *testing.T) {
	fixture := newServiceFixture(t)
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "custom", UpstreamURL: "https://custom.example/v1",
		Protocols: models.JSON(`["openai-completions"]`),
		Models:    models.JSON(`[{"id":"group-model","alias":""},{"id":"manual-group-model","alias":""}]`),
		Config:    models.JSON(`{}`), Enabled: true,
	})
	scopeKey, _ := pricing.GroupScopeKey(group.ID)
	oldInput, oldOutput, oldRead, oldWrite := int64(1), int64(2), int64(3), int64(4)
	oldTiers := models.JSON(`[{"threshold_tokens":128000,"input_price_nano_usd_per_million_tokens":5}]`)
	manualInput := int64(99)
	for _, row := range []models.ModelPrice{
		{
			PriceScopeKey: scopeKey, ModelID: "group-model",
			InputPriceNanoUSDPerMillionTokens:      &oldInput,
			OutputPriceNanoUSDPerMillionTokens:     &oldOutput,
			CacheReadPriceNanoUSDPerMillionTokens:  &oldRead,
			CacheWritePriceNanoUSDPerMillionTokens: &oldWrite,
			ContextPriceTiers:                      oldTiers, UpdatedAtMS: 111,
		},
		{
			PriceScopeKey: scopeKey, ModelID: "manual-group-model",
			InputPriceNanoUSDPerMillionTokens: &manualInput,
			IsManual:                          true, UpdatedAtMS: 112,
		},
	} {
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	zero := pricing.NanoUSD(0)
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai",
			Models: map[string]catalog.Model{
				"group-model": {
					ID: "group-model",
					Cost: &catalog.ModelCost{
						Prices: pricing.Prices{
							Input:     priceTestValue(8),
							CacheRead: pricing.Price{NanoUSDPerMillion: zero, Set: true},
						},
						ContextTiers: []pricing.ContextTier{{
							InputThresholdTokens: 256_000,
							Prices: pricing.Prices{
								Output: priceTestValue(16),
							},
						}},
					},
				},
				"manual-group-model": {
					ID:   "manual-group-model",
					Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(100)}},
				},
			},
		},
	}}

	if err := fixture.service.applyCatalogSnapshot(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	assertCatalogPriceRow(t, fixture, scopeKey, "group-model", func(row models.ModelPrice) {
		if row.IsManual || row.InputPriceNanoUSDPerMillionTokens == nil ||
			*row.InputPriceNanoUSDPerMillionTokens != 8 ||
			row.OutputPriceNanoUSDPerMillionTokens != nil ||
			row.CacheReadPriceNanoUSDPerMillionTokens == nil ||
			*row.CacheReadPriceNanoUSDPerMillionTokens != 0 ||
			row.CacheWritePriceNanoUSDPerMillionTokens != nil {
			t.Fatalf("automatic Group row = %#v", row)
		}
		var tiers []models.ContextPriceTier
		if err := json.Unmarshal(row.ContextPriceTiers, &tiers); err != nil {
			t.Fatal(err)
		}
		if len(tiers) != 1 || tiers[0].ThresholdTokens != 256_000 ||
			tiers[0].OutputPriceNanoUSDPerMillionTokens == nil ||
			*tiers[0].OutputPriceNanoUSDPerMillionTokens != 16 {
			t.Fatalf("automatic Group tiers = %#v", tiers)
		}
	})
	assertCatalogPriceRow(t, fixture, scopeKey, "manual-group-model", func(row models.ModelPrice) {
		if !row.IsManual || row.InputPriceNanoUSDPerMillionTokens == nil ||
			*row.InputPriceNanoUSDPerMillionTokens != 99 || row.UpdatedAtMS != 112 {
			t.Fatalf("manual Group row changed: %#v", row)
		}
	})
}

func TestCatalogSyncReconcileFailurePublishesNeitherRuntimeAndKeepsPendingLKG(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	seedCatalogPriceGroup(t, fixture, "openai", &providerID, []string{"gpt"})
	oldValue := int64(1)
	if err := fixture.db.Create(&models.ModelPrice{
		PriceScopeKey: "provider:openai", ModelID: "gpt",
		InputPriceNanoUSDPerMillionTokens: &oldValue,
	}).Error; err != nil {
		t.Fatal(err)
	}
	oldTable, err := loadPriceTable(t.Context(), fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	fixture.priceRuntime.Publish(oldTable)
	oldSnapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": catalogProviderFixture("openai", "Old", "gpt", 1),
	}}
	fixture.catalogRuntime.Publish(oldSnapshot)
	if err := fixture.db.Exec(`
		CREATE TRIGGER reject_catalog_price_update
		BEFORE UPDATE ON model_prices
		BEGIN
			SELECT RAISE(ABORT, 'forced catalog reconciliation failure');
		END
	`).Error; err != nil {
		t.Fatal(err)
	}

	result := catalogResultFixture(4000, "new-etag", map[string]catalog.Provider{
		"openai": catalogProviderFixture("openai", "New", "gpt", 9),
	})
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		return result, nil
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{}, false)
	var stored atomic.Bool
	coordinator.storeCache = func(string, catalog.SyncResult) error {
		stored.Store(true)
		return nil
	}

	if _, err := coordinator.Sync(t.Context(), CatalogSyncManual); err == nil {
		t.Fatal("Sync() error = nil, want reconciliation failure")
	}
	if !stored.Load() {
		t.Fatal("validated catalog was not durably stored before reconciliation")
	}
	if fixture.priceRuntime.Load() != oldTable {
		t.Fatal("failed reconciliation replaced PriceRuntime")
	}
	if !reflect.DeepEqual(fixture.catalogRuntime.Load(), oldSnapshot) {
		t.Fatal("failed reconciliation replaced CatalogRuntime")
	}
	if coordinator.PendingGeneration() == nil {
		t.Fatal("failed reconciliation did not retain pending durable generation for retry")
	}
}

func catalogResultFixture(
	checkedAt int64,
	etag string,
	providers map[string]catalog.Provider,
) catalog.SyncResult {
	if providers == nil {
		providers = map[string]catalog.Provider{}
	}
	rawProviders := make(map[string]any, len(providers))
	for providerID, provider := range providers {
		rawModels := make(map[string]any, len(provider.Models))
		for modelID, model := range provider.Models {
			entry := map[string]any{"id": model.ID, "name": model.Name, "cost": nil}
			if model.Cost != nil {
				cost := map[string]any{"input": nil, "output": nil, "cache_read": nil, "cache_write": nil, "tiers": nil}
				if model.Cost.Prices.Input.Set {
					cost["input"] = float64(model.Cost.Prices.Input.NanoUSDPerMillion) / 1_000_000_000
				}
				if model.Cost.Prices.Output.Set {
					cost["output"] = float64(model.Cost.Prices.Output.NanoUSDPerMillion) / 1_000_000_000
				}
				entry["cost"] = cost
			}
			rawModels[modelID] = entry
		}
		rawProviders[providerID] = map[string]any{
			"id": provider.ID, "name": provider.Name,
			"api": provider.APIURL, "npm": provider.NPM, "models": rawModels,
		}
	}
	raw, err := json.Marshal(rawProviders)
	if err != nil {
		panic(err)
	}
	snapshot, err := catalog.Parse(bytes.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return catalog.SyncResult{
		Metadata: catalog.Metadata{
			ETag: etag, CheckedAtMillis: checkedAt, SuccessfulFetchAtMillis: checkedAt,
		},
		RawJSON:  raw,
		Snapshot: snapshot,
	}
}

func catalogProviderFixture(id, name, modelID string, input pricing.NanoUSD) catalog.Provider {
	provider := catalog.Provider{ID: id, Name: name, APIURL: "https://example.com/v1", Models: map[string]catalog.Model{}}
	if modelID != "" {
		provider.Models[modelID] = catalog.Model{
			ID: modelID, Name: modelID,
			Cost: &catalog.ModelCost{Prices: pricing.Prices{
				Input: pricing.Price{Set: true, NanoUSDPerMillion: input},
			}},
		}
	}
	return provider
}

func seedCatalogPriceGroup(
	t *testing.T,
	fixture serviceFixture,
	name string,
	providerID *string,
	modelIDs []string,
) {
	t.Helper()
	groupModels := make([]GroupModel, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		groupModels = append(groupModels, GroupModel{ID: modelID})
	}
	encoded, err := json.Marshal(groupModels)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.Group{
		Name: name, ProviderID: providerID, UpstreamURL: "https://" + name + ".example.com/v1",
		Protocols: models.JSON(`["openai-completions"]`), Models: models.JSON(encoded), Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func assertCatalogPriceRow(
	t *testing.T,
	fixture serviceFixture,
	scopeKey string,
	modelID string,
	assert func(models.ModelPrice),
) {
	t.Helper()
	var row models.ModelPrice
	if err := fixture.db.Where("price_scope_key = ? AND model_id = ?", scopeKey, modelID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	assert(row)
}

func assertCatalogPriceMissing(t *testing.T, fixture serviceFixture, scopeKey, modelID string) {
	t.Helper()
	var count int64
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("price_scope_key = ? AND model_id = ?", scopeKey, modelID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("price %s/%s count = %d, want 0", scopeKey, modelID, count)
	}
}

func waitForCatalogJoiners(t *testing.T, coordinator *CatalogSyncCoordinator, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for coordinator.joinedWaiterCount() < want {
		if time.Now().After(deadline) {
			t.Fatalf("joined waiters = %d, want at least %d", coordinator.joinedWaiterCount(), want)
		}
	}
}

func waitForCatalogIdle(t *testing.T, coordinator *CatalogSyncCoordinator) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		coordinator.mu.Lock()
		idle := coordinator.inFlight == nil
		coordinator.mu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("catalog coordinator did not become idle")
		}
	}
}

type fakeCatalogTimer struct {
	interval time.Duration
	ticks    chan time.Time
	stopped  atomic.Bool
}

func newFakeCatalogTimer(interval time.Duration) *fakeCatalogTimer {
	return &fakeCatalogTimer{interval: interval, ticks: make(chan time.Time, 1)}
}

func (timer *fakeCatalogTimer) C() <-chan time.Time { return timer.ticks }
func (timer *fakeCatalogTimer) Stop()               { timer.stopped.Store(true) }

func awaitCatalogCall(t *testing.T, calls <-chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("catalog network call did not occur")
	}
}

func awaitCatalogTimer(t *testing.T, timers <-chan *fakeCatalogTimer) *fakeCatalogTimer {
	t.Helper()
	select {
	case timer := <-timers:
		return timer
	case <-time.After(time.Second):
		t.Fatal("catalog timer was not created")
		return nil
	}
}

func waitForCatalogShutdown(t *testing.T, coordinator *CatalogSyncCoordinator) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		coordinator.mu.Lock()
		shuttingDown := coordinator.shuttingDown
		coordinator.mu.Unlock()
		if shuttingDown {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("catalog coordinator did not enter shutdown")
		}
	}
}
