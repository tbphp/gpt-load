package control

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

func TestImportGroupKeysAddsAndCountsDuplicatesWithoutPublishing(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-existing")
	beforeSnapshot := fixture.manager.Current()
	var beforeGroup models.Group
	if err := fixture.db.First(&beforeGroup, groupID).Error; err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.ImportGroupKeys(t.Context(), groupID, GroupKeyImportRequest{
		Keys: "sk-existing\nsk-new\nsk-new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.KeysAdded != 1 || result.KeysDuplicated != 2 {
		t.Fatalf("result = %#v", result)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("key-only import published Snapshot")
	}

	var afterGroup models.Group
	if err := fixture.db.First(&afterGroup, groupID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeGroup, afterGroup) {
		t.Fatalf("Group changed:\nbefore=%#v\nafter=%#v", beforeGroup, afterGroup)
	}
	assertImportedKeyState(t, fixture, groupID, 2)
}

func TestImportGroupKeysRepairsMissingRegistryWithoutResettingRuntime(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-existing\nsk-registry-missing")
	var keys []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&keys).Error; err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("seeded keys = %#v", keys)
	}
	if count, ok := fixture.registry.IncrFailure(keys[0].ID); !ok || count != 1 {
		t.Fatalf("seed failure count = %d, %t", count, ok)
	}
	if !fixture.registry.RemoveKey(keys[1].ID) {
		t.Fatalf("remove Registry key %d = false", keys[1].ID)
	}
	beforeSnapshot := fixture.manager.Current()

	result, err := fixture.service.ImportGroupKeys(t.Context(), groupID, GroupKeyImportRequest{
		Keys: "sk-existing\nsk-registry-missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.KeysAdded != 0 || result.KeysDuplicated != 2 {
		t.Fatalf("result = %#v", result)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("Registry repair published Snapshot")
	}
	if count, ok := fixture.registry.IncrFailure(keys[0].ID); !ok || count != 2 {
		t.Fatalf("existing Registry failure count = %d, %t, want 2", count, ok)
	}
	if value, ok := fixture.registry.EncryptedValue(keys[1].ID); !ok || value != keys[1].KeyValue {
		t.Fatalf("repaired Registry value = %q, %t, want %q", value, ok, keys[1].KeyValue)
	}
}

func TestImportGroupKeysReturnsNotFoundWithoutMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-existing")
	var existingKey models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).First(&existingKey).Error; err != nil {
		t.Fatal(err)
	}
	if count, ok := fixture.registry.IncrFailure(existingKey.ID); !ok || count != 1 {
		t.Fatalf("seed failure count = %d, %t", count, ok)
	}
	beforeSnapshot := fixture.manager.Current()
	beforeCandidates := fixture.registry.CollectCandidates([]uint{groupID}, nil)

	_, err := fixture.service.ImportGroupKeys(t.Context(), groupID+1000, GroupKeyImportRequest{Keys: "sk-new"})
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrResourceNotFound.Code {
		t.Fatalf("ImportGroupKeys() error = %#v, want NOT_FOUND", err)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("not-found import published Snapshot")
	}
	if got := fixture.registry.CollectCandidates([]uint{groupID}, nil); !reflect.DeepEqual(got, beforeCandidates) {
		t.Fatalf("not-found import changed Registry: %#v", got)
	}
	if count, ok := fixture.registry.IncrFailure(existingKey.ID); !ok || count != 2 {
		t.Fatalf("Registry failure count = %d, %t, want 2", count, ok)
	}
	assertImportedKeyState(t, fixture, groupID, 1)

	_, err = fixture.service.ImportGroupKeys(t.Context(), 0, GroupKeyImportRequest{Keys: "sk-zero"})
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrValidation.Code {
		t.Fatalf("ImportGroupKeys(group 0) error = %#v, want VALIDATION_FAILED", err)
	}
}

func TestImportGroupKeysRollsBackOnEncryptionFailure(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-existing")
	var existingKey models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).First(&existingKey).Error; err != nil {
		t.Fatal(err)
	}
	if count, ok := fixture.registry.IncrFailure(existingKey.ID); !ok || count != 1 {
		t.Fatalf("seed failure count = %d, %t", count, ok)
	}
	fixture.service.encryption = groupCreateFailingEncryptService{Service: fixture.encryption}
	beforeSnapshot := fixture.manager.Current()
	beforeCandidates := fixture.registry.CollectCandidates([]uint{groupID}, nil)

	_, err := fixture.service.ImportGroupKeys(t.Context(), groupID, GroupKeyImportRequest{Keys: "sk-new"})
	if err == nil {
		t.Fatal("ImportGroupKeys() error = nil, want encryption failure")
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("failed import published Snapshot")
	}
	if got := fixture.registry.CollectCandidates([]uint{groupID}, nil); !reflect.DeepEqual(got, beforeCandidates) {
		t.Fatalf("failed import changed Registry: %#v", got)
	}
	if count, ok := fixture.registry.IncrFailure(existingKey.ID); !ok || count != 2 {
		t.Fatalf("Registry failure count = %d, %t, want 2", count, ok)
	}
	assertImportedKeyState(t, fixture, groupID, 1)
}

