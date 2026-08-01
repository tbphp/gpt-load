package control

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/dialect"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestDiscoveryUsesSingleReadSnapshot(t *testing.T) {
	fixture, dsn := newFileServiceFixture(t)
	group := seedPersistedDiscoveryGroup(t, fixture, true, models.JSON(`{}`))
	group.Name = "discovery-snapshot-old"
	group.UpstreamURL = "https://discovery-old.example/v1"
	group.Protocols = models.JSON(`["openai-completions"]`)
	if err := fixture.db.Save(&group).Error; err != nil {
		t.Fatalf("seed old Group version: %v", err)
	}
	seedPersistedDiscoveryKey(
		t,
		fixture,
		group.ID,
		1,
		"discovery-key-old",
		models.UpstreamKeyStatusActive,
	)
	if err := fixture.db.Create(&models.SystemSetting{
		Key: "header_rules", Value: `{"set":{"X-Version":"old"}}`,
	}).Error; err != nil {
		t.Fatalf("seed old system settings: %v", err)
	}

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
	newCiphertext, err := fixture.encryption.Encrypt("discovery-key-new")
	if err != nil {
		t.Fatalf("encrypt new key version: %v", err)
	}

	firstSelect := make(chan struct{})
	releaseReader := make(chan struct{})
	var barrierOnce sync.Once
	const callbackName = "test:discovery_single_read_snapshot"
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
		t.Fatalf("register discovery query barrier: %v", err)
	}

	type observedTarget struct {
		baseURL string
		apiKey  string
		version string
	}
	observed := make(chan observedTarget, 1)
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAICompletions,
		listFn: func(
			_ context.Context,
			baseURL, apiKey string,
			rules state.HeaderRules,
		) ([]string, error) {
			observed <- observedTarget{
				baseURL: baseURL,
				apiKey:  apiKey,
				version: rules.Set["X-Version"],
			}
			return []string{"model"}, nil
		},
	})
	discoveryDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.DiscoverGroupModels(t.Context(), group.ID)
		discoveryDone <- err
	}()
	select {
	case <-firstSelect:
	case <-time.After(time.Second):
		t.Fatal("discovery did not pause after its first SELECT")
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.Group{}).Where("id = ?", group.ID).
				Updates(map[string]any{
					"name":         "discovery-snapshot-new",
					"upstream_url": "https://discovery-new.example/v1",
				}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.UpstreamKey{}).Where("group_id = ?", group.ID).
				Update("key_value", newCiphertext).Error; err != nil {
				return err
			}
			return tx.Model(&models.SystemSetting{}).Where("key = ?", "header_rules").
				Update("value", `{"set":{"X-Version":"new"}}`).Error
		})
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("concurrent version update error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery read blocked WAL writer")
	}
	close(releaseReader)

	select {
	case err := <-discoveryDone:
		if err != nil {
			t.Fatalf("DiscoverGroupModels() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery did not finish after query barrier release")
	}
	var got observedTarget
	select {
	case got = <-observed:
	default:
		t.Fatal("discovery did not reach provider mapping")
	}
	old := observedTarget{
		baseURL: "https://discovery-old.example/v1",
		apiKey:  "discovery-key-old",
		version: "old",
	}
	newVersion := observedTarget{
		baseURL: "https://discovery-new.example/v1",
		apiKey:  "discovery-key-new",
		version: "new",
	}
	if got != old && got != newVersion {
		t.Fatalf("discovery observed mixed versions: %#v", got)
	}
}

