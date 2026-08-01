package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestGroupCollectionStatusReflectsRouteCapabilityAndHealth(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.FixedZone("test", 8*60*60))
	fixture.service.now = func() time.Time { return now }

	zero := 0
	available := createGroupCollectionGroup(t, fixture, "available", true, nil)
	zeroModelCompletions := createGroupCollectionGroup(t, fixture, "zero-model-completions", true, nil)
	zeroModelResponses := createGroupCollectionGroup(t, fixture, "zero-model-responses", true, nil)
	mixedZeroModels := createGroupCollectionGroup(t, fixture, "mixed-zero-models", true, nil)
	responsesWithoutHealthyKey := createGroupCollectionGroup(t, fixture, "responses-without-healthy-key", true, nil)
	zeroKeys := createGroupCollectionGroup(t, fixture, "zero-keys", true, nil)
	cooldown := createGroupCollectionGroup(t, fixture, "cooldown", true, nil)
	blacklisted := createGroupCollectionGroup(t, fixture, "blacklisted", true, nil)
	disabled := createGroupCollectionGroup(t, fixture, "disabled", false, nil)
	groupWeightZero := createGroupCollectionGroup(t, fixture, "group-weight-zero", true, &zero)
	keyWeightZero := createGroupCollectionGroup(t, fixture, "key-weight-zero", true, nil)
	setGroupCollectionRoute(t, fixture, zeroModelCompletions, `["openai-completions"]`, `[]`)
	setGroupCollectionRoute(t, fixture, zeroModelResponses, `["openai-responses"]`, `[]`)
	setGroupCollectionRoute(
		t,
		fixture,
		mixedZeroModels,
		`["openai-completions","openai-responses"]`,
		`[]`,
	)
	setGroupCollectionRoute(t, fixture, responsesWithoutHealthyKey, `["openai-responses"]`, `[]`)
	setGroupCollectionRoute(t, fixture, disabled, `["openai-responses"]`, `[]`)
	setGroupCollectionRoute(t, fixture, groupWeightZero, `["openai-responses"]`, `[]`)

	entries := []state.KeyEntry{
		createGroupCollectionKey(t, fixture, available.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, zeroModelCompletions.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, zeroModelResponses.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, mixedZeroModels.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, cooldown.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, blacklisted.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, disabled.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, groupWeightZero.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, keyWeightZero.ID, models.UpstreamKeyStatusActive, &zero),
	}
	entries[4].CooldownUntil = now.Add(time.Minute)
	entries[5].Blacklisted = true
	publishGroupCollectionRuntime(t, fixture, entries)

	observedAtMS, records, err := fixture.service.captureGroupCollectionRecords(context.Background())
	if err != nil {
		t.Fatalf("captureGroupCollectionRecords() error = %v", err)
	}
	if observedAtMS != now.UTC().UnixMilli() {
		t.Fatalf("observedAtMS = %d, want %d", observedAtMS, now.UTC().UnixMilli())
	}
	if len(records) != 11 {
		t.Fatalf("record count = %d, want 11", len(records))
	}

	want := map[uint]GroupCollectionStatus{
		available.ID:                  GroupCollectionStatusAvailable,
		zeroModelCompletions.ID:       GroupCollectionStatusUnavailable,
		zeroModelResponses.ID:         GroupCollectionStatusAvailable,
		mixedZeroModels.ID:            GroupCollectionStatusAvailable,
		responsesWithoutHealthyKey.ID: GroupCollectionStatusUnavailable,
		zeroKeys.ID:                   GroupCollectionStatusUnavailable,
		cooldown.ID:                   GroupCollectionStatusUnavailable,
		blacklisted.ID:                GroupCollectionStatusUnavailable,
		disabled.ID:                   GroupCollectionStatusDisabled,
		groupWeightZero.ID:            GroupCollectionStatusDisabled,
		keyWeightZero.ID:              GroupCollectionStatusUnavailable,
	}
	for _, record := range records {
		if got := record.Status; got != want[record.ID] {
			t.Errorf("group %d status = %q, want %q", record.ID, got, want[record.ID])
		}
		if record.ID == groupWeightZero.ID {
			if got, want := record.KeyCounts, (GroupCollectionKeyCounts{
				Total: 1, Disabled: 1,
			}); got != want {
				t.Errorf("group weight zero key counts = %#v, want %#v", got, want)
			}
		}
		if record.ID == zeroModelCompletions.ID {
			if got, want := record.KeyCounts, (GroupCollectionKeyCounts{
				Total: 1, Available: 1,
			}); got != want {
				t.Errorf("zero-model Completions key counts = %#v, want %#v", got, want)
			}
		}
		assertGroupCollectionKeyCountInvariant(t, record)
	}
}

