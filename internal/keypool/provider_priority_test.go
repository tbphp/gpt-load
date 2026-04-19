package keypool

import (
	"testing"

	"gpt-load/internal/encryption"
	"gpt-load/internal/models"
	"gpt-load/internal/store"

	"github.com/glebarez/sqlite"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestSelectKey_PriorityModeUsesHighestWeightWithStableTieBreak(t *testing.T) {
	db := openPriorityTestDB(t)
	memStore := store.NewMemoryStore()
	encryptionSvc, err := encryption.NewService("")
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}

	group := models.Group{
		Name:        "priority-standard",
		GroupType:   "standard",
		ChannelType: "openai",
		TestModel:   "gpt-4.1-nano",
		Upstreams:   datatypes.JSON([]byte(`[{"url":"https://api.openai.com","weight":1}]`)),
		Config: datatypes.JSONMap{
			"key_selection_mode": "priority",
		},
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}

	provider := NewProvider(db, memStore, nil, encryptionSvc)
	keys := []models.APIKey{
		{GroupID: group.ID, KeyValue: "key-a", KeyHash: encryptionSvc.Hash("key-a"), Status: models.KeyStatusActive, Weight: 99},
		{GroupID: group.ID, KeyValue: "key-b", KeyHash: encryptionSvc.Hash("key-b"), Status: models.KeyStatusActive, Weight: 99},
		{GroupID: group.ID, KeyValue: "key-c", KeyHash: encryptionSvc.Hash("key-c"), Status: models.KeyStatusActive, Weight: 98},
	}
	if err := provider.AddKeys(group.ID, keys); err != nil {
		t.Fatalf("AddKeys returned error: %v", err)
	}

	selected, err := provider.SelectKey(group.ID)
	if err != nil {
		t.Fatalf("SelectKey returned error: %v", err)
	}
	if selected.KeyValue != "key-a" {
		t.Fatalf("expected first highest-priority key key-a, got %s", selected.KeyValue)
	}

	if err := db.Model(&models.APIKey{}).Where("id = ?", selected.ID).Update("status", models.KeyStatusInvalid).Error; err != nil {
		t.Fatalf("mark first key invalid: %v", err)
	}

	fallback, err := provider.SelectKey(group.ID)
	if err != nil {
		t.Fatalf("SelectKey fallback returned error: %v", err)
	}
	if fallback.KeyValue != "key-b" {
		t.Fatalf("expected fallback key-b after key-a invalid, got %s", fallback.KeyValue)
	}
}

func openPriorityTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Group{}, &models.APIKey{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}