func TestDiscoveryReleasesReadSnapshotBeforeDecrypt(t *testing.T) {
	fixture, _ := newFileServiceFixture(t)
	group := seedPersistedDiscoveryGroup(t, fixture, true, models.JSON(`{}`))
	group.Protocols = models.JSON(`["openai-completions"]`)
	if err := fixture.db.Save(&group).Error; err != nil {
		t.Fatal(err)
	}
	seedPersistedDiscoveryKey(
		t,
		fixture,
		group.ID,
		1,
		"release-snapshot-key",
		models.UpstreamKeyStatusActive,
	)
	decryptStarted := make(chan struct{})
	releaseDecrypt := make(chan struct{})
	fixture.service.encryption = blockingDecryptService{
		Service: fixture.encryption,
		started: decryptStarted,
		release: releaseDecrypt,
	}
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return []string{"model"}, nil
		},
	})

	discoveryDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.DiscoverGroupModels(t.Context(), group.ID)
		discoveryDone <- err
	}()
	select {
	case <-decryptStarted:
	case <-time.After(time.Second):
		t.Fatal("discovery did not reach decrypt mapping")
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- fixture.db.Model(&models.Group{}).
			Where("id = ?", group.ID).
			Update("name", "write-during-decrypt").Error
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("write during decrypt error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("decrypt mapping retained the single pooled read connection")
	}
	close(releaseDecrypt)
	select {
	case err := <-discoveryDone:
		if err != nil {
			t.Fatalf("DiscoverGroupModels() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery did not finish after decrypt release")
	}
}

func TestDiscoverGroupModelsUsesDisabledGroupAndActiveKeysInIDOrder(t *testing.T) {
	fixture := newServiceFixture(t)
	group := seedPersistedDiscoveryGroup(t, fixture, false, models.JSON(
		`{"header_rules":{"set":{"X-Group":"group"},"remove":["X-Remove"]}}`,
	))
	seedPersistedDiscoveryKey(t, fixture, group.ID, 1, "key-1", models.UpstreamKeyStatusActive)
	seedPersistedDiscoveryKey(t, fixture, group.ID, 2, "key-2", models.UpstreamKeyStatusDisabled)
	seedPersistedDiscoveryKey(t, fixture, group.ID, 3, "key-3", models.UpstreamKeyStatusActive)
	if err := fixture.db.Create(&models.SystemSetting{
		Key: "header_rules", Value: `{"set":{"X-System":"system"}}`,
	}).Error; err != nil {
		t.Fatalf("seed system HeaderRules: %v", err)
	}

	var calls []string
	newRecorder := func(value protocol.Protocol) *recordingDiscoveryDialect {
		return &recordingDiscoveryDialect{
			value: value,
			listFn: func(
				_ context.Context,
				baseURL, apiKey string,
				rules state.HeaderRules,
			) ([]string, error) {
				calls = append(calls, string(value)+":"+apiKey)
				if baseURL != group.UpstreamURL {
					t.Fatalf("base URL = %q, want %q", baseURL, group.UpstreamURL)
				}
				wantRules := state.HeaderRules{
					Set: map[string]string{"X-Group": "group"}, Remove: []string{"X-Remove"},
				}
				if !reflect.DeepEqual(rules, wantRules) {
					t.Fatalf("HeaderRules = %#v, want persisted Group override %#v", rules, wantRules)
				}
				if value == protocol.Anthropic && apiKey == "key-1" {
					return []string{"z-model", "a-model"}, nil
				}
				return nil, errors.New("try next candidate")
			},
		}
	}
	fixture.service.dialects = dialect.NewSet(
		newRecorder(protocol.Anthropic),
		newRecorder(protocol.OpenAICompletions),
	)

	result, err := fixture.service.DiscoverGroupModels(t.Context(), group.ID)
	if err != nil {
		t.Fatalf("DiscoverGroupModels() error = %v", err)
	}
	if !reflect.DeepEqual(result.Models, []string{"z-model", "a-model"}) {
		t.Fatalf("models = %#v, want upstream order", result.Models)
	}
	wantCalls := []string{
		"openai-completions:key-1",
		"openai-completions:key-3",
		"anthropic:key-1",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want protocol-outer active-key-ID-inner order %#v", calls, wantCalls)
	}
}

func TestDiscoverGroupModelsReturnsNotFoundAndNoActiveUpstreamKey(t *testing.T) {
	t.Run("missing Group", func(t *testing.T) {
		fixture := newServiceFixture(t)
		_, err := fixture.service.DiscoverGroupModels(t.Context(), 999)
		if !errors.Is(err, app_errors.ErrResourceNotFound) {
			t.Fatalf("DiscoverGroupModels() error = %v, want ErrResourceNotFound", err)
		}
	})

	t.Run("no active upstream key", func(t *testing.T) {
		fixture := newServiceFixture(t)
		group := seedPersistedDiscoveryGroup(t, fixture, true, models.JSON(`{}`))
		seedPersistedDiscoveryKey(t, fixture, group.ID, 1, "disabled", models.UpstreamKeyStatusDisabled)
		_, err := fixture.service.DiscoverGroupModels(t.Context(), group.ID)
		if !errors.Is(err, app_errors.ErrNoActiveUpstreamKey) {
			t.Fatalf("DiscoverGroupModels() error = %v, want ErrNoActiveUpstreamKey", err)
		}
	})
}

func TestDiscoverGroupModelsDecryptsEveryKeyBeforeHTTP(t *testing.T) {
	fixture := newServiceFixture(t)
	group := seedPersistedDiscoveryGroup(t, fixture, true, models.JSON(`{}`))
	seedPersistedDiscoveryKey(t, fixture, group.ID, 1, "key-1", models.UpstreamKeyStatusActive)
	if err := fixture.db.Create(&models.UpstreamKey{
		ID: 3, GroupID: group.ID, KeyValue: "corrupt-second-active-ciphertext",
		KeyHash: "corrupt-hash", Status: models.UpstreamKeyStatusActive,
	}).Error; err != nil {
		t.Fatalf("seed corrupt active key: %v", err)
	}

	calls := 0
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.Anthropic,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			calls++
			return nil, nil
		},
	})
	_, err := fixture.service.DiscoverGroupModels(t.Context(), group.ID)
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("DiscoverGroupModels() error = %v, want sanitized ErrInternalServer", err)
	}
	if calls != 0 {
		t.Fatalf("ListModels calls = %d, want zero before every key decrypts", calls)
	}
	for _, secret := range []string{"key-1", "corrupt-second-active-ciphertext", "corrupt-hash"} {
		if strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("error exposes %q: %v", secret, err)
		}
	}
}