func TestCaptureGroupCollectionRecordsKeyBuckets(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.July, 31, 4, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }

	group := createGroupCollectionGroup(t, fixture, "all-buckets", true, nil)
	disabledGroup := createGroupCollectionGroup(t, fixture, "disabled-all-keys", false, nil)
	zero := 0
	entries := []state.KeyEntry{
		createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusDisabled, nil),
		createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusActive, &zero),
		createGroupCollectionKey(t, fixture, disabledGroup.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, disabledGroup.ID, models.UpstreamKeyStatusActive, nil),
	}
	entries[1].CooldownUntil = now.Add(time.Minute)
	entries[2].Blacklisted = true
	entries[5].CooldownUntil = now.Add(time.Minute)
	entries[6].Blacklisted = true
	publishGroupCollectionRuntime(t, fixture, entries)

	_, records, err := fixture.service.captureGroupCollectionRecords(context.Background())
	if err != nil {
		t.Fatalf("captureGroupCollectionRecords() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}
	if got, want := records[0].KeyCounts, (GroupCollectionKeyCounts{
		Total: 5, Available: 1, Cooldown: 1, Blacklisted: 1, Disabled: 2,
	}); got != want {
		t.Fatalf("all-buckets key counts = %#v, want %#v", got, want)
	}
	if got, want := records[1].KeyCounts, (GroupCollectionKeyCounts{
		Total: 2, Disabled: 2,
	}); got != want {
		t.Fatalf("disabled group key counts = %#v, want %#v", got, want)
	}
	for _, record := range records {
		assertGroupCollectionKeyCountInvariant(t, record)
	}
}

func TestCaptureGroupCollectionRecordsMapsOwnedMetadata(t *testing.T) {
	fixture := newServiceFixture(t)
	group := createGroupCollectionGroup(t, fixture, "metadata", true, nil)
	group.Protocols = models.JSON(`["openai-completions","anthropic"]`)
	group.Models = models.JSON(`[{"id":"one"},{"id":"two","alias":"public-two"}]`)
	if err := fixture.db.Model(group).Updates(map[string]any{
		"protocols": group.Protocols,
		"models":    group.Models,
	}).Error; err != nil {
		t.Fatalf("update group metadata: %v", err)
	}
	entry := createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusActive, nil)
	publishGroupCollectionRuntime(t, fixture, []state.KeyEntry{entry})

	_, records, err := fixture.service.captureGroupCollectionRecords(t.Context())
	if err != nil {
		t.Fatalf("captureGroupCollectionRecords() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %#v, want one", records)
	}
	record := records[0]
	if record.ID != group.ID || record.Name != "metadata" ||
		record.UpstreamURL != group.UpstreamURL || record.CreatedAtMS != group.CreatedAtMS ||
		record.ModelCount != 2 ||
		!reflect.DeepEqual(record.Protocols, []protocol.Protocol{
			protocol.OpenAICompletions,
			protocol.Anthropic,
		}) {
		t.Fatalf("record metadata = %#v, want persisted metadata", record)
	}
}

