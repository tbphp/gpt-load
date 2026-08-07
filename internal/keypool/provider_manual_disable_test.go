package keypool

import (
	"errors"
	"fmt"
	"gpt-load/internal/encryption"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestProvider(t *testing.T) (*KeyProvider, *gorm.DB, *store.MemoryStore) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.APIKey{}); err != nil {
		t.Fatal(err)
	}
	enc, err := encryption.NewService("")
	if err != nil {
		t.Fatal(err)
	}
	memoryStore := store.NewMemoryStore()
	return NewProvider(db, memoryStore, nil, enc), db, memoryStore
}

func TestSetKeyEnabledSynchronizesDatabaseAndStore(t *testing.T) {
	provider, db, memoryStore := newTestProvider(t)
	key := models.APIKey{GroupID: 7, KeyValue: "secret", Status: models.KeyStatusActive, FailureCount: 3}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := provider.addKeyToStore(&key); err != nil {
		t.Fatal(err)
	}

	updated, err := provider.SetKeyEnabled(key.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != models.KeyStatusDisabled {
		t.Fatalf("status = %q, want disabled", updated.Status)
	}
	if length, _ := memoryStore.LLen("group:7:active_keys"); length != 0 {
		t.Fatalf("active list length = %d, want 0", length)
	}
	details, _ := memoryStore.HGetAll(fmt.Sprintf("key:%d", key.ID))
	if details["status"] != models.KeyStatusDisabled {
		t.Fatalf("cached status = %q, want disabled", details["status"])
	}

	updated, err = provider.SetKeyEnabled(key.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != models.KeyStatusActive || updated.FailureCount != 0 {
		t.Fatalf("enabled key = status %q failures %d", updated.Status, updated.FailureCount)
	}
	if length, _ := memoryStore.LLen("group:7:active_keys"); length != 1 {
		t.Fatalf("active list length = %d, want 1", length)
	}
	var persisted models.APIKey
	if err := db.First(&persisted, key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.KeyStatusActive || persisted.FailureCount != 0 {
		t.Fatalf("persisted key = status %q failures %d", persisted.Status, persisted.FailureCount)
	}
}

func TestHandleSuccessDoesNotReactivateManuallyDisabledKey(t *testing.T) {
	provider, db, _ := newTestProvider(t)
	key := models.APIKey{GroupID: 9, KeyValue: "secret", Status: models.KeyStatusDisabled, FailureCount: 2}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := provider.addKeyToStore(&key); err != nil {
		t.Fatal(err)
	}
	if err := provider.handleSuccess(key.ID, fmt.Sprintf("key:%d", key.ID), "group:9:active_keys"); err != nil {
		t.Fatal(err)
	}
	var persisted models.APIKey
	if err := db.First(&persisted, key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.KeyStatusDisabled || persisted.FailureCount != 2 {
		t.Fatalf("disabled key changed: status %q failures %d", persisted.Status, persisted.FailureCount)
	}
}

func TestSetKeyEnabledRejectsAutomaticInvalidKey(t *testing.T) {
	provider, db, _ := newTestProvider(t)
	key := models.APIKey{GroupID: 11, KeyValue: "secret", Status: models.KeyStatusInvalid}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := provider.SetKeyEnabled(key.ID, true); err == nil {
		t.Fatal("expected enabling an automatic invalid key to fail")
	} else if !errors.Is(err, app_errors.ErrInvalidKeyStatus) {
		t.Fatalf("error = %v, want ErrInvalidKeyStatus", err)
	}

	if _, err := provider.SetKeyEnabled(key.ID, false); err == nil {
		t.Fatal("expected disabling an automatic invalid key to fail")
	} else if !errors.Is(err, app_errors.ErrInvalidKeyStatus) {
		t.Fatalf("error = %v, want ErrInvalidKeyStatus", err)
	}
	var persisted models.APIKey
	if err := db.First(&persisted, key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.KeyStatusInvalid {
		t.Fatalf("status = %q, want invalid", persisted.Status)
	}
}