func TestDiscoverGroupModelsDoesNotMutateDatabaseSnapshotOrRegistry(t *testing.T) {
	tests := []struct {
		name    string
		listFn  func(context.Context, context.CancelFunc) ([]string, error)
		wantErr error
	}{
		{
			name: "success",
			listFn: func(context.Context, context.CancelFunc) ([]string, error) {
				return []string{"remote-only"}, nil
			},
		},
		{
			name: "all candidates failed",
			listFn: func(context.Context, context.CancelFunc) ([]string, error) {
				return nil, errors.New("upstream failure")
			},
			wantErr: app_errors.ErrBadGateway,
		},
		{
			name: "parent cancellation",
			listFn: func(_ context.Context, cancel context.CancelFunc) ([]string, error) {
				cancel()
				return nil, context.Canceled
			},
			wantErr: context.Canceled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
				UpstreamURL: "https://state.example.com/v1",
				Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
				Keys:        "state-key",
				Models: optionalGroupModels{
					Set: true, Values: []GroupModel{{ID: "persisted-model"}},
				},
			})
			if err != nil {
				t.Fatalf("seed CreateGroup() error = %v", err)
			}
			before := captureGroupDiscoveryState(t, fixture, created.GroupID)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
				value: protocol.OpenAICompletions,
				listFn: func(ctx context.Context, _, _ string, _ state.HeaderRules) ([]string, error) {
					return test.listFn(ctx, cancel)
				},
			})

			_, err = fixture.service.DiscoverGroupModels(ctx, created.GroupID)
			if test.wantErr == nil && err != nil {
				t.Fatalf("DiscoverGroupModels() error = %v", err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("DiscoverGroupModels() error = %v, want %v", err, test.wantErr)
			}
			after := captureGroupDiscoveryState(t, fixture, created.GroupID)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("discovery mutated persistent/runtime state\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestDiscoverGroupModelsDoesNotAcquireWriteMuOrBlockWrites(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://discovery-lock.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        "key-1",
	})
	if err != nil {
		t.Fatalf("seed CreateGroup() error = %v", err)
	}
	fixture.service.modelDiscoveryTimeout = 3 * time.Second
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return []string{"model"}, nil
		},
	})

	fixture.service.writeMu.Lock()
	lockCheckDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.DiscoverGroupModels(t.Context(), created.GroupID)
		lockCheckDone <- err
	}()
	select {
	case err := <-lockCheckDone:
		if err != nil {
			t.Fatalf("DiscoverGroupModels() error while writeMu locked = %v", err)
		}
	case <-time.After(time.Second):
		fixture.service.writeMu.Unlock()
		t.Fatal("DiscoverGroupModels() waited for writeMu")
	}
	fixture.service.writeMu.Unlock()

	entered := make(chan struct{})
	release := make(chan struct{})
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAICompletions,
		listFn: func(ctx context.Context, _, _ string, _ state.HeaderRules) ([]string, error) {
			close(entered)
			select {
			case <-release:
				return []string{"model"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})
	discoveryDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.DiscoverGroupModels(t.Context(), created.GroupID)
		discoveryDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("discovery did not reach HTTP before timeout")
	}

	groupWriteDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
			UpstreamURL: "https://concurrent-group-write.example.com/v1",
			Protocols:   []protocol.Protocol{protocol.Anthropic},
			Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
			Keys:        "concurrent-group-key",
		})
		groupWriteDone <- err
	}()
	select {
	case err := <-groupWriteDone:
		if err != nil {
			t.Fatalf("concurrent CreateGroup() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("long discovery blocked Group write")
	}

	keyWriteDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.ImportGroupKeys(t.Context(), created.GroupID, GroupKeyImportRequest{
			Keys: "concurrent-imported-key",
		})
		keyWriteDone <- err
	}()
	select {
	case err := <-keyWriteDone:
		if err != nil {
			t.Fatalf("concurrent ImportGroupKeys() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("long discovery blocked key write")
	}

	close(release)
	select {
	case err := <-discoveryDone:
		if err != nil {
			t.Fatalf("DiscoverGroupModels() error after release = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery did not finish after release")
	}
}

type groupDiscoveryState struct {
	rowCounts        [3]int64
	models           string
	config           string
	snapshot         *state.ConfigSnapshot
	snapshotRevision uint64
	registryKeys     []state.KeyMeta
	registryValues   map[uint]string
}

func captureGroupDiscoveryState(t *testing.T, fixture serviceFixture, groupID uint) groupDiscoveryState {
	t.Helper()
	var group models.Group
	if err := fixture.db.First(&group, groupID).Error; err != nil {
		t.Fatalf("load Group state: %v", err)
	}
	var keyRows []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&keyRows).Error; err != nil {
		t.Fatalf("load key state: %v", err)
	}
	registryValues := make(map[uint]string, len(keyRows))
	for _, keyRow := range keyRows {
		if ciphertext, ok := fixture.registry.EncryptedValue(keyRow.ID); ok {
			registryValues[keyRow.ID] = ciphertext
		}
	}
	snapshot := fixture.manager.Current()
	return groupDiscoveryState{
		rowCounts:        discoveryRowCounts(t, fixture.db),
		models:           string(group.Models),
		config:           string(group.Config),
		snapshot:         snapshot,
		snapshotRevision: snapshot.Revision,
		registryKeys:     fixture.registry.CollectCandidates([]uint{groupID}, nil, time.Time{}),
		registryValues:   registryValues,
	}
}

func seedPersistedDiscoveryGroup(
	t *testing.T,
	fixture serviceFixture,
	enabled bool,
	groupConfig models.JSON,
) models.Group {
	t.Helper()
	group := models.Group{
		Name: "persisted-discovery", UpstreamURL: "https://persisted.example.com/v1",
		Protocols: models.JSON(`["anthropic","openai-completions"]`),
		Models:    models.JSON(`[{"id":"persisted-only"}]`), Config: groupConfig, Enabled: enabled,
	}
	if err := fixture.db.Create(&group).Error; err != nil {
		t.Fatalf("seed persisted discovery Group: %v", err)
	}
	return group
}

func seedPersistedDiscoveryKey(
	t *testing.T,
	fixture serviceFixture,
	groupID, keyID uint,
	plaintext string,
	status models.UpstreamKeyStatus,
) {
	t.Helper()
	ciphertext, err := fixture.encryption.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt seeded discovery key: %v", err)
	}
	if err := fixture.db.Create(&models.UpstreamKey{
		ID: keyID, GroupID: groupID, KeyValue: ciphertext,
		KeyHash: fixture.encryption.Hash(plaintext), Status: status,
	}).Error; err != nil {
		t.Fatalf("seed persisted discovery key: %v", err)
	}
}