func TestCaptureGroupCollectionRecordsFailsClosedOnInconsistentObservations(t *testing.T) {
	tests := []struct {
		name              string
		mutate            func(*state.ConfigSnapshot, *[]state.KeyRuntimeView, *groupCollectionRows)
		wantErrorContains string
	}{
		{
			name: "persisted group zero id",
			mutate: func(_ *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, rows *groupCollectionRows) {
				rows.groups[0].ID = 0
			},
		},
		{
			name: "persisted group duplicate id",
			mutate: func(_ *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, rows *groupCollectionRows) {
				rows.groups = append(rows.groups, rows.groups[0])
			},
		},
		{
			name: "catalog group missing at equal cardinality",
			mutate: func(snapshot *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				delete(snapshot.GroupCatalog, 1)
				snapshot.GroupCatalog[2] = state.GroupCatalogView{ID: 2, Name: "replacement", Enabled: true}
			},
			wantErrorContains: "missing from runtime catalog",
		},
		{
			name: "catalog group extra",
			mutate: func(snapshot *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				snapshot.GroupCatalog[2] = state.GroupCatalogView{ID: 2, Name: "extra", Enabled: true}
			},
		},
		{
			name: "catalog group zero id",
			mutate: func(snapshot *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				view := snapshot.GroupCatalog[1]
				view.ID = 0
				snapshot.GroupCatalog[1] = view
			},
			wantErrorContains: "zero id",
		},
		{
			name: "catalog id mismatch",
			mutate: func(snapshot *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				view := snapshot.GroupCatalog[1]
				view.ID = 2
				snapshot.GroupCatalog[1] = view
			},
		},
		{
			name: "catalog name mismatch",
			mutate: func(snapshot *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				view := snapshot.GroupCatalog[1]
				view.Name = "other"
				snapshot.GroupCatalog[1] = view
			},
		},
		{
			name: "catalog enabled mismatch",
			mutate: func(snapshot *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				view := snapshot.GroupCatalog[1]
				view.Enabled = false
				snapshot.GroupCatalog[1] = view
			},
		},
		{
			name: "catalog weight mismatch",
			mutate: func(snapshot *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				other := 26
				view := snapshot.GroupCatalog[1]
				view.WeightManual = &other
				snapshot.GroupCatalog[1] = view
			},
		},
		{
			name: "persisted key zero id",
			mutate: func(_ *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, rows *groupCollectionRows) {
				rows.keys[0].ID = 0
			},
		},
		{
			name: "persisted key duplicate id",
			mutate: func(_ *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, rows *groupCollectionRows) {
				rows.keys = append(rows.keys, rows.keys[0])
			},
		},
		{
			name: "runtime key zero id",
			mutate: func(_ *state.ConfigSnapshot, runtimeKeys *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				(*runtimeKeys)[0].ID = 0
			},
		},
		{
			name: "runtime key duplicate id",
			mutate: func(_ *state.ConfigSnapshot, runtimeKeys *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				*runtimeKeys = append(*runtimeKeys, (*runtimeKeys)[0])
			},
		},
		{
			name: "runtime key missing at equal cardinality",
			mutate: func(_ *state.ConfigSnapshot, runtimeKeys *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				*runtimeKeys = []state.KeyRuntimeView{{
					ID: 11, GroupID: 1, Status: state.KeyStatusActive,
				}}
			},
			wantErrorContains: "missing from runtime registry",
		},
		{
			name: "runtime key extra",
			mutate: func(_ *state.ConfigSnapshot, runtimeKeys *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				*runtimeKeys = append(*runtimeKeys, state.KeyRuntimeView{
					ID: 11, GroupID: 1, Status: state.KeyStatusActive,
				})
			},
		},
		{
			name: "persisted key references missing group",
			mutate: func(_ *state.ConfigSnapshot, runtimeKeys *[]state.KeyRuntimeView, rows *groupCollectionRows) {
				rows.keys[0].GroupID = 2
				(*runtimeKeys)[0].GroupID = 2
			},
			wantErrorContains: "references missing group",
		},
		{
			name: "runtime group mismatch",
			mutate: func(_ *state.ConfigSnapshot, runtimeKeys *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				(*runtimeKeys)[0].GroupID = 2
			},
		},
		{
			name: "runtime status mismatch",
			mutate: func(_ *state.ConfigSnapshot, runtimeKeys *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				(*runtimeKeys)[0].Status = state.KeyStatusDisabled
			},
		},
		{
			name: "runtime weight mismatch",
			mutate: func(_ *state.ConfigSnapshot, runtimeKeys *[]state.KeyRuntimeView, _ *groupCollectionRows) {
				other := 51
				(*runtimeKeys)[0].WeightManual = &other
			},
		},
		{
			name: "invalid persisted key status",
			mutate: func(_ *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, rows *groupCollectionRows) {
				rows.keys[0].Status = models.UpstreamKeyStatus("broken")
			},
		},
		{
			name: "invalid protocols json",
			mutate: func(_ *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, rows *groupCollectionRows) {
				rows.groups[0].Protocols = models.JSON(`{"not":"an array"}`)
			},
		},
		{
			name: "invalid protocol value",
			mutate: func(_ *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, rows *groupCollectionRows) {
				rows.groups[0].Protocols = models.JSON(`["legacy-openai"]`)
			},
		},
		{
			name: "invalid models json",
			mutate: func(_ *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, rows *groupCollectionRows) {
				rows.groups[0].Models = models.JSON(`{"not":"an array"}`)
			},
		},
		{
			name: "invalid model value",
			mutate: func(_ *state.ConfigSnapshot, _ *[]state.KeyRuntimeView, rows *groupCollectionRows) {
				rows.groups[0].Models = models.JSON(`[{"id":"   "}]`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, runtimeKeys, rows := validGroupCollectionObservation()
			test.mutate(snapshot, &runtimeKeys, &rows)
			_, err := mapGroupCollectionRecords(snapshot, runtimeKeys, rows, time.Unix(1, 0))
			if !errors.Is(err, app_errors.ErrInternalServer) {
				t.Fatalf("mapGroupCollectionRecords() error = %v, want ErrInternalServer", err)
			}
			if test.wantErrorContains != "" && !strings.Contains(err.Error(), test.wantErrorContains) {
				t.Fatalf(
					"mapGroupCollectionRecords() error = %v, want diagnostic containing %q",
					err,
					test.wantErrorContains,
				)
			}
		})
	}
}

func TestCaptureGroupCollectionRecordsRejectsNilSnapshotWithoutPanic(t *testing.T) {
	_, runtimeKeys, rows := validGroupCollectionObservation()
	var panicValue any
	var err error
	func() {
		defer func() { panicValue = recover() }()
		_, err = mapGroupCollectionRecords(nil, runtimeKeys, rows, time.Unix(1, 0))
	}()
	if panicValue != nil {
		t.Fatalf("mapGroupCollectionRecords() panic = %v", panicValue)
	}
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("mapGroupCollectionRecords() error = %v, want ErrInternalServer", err)
	}
}