func TestImportGroupKeysCleansPoolAfterCommitBusy(t *testing.T) {
	fixture, dsn := newFileServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-existing")
	beforeSnapshot := fixture.manager.Current()
	releaseReader := holdRollbackJournalReadLock(t, fixture.db, dsn)

	_, err := fixture.service.ImportGroupKeys(
		t.Context(), groupID, GroupKeyImportRequest{Keys: "sk-failed-busy"},
	)
	if err == nil {
		t.Fatal("ImportGroupKeys() error = nil, want COMMIT BUSY")
	}
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrDatabase.Code {
		t.Fatalf("ImportGroupKeys() error = %#v, want DATABASE_ERROR", err)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("key-only import changed Snapshot")
	}

	releaseReader()
	assertImportedKeyState(t, fixture, groupID, 1)
	result, err := fixture.service.ImportGroupKeys(
		t.Context(), groupID, GroupKeyImportRequest{Keys: "sk-after-busy"},
	)
	if err != nil || result.KeysAdded != 1 || result.KeysDuplicated != 0 {
		t.Fatalf("ImportGroupKeys() = %#v, %v", result, err)
	}
	assertImportedKeyState(t, fixture, groupID, 2)
}

func TestImportGroupKeysLeavesGroupFieldsUnchanged(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-existing")
	weight := 37
	validationModel := "gpt-field-check"
	if err := fixture.db.Model(&models.Group{}).Where("id = ?", groupID).Updates(map[string]any{
		"convert_enabled":  true,
		"weight_manual":    weight,
		"validation_model": validationModel,
		"config":           models.JSON(`{"max_retries":3}`),
		"enabled":          false,
	}).Error; err != nil {
		t.Fatal(err)
	}
	beforeSnapshot := fixture.manager.Current()
	var beforeGroup models.Group
	if err := fixture.db.First(&beforeGroup, groupID).Error; err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.ImportGroupKeys(t.Context(), groupID, GroupKeyImportRequest{
		Keys: "sk-existing\nsk-new\nsk-new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.KeysAdded != 1 || result.KeysDuplicated != 2 {
		t.Fatalf("result = %#v", result)
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("key-only import published Snapshot")
	}

	var afterGroup models.Group
	if err := fixture.db.First(&afterGroup, groupID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeGroup, afterGroup) {
		t.Fatalf("Group changed:\nbefore=%#v\nafter=%#v", beforeGroup, afterGroup)
	}
}

func TestConcurrentGroupKeyImportsRemainSerialized(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-existing")
	blocking := &groupKeyBlockingEncryptService{
		Service: fixture.encryption,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	fixture.service.encryption = blocking

	results := make(chan GroupKeyImportResult, 2)
	errors := make(chan error, 2)
	startImport := func() {
		result, err := fixture.service.ImportGroupKeys(t.Context(), groupID, GroupKeyImportRequest{Keys: "sk-concurrent"})
		results <- result
		errors <- err
	}
	go startImport()
	select {
	case <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first import did not reach encryption")
	}
	if fixture.service.writeMu.TryLock() {
		fixture.service.writeMu.Unlock()
		t.Fatal("writeMu was not held during key persistence")
	}
	go startImport()
	close(blocking.release)

	added := 0
	duplicated := 0
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent ImportGroupKeys() error = %v", err)
		}
		result := <-results
		added += result.KeysAdded
		duplicated += result.KeysDuplicated
	}
	if added != 1 || duplicated != 1 {
		t.Fatalf("concurrent totals = added %d, duplicated %d", added, duplicated)
	}
	assertImportedKeyState(t, fixture, groupID, 2)
}

type groupKeyBlockingEncryptService struct {
	encryption.Service
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *groupKeyBlockingEncryptService) Encrypt(plaintext string) (string, error) {
	s.once.Do(func() {
		close(s.entered)
		<-s.release
	})
	return s.Service.Encrypt(plaintext)
}

func createGroupForKeyImport(t *testing.T, fixture serviceFixture, keys string) uint {
	t.Helper()
	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://key-import.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Keys:        keys,
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	return result.GroupID
}

func assertImportedKeyState(t *testing.T, fixture serviceFixture, groupID uint, want int) {
	t.Helper()
	var rows []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != want {
		t.Fatalf("persisted keys = %d, want %d", len(rows), want)
	}
	if candidates := fixture.registry.CollectCandidates([]uint{groupID}, nil); len(candidates) != want {
		t.Fatalf("Registry candidates = %#v, want %d", candidates, want)
	}
	for _, row := range rows {
		if value, ok := fixture.registry.EncryptedValue(row.ID); !ok || value != row.KeyValue {
			t.Fatalf("Registry key %d = %q, %t, want %q", row.ID, value, ok, row.KeyValue)
		}
	}
}
