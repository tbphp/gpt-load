package control

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestReadHomeBaseUsesPersistedAndRuntimeSnapshots(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	nowMS := now.UnixMilli()

	enabled := validControlGroup("home-enabled")
	enabled.Models = models.JSON(`[
		{"id":"gpt-4o","alias":"client-primary"},
		{"id":"client-secondary"},
		{"id":"ignored-empty","alias":" "}
	]`)
	enabledTwo := validControlGroup("home-enabled-two")
	enabledTwo.Models = models.JSON(`[
		{"id":"upstream-duplicate","alias":"client-primary"},
		{"id":"client-secondary"},
		{"id":"client-third","alias":"client-third"}
	]`)
	disabled := validControlGroup("home-disabled")
	disabled.Enabled = false
	disabled.Models = models.JSON(`[{"id":"disabled-model"}]`)
	for _, group := range []*models.Group{enabled, enabledTwo, disabled} {
		if err := fixture.db.Create(group).Error; err != nil {
			t.Fatalf("create group %q: %v", group.Name, err)
		}
	}
	if err := fixture.db.Model(disabled).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable group: %v", err)
	}

	credentials := []models.Credential{
		{
			ID: 1, GroupID: enabled.ID, Data: "cipher-1", Fingerprint: "hash-1",
			Status: models.CredentialStatusActive,
		},
		{
			ID: 2, GroupID: enabled.ID, Data: "cipher-2", Fingerprint: "hash-2",
			Status: models.CredentialStatusActive,
		},
		{
			ID: 3, GroupID: enabledTwo.ID, Data: "cipher-3", Fingerprint: "hash-3",
			Status: models.CredentialStatusDisabled,
		},
		{
			ID: 4, GroupID: disabled.ID, Data: "cipher-4", Fingerprint: "hash-4",
			Status: models.CredentialStatusActive,
		},
	}
	if err := fixture.db.Create(&credentials).Error; err != nil {
		t.Fatalf("create credentials: %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{
		{
			ID: 1, GroupID: enabled.ID, Status: state.CredentialStatusActive,
			Version:            groupCollectionCredentialVersion(credentials[0].SecretVersion),
			IdentityGeneration: groupCollectionCredentialIdentity(credentials[0].IdentityFingerprint, *enabled), Fingerprint: credentials[0].Fingerprint,
			EncryptedValue: "cipher-1",
		},
		{
			ID: 2, GroupID: enabled.ID, Status: state.CredentialStatusActive,
			Version:            groupCollectionCredentialVersion(credentials[1].SecretVersion),
			IdentityGeneration: groupCollectionCredentialIdentity(credentials[1].IdentityFingerprint, *enabled), Fingerprint: credentials[1].Fingerprint,
			CooldownUntil: now.Add(time.Hour), EncryptedValue: "cipher-2",
		},
		{
			ID: 3, GroupID: enabledTwo.ID, Status: state.CredentialStatusDisabled,
			Version:            groupCollectionCredentialVersion(credentials[2].SecretVersion),
			IdentityGeneration: groupCollectionCredentialIdentity(credentials[2].IdentityFingerprint, *enabledTwo), Fingerprint: credentials[2].Fingerprint,
			EncryptedValue: "cipher-3",
		},
		{
			ID: 4, GroupID: disabled.ID, Status: state.CredentialStatusActive,
			Version:            groupCollectionCredentialVersion(credentials[3].SecretVersion),
			IdentityGeneration: groupCollectionCredentialIdentity(credentials[3].IdentityFingerprint, *disabled), Fingerprint: credentials[3].Fingerprint,
			EncryptedValue: "cipher-4",
		},
	}); err != nil {
		t.Fatalf("registry.ReplaceCredentials() error = %v", err)
	}

	accessKeys := []models.AccessKey{
		{
			ID: 9, Name: "all protocols", KeyValue: "cipher-access-9",
			KeyHash: "access-hash-9", KeySuffix: "c0de",
			Status: string(state.AccessKeyStatusActive),
			Filters: models.JSON(
				`{"groups":[],"protocols":[],"models":[]}`,
			),
		},
		{
			ID: 3, Name: "filtered", KeyValue: "cipher-access-3",
			KeyHash: "access-hash-3", KeySuffix: "88ab",
			Status: string(state.AccessKeyStatusActive),
			Filters: models.JSON(
				`{"groups":[],"protocols":["gemini","openai-completions","gemini"],"models":[]}`,
			),
		},
		{
			ID: 5, Name: "disabled", KeyValue: "cipher-access-5",
			KeyHash: "access-hash-5", KeySuffix: "dead",
			Status: string(state.AccessKeyStatusDisabled),
			Filters: models.JSON(
				`{"groups":[],"protocols":[],"models":[]}`,
			),
		},
	}
	if err := fixture.db.Create(&accessKeys).Error; err != nil {
		t.Fatalf("create access keys: %v", err)
	}
	input, err := stateloader.BuildCompileInput(context.Background(), fixture.db, fixture.channelRegistry)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	if _, err := fixture.manager.Publish(input); err != nil {
		t.Fatalf("manager.Publish() error = %v", err)
	}

	registrySnapshotCalls := 0
	fixture.service.registrySnapshot = func() []state.CredentialRuntimeView {
		registrySnapshotCalls++
		return fixture.registry.Snapshot()
	}

	got, err := fixture.service.ReadHomeBase(context.Background(), nowMS)
	if err != nil {
		t.Fatalf("ReadHomeBase() error = %v", err)
	}
	want := HomeBase{
		Inventory: HomeInventory{
			GroupCount:               3,
			CredentialCount:          4,
			AvailableCredentialCount: 1,
			ModelCount:               4,
		},
		AccessKeys: []HomeAccessKey{
			{
				ID: 3, Name: "filtered",
				MaskedKey: "sk-gl-****88ab",
				Protocols: []protocol.Protocol{
					protocol.OpenAICompletions,
					protocol.Gemini,
				},
			},
			{
				ID: 9, Name: "all protocols",
				MaskedKey: "sk-gl-****c0de",
				Protocols: protocol.DataPlaneProtocols(),
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadHomeBase() = %#v, want %#v", got, want)
	}
	if registrySnapshotCalls != 1 {
		t.Fatalf("registry Snapshot calls = %d, want 1", registrySnapshotCalls)
	}
}

func TestHomeInventoryWireUsesCredentialTerms(t *testing.T) {
	encoded, err := json.Marshal(HomeInventory{
		GroupCount: 1, CredentialCount: 2, AvailableCredentialCount: 1, ModelCount: 3,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(encoded)
	for _, field := range []string{`"credential_count":2`, `"available_credential_count":1`} {
		if !strings.Contains(text, field) {
			t.Fatalf("home inventory missing %s: %s", field, text)
		}
	}
	for _, legacy := range []string{"upstream_key_count", "available_upstream_key_count"} {
		if strings.Contains(text, legacy) {
			t.Fatalf("home inventory exposes %s: %s", legacy, text)
		}
	}
}

func TestReadAccessKeyHomeBaseScopesInventoryToRoutableModels(t *testing.T) {
	fixture := newServiceFixture(t)
	allowed := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "allowed", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[
			{"id":"upstream-allowed","alias":"client-allowed"},
			{"id":"upstream-hidden","alias":"client-hidden"}
		]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "private", ChannelID: string(channel.Gemini), Params: models.JSON(`{}`),
		Models:    models.JSON(`[{"id":"private-upstream","alias":"private-client"}]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	})
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "scoped home",
		Filters: &AccessKeyFilters{
			Groups:    []uint{allowed.ID},
			Protocols: []protocol.Protocol{protocol.OpenAICompletions},
			Models:    []string{"client-allowed"},
		},
	})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}

	result, err := fixture.service.ReadAccessKeyHomeBase(
		t.Context(),
		time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC).UnixMilli(),
		created.ID,
	)
	if err != nil {
		t.Fatalf("ReadAccessKeyHomeBase() error = %v", err)
	}
	if result.Inventory != (HomeInventory{GroupCount: 1, ModelCount: 1}) ||
		len(result.AccessKeys) != 1 || result.AccessKeys[0].ID != created.ID ||
		result.CurrentAccessKey == nil || result.CurrentAccessKey.ID != created.ID {
		t.Fatalf("ReadAccessKeyHomeBase() = %#v", result)
	}
}

func TestReadHomeBaseFailsClosed(t *testing.T) {
	t.Run("invalid time", func(t *testing.T) {
		fixture := newServiceFixture(t)
		for _, nowMS := range []int64{-1, maxSafeInteger + 1} {
			if _, err := fixture.service.ReadHomeBase(
				context.Background(),
				nowMS,
			); err == nil {
				t.Fatalf("ReadHomeBase(nowMS=%d) error = nil", nowMS)
			}
		}
	})

	t.Run("invalid stored filter", func(t *testing.T) {
		fixture := newServiceFixture(t)
		row := models.AccessKey{
			Name: "corrupt", KeyValue: "cipher", KeyHash: "corrupt-hash",
			KeySuffix: "cafe", Status: string(state.AccessKeyStatusActive),
			Filters: models.JSON(`{"unknown":[]}`),
		}
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatalf("create access key: %v", err)
		}
		if _, err := fixture.service.ReadHomeBase(
			context.Background(),
			1,
		); err == nil {
			t.Fatal("ReadHomeBase() error = nil")
		}
	})

	t.Run("query failure", func(t *testing.T) {
		fixture := newServiceFixture(t)
		fixture.service.db = nil
		if _, err := fixture.service.ReadHomeBase(
			context.Background(),
			1,
		); err == nil {
			t.Fatal("ReadHomeBase() error = nil")
		}
	})

	t.Run("unsafe inventory", func(t *testing.T) {
		if err := validateHomeInventory(HomeInventory{
			GroupCount: maxSafeInteger + 1,
		}); err == nil {
			t.Fatal("validateHomeInventory() error = nil")
		}
	})
}

func TestReadHomeBaseKeepsDatabaseRowsInOneReadSnapshot(t *testing.T) {
	fixture, dsn := newFileServiceFixture(t)
	group := validControlGroup("home-snapshot")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	input, err := stateloader.BuildCompileInput(t.Context(), fixture.db)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	if _, err := fixture.manager.Publish(input); err != nil {
		t.Fatalf("manager.Publish() error = %v", err)
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

	var held atomic.Bool
	firstSelectDone := make(chan struct{})
	releaseRead := make(chan struct{})
	const callbackName = "test:hold-home-after-group-count"
	if err := fixture.db.Callback().Query().
		After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "groups" && held.CompareAndSwap(false, true) {
				close(firstSelectDone)
				<-releaseRead
			}
		}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	t.Cleanup(func() {
		fixture.db.Callback().Query().Remove(callbackName)
	})

	type readResult struct {
		value HomeBase
		err   error
	}
	readDone := make(chan readResult, 1)
	go func() {
		value, err := fixture.service.ReadHomeBase(t.Context(), 1)
		readDone <- readResult{value: value, err: err}
	}()
	select {
	case <-firstSelectDone:
	case <-time.After(time.Second):
		t.Fatal("ReadHomeBase did not complete the first SELECT")
	}

	newAccessKey := models.AccessKey{
		Name: "newer", KeyValue: "cipher", KeyHash: "newer-hash",
		KeySuffix: "abcd", Status: string(state.AccessKeyStatusActive),
		Filters: models.JSON(
			`{"groups":[],"protocols":[],"models":[]}`,
		),
	}
	if err := writer.Create(&newAccessKey).Error; err != nil {
		t.Fatalf("WAL writer create access key: %v", err)
	}
	close(releaseRead)

	select {
	case result := <-readDone:
		if result.err != nil {
			t.Fatalf("ReadHomeBase() error = %v", result.err)
		}
		if result.value.Inventory.GroupCount != 1 ||
			len(result.value.AccessKeys) != 0 {
			t.Fatalf(
				"ReadHomeBase() mixed database versions: %#v",
				result.value,
			)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadHomeBase did not finish")
	}
}