func TestCaptureGroupCollectionRecordsWaitsForControlWrite(t *testing.T) {
	fixture := newServiceFixture(t)
	group := createGroupCollectionGroup(t, fixture, "wait-for-write", true, nil)
	entry := createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusActive, nil)
	publishGroupCollectionRuntime(t, fixture, []state.KeyEntry{entry})

	fixture.service.writeMu.Lock()
	locked := true
	t.Cleanup(func() {
		if locked {
			fixture.service.writeMu.Unlock()
		}
	})
	done := make(chan error, 1)
	go func() {
		_, _, err := fixture.service.captureGroupCollectionRecords(t.Context())
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("capture returned during control write: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	fixture.service.writeMu.Unlock()
	locked = false
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("capture after control write error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("capture did not finish after control write")
	}
}

func TestCaptureGroupCollectionRecordsUsesSingleReadSnapshot(t *testing.T) {
	fixture, dsn := newFileServiceFixture(t)
	group := createGroupCollectionGroup(t, fixture, "collection-snapshot-old", true, nil)
	entry := createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusActive, nil)
	publishGroupCollectionRuntime(t, fixture, []state.KeyEntry{entry})

	writer, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("storage.Open(writer) error = %v", err)
	}
	writerSQL, err := writer.DB()
	if err != nil {
		t.Fatalf("writer.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := writerSQL.Close(); err != nil {
			t.Errorf("close writer database: %v", err)
		}
	})

	firstSelect := make(chan struct{})
	releaseReader := make(chan struct{})
	var barrierOnce sync.Once
	const callbackName = "test:group_collection_single_read_snapshot"
	if err := fixture.db.Callback().Query().After("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Table != "groups" {
				return
			}
			barrierOnce.Do(func() {
				close(firstSelect)
				<-releaseReader
			})
		},
	); err != nil {
		t.Fatalf("register collection query barrier: %v", err)
	}

	type captureResult struct {
		records []groupCollectionRecord
		err     error
	}
	done := make(chan captureResult, 1)
	go func() {
		_, records, err := fixture.service.captureGroupCollectionRecords(t.Context())
		done <- captureResult{records: records, err: err}
	}()
	select {
	case <-firstSelect:
	case <-time.After(time.Second):
		t.Fatal("capture did not pause after Group SELECT")
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.Group{}).Where("id = ?", group.ID).
				Update("name", "collection-snapshot-new").Error; err != nil {
				return err
			}
			return tx.Create(&models.UpstreamKey{
				GroupID: group.ID, KeyValue: "cipher-new", KeyHash: "hash-new",
				Status: models.UpstreamKeyStatusActive,
			}).Error
		})
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("concurrent collection update error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("collection read blocked WAL writer")
	}
	close(releaseReader)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("captureGroupCollectionRecords() error = %v", result.err)
		}
		if len(result.records) != 1 || result.records[0].Name != "collection-snapshot-old" ||
			result.records[0].KeyCounts.Total != 1 {
			t.Fatalf("capture mixed database versions: %#v", result.records)
		}
	case <-time.After(time.Second):
		t.Fatal("capture did not finish after query barrier release")
	}
}

func TestCaptureGroupCollectionRecordsContextCancellationTakesPrecedence(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	fixture.service.db = nil
	_, _, err := fixture.service.captureGroupCollectionRecords(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("captureGroupCollectionRecords() error = %v, want context.Canceled", err)
	}
}

func TestCaptureGroupCollectionRecordsMapsDatabaseErrors(t *testing.T) {
	fixture := newServiceFixture(t)
	if err := fixture.db.Exec("DROP TABLE groups").Error; err != nil {
		t.Fatalf("drop groups table: %v", err)
	}
	_, _, err := fixture.service.captureGroupCollectionRecords(t.Context())
	if !errors.Is(err, app_errors.ErrDatabase) {
		t.Fatalf("captureGroupCollectionRecords() error = %v, want ErrDatabase", err)
	}
}

type groupCollectionEncryptionProbe struct {
	encryption.Service
	decryptCalls int
}

func (probe *groupCollectionEncryptionProbe) Decrypt(ciphertext string) (string, error) {
	probe.decryptCalls++
	return probe.Service.Decrypt(ciphertext)
}

func TestCaptureGroupCollectionRecordsNeverReadsOrDecryptsSecrets(t *testing.T) {
	fixture := newServiceFixture(t)
	group := createGroupCollectionGroup(t, fixture, "no-secrets", true, nil)
	entry := createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusActive, nil)
	publishGroupCollectionRuntime(t, fixture, []state.KeyEntry{entry})
	probe := &groupCollectionEncryptionProbe{Service: fixture.encryption}
	fixture.service.encryption = probe
	fixture.service.registry = nil
	var upstreamKeySelects []string
	const callbackName = "test:group_collection_selected_key_columns"
	if err := fixture.db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Table == "upstream_keys" {
				upstreamKeySelects = append([]string(nil), tx.Statement.Selects...)
			}
		},
	); err != nil {
		t.Fatalf("register key query observer: %v", err)
	}

	_, _, err := fixture.service.captureGroupCollectionRecords(t.Context())
	if err != nil {
		t.Fatalf("captureGroupCollectionRecords() error = %v", err)
	}
	if probe.decryptCalls != 0 {
		t.Fatalf("Decrypt calls = %d, want 0", probe.decryptCalls)
	}
	if want := []string{"id", "group_id", "status", "weight_manual"}; !reflect.DeepEqual(
		upstreamKeySelects,
		want,
	) {
		t.Fatalf("upstream key SELECT columns = %#v, want %#v", upstreamKeySelects, want)
	}
}

func validGroupCollectionObservation() (
	*state.ConfigSnapshot,
	[]state.KeyRuntimeView,
	groupCollectionRows,
) {
	groupWeight := 25
	keyWeight := 50
	return &state.ConfigSnapshot{
			GroupCatalog: map[uint]state.GroupCatalogView{
				1: {ID: 1, Name: "group", Enabled: true, WeightManual: &groupWeight},
			},
		}, []state.KeyRuntimeView{{
			ID: 10, GroupID: 1, Status: state.KeyStatusActive, WeightManual: &keyWeight,
		}}, groupCollectionRows{
			groups: []models.Group{{
				ID: 1, Name: "group", Enabled: true, WeightManual: &groupWeight,
				UpstreamURL: "https://group.example/v1",
				Protocols:   models.JSON(`["openai-completions"]`),
				Models:      models.JSON(`[{"id":"gpt-4o"}]`),
				CreatedAtMS: 123,
			}},
			keys: []models.UpstreamKey{{
				ID: 10, GroupID: 1, Status: models.UpstreamKeyStatusActive,
				WeightManual: &keyWeight,
			}},
		}
}

func createGroupCollectionGroup(
	t *testing.T,
	fixture serviceFixture,
	name string,
	enabled bool,
	weight *int,
) *models.Group {
	t.Helper()
	group := validControlGroup(name)
	group.Enabled = enabled
	group.WeightManual = weight
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatalf("create group %q: %v", name, err)
	}
	if !enabled {
		if err := fixture.db.Model(group).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable group %q: %v", name, err)
		}
		group.Enabled = false
	}
	return group
}

func setGroupCollectionRoute(
	t *testing.T,
	fixture serviceFixture,
	group *models.Group,
	rawProtocols string,
	rawModels string,
) {
	t.Helper()
	group.Protocols = models.JSON(rawProtocols)
	group.Models = models.JSON(rawModels)
	if err := fixture.db.Model(group).Updates(map[string]any{
		"protocols": group.Protocols,
		"models":    group.Models,
	}).Error; err != nil {
		t.Fatalf("update group %q route: %v", group.Name, err)
	}
}

func createGroupCollectionKey(
	t *testing.T,
	fixture serviceFixture,
	groupID uint,
	status models.UpstreamKeyStatus,
	weight *int,
) state.KeyEntry {
	t.Helper()
	row := models.UpstreamKey{
		GroupID:      groupID,
		KeyValue:     "ciphertext-not-observed",
		KeyHash:      "hash-not-observed-" + time.Now().String(),
		Status:       status,
		WeightManual: weight,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatalf("create key for group %d: %v", groupID, err)
	}
	runtimeStatus := state.KeyStatusActive
	if status == models.UpstreamKeyStatusDisabled {
		runtimeStatus = state.KeyStatusDisabled
	}
	return state.KeyEntry{
		ID: row.ID, GroupID: groupID, Status: runtimeStatus,
		WeightManual: weight, EncryptedValue: row.KeyValue,
	}
}

func publishGroupCollectionRuntime(
	t *testing.T,
	fixture serviceFixture,
	entries []state.KeyEntry,
) {
	t.Helper()
	input, err := stateloader.BuildCompileInput(t.Context(), fixture.db)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	if _, err := fixture.manager.Publish(input); err != nil {
		t.Fatalf("manager.Publish() error = %v", err)
	}
	if err := fixture.registry.Replace(entries); err != nil {
		t.Fatalf("registry.Replace() error = %v", err)
	}
}

func assertGroupCollectionKeyCountInvariant(t *testing.T, record groupCollectionRecord) {
	t.Helper()
	counts := record.KeyCounts
	if got := counts.Available + counts.Cooldown + counts.Blacklisted + counts.Disabled; got != counts.Total {
		t.Errorf("group %d bucket sum = %d, want total %d", record.ID, got, counts.Total)
	}
}
