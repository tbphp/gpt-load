package keypool

import (
	"errors"
	"gpt-load/internal/encryption"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestProvider(t *testing.T) *KeyProvider {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.AutoMigrate(&models.APIKey{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	enc, err := encryption.NewService("")
	if err != nil {
		t.Fatalf("failed to init encryption: %v", err)
	}
	p := NewProvider(db, store.NewMemoryStore(), nil, enc)

	keys := []models.APIKey{
		{KeyValue: "sk-unrestricted", Status: models.KeyStatusActive, GroupID: 1},
		{KeyValue: "sk-only-glm", Status: models.KeyStatusActive, GroupID: 1, AllowedModels: "glm-5.2"},
		{KeyValue: "sk-only-deepseek", Status: models.KeyStatusActive, GroupID: 1, AllowedModels: "deepseek-v4-pro-0813,deepseek-v4-flash-0731"},
	}
	// AddKeys 同时负责 DB 创建与 store 缓存
	if err := p.AddKeys(1, keys); err != nil {
		t.Fatalf("failed to add keys to pool: %v", err)
	}
	return p
}

func TestSelectKeyForGroupModel(t *testing.T) {
	p := newTestProvider(t)

	// 1. 全受限组 + 不匹配模型 → ErrNoKeyForModel
	restricted := []models.APIKey{
		{KeyValue: "sk-A-glm", Status: models.KeyStatusActive, GroupID: 2, AllowedModels: "glm-5.2"},
		{KeyValue: "sk-B-deepseek", Status: models.KeyStatusActive, GroupID: 2, AllowedModels: "deepseek-v4-pro-0813"},
	}
	if err := p.AddKeys(2, restricted); err != nil {
		t.Fatalf("failed to add restricted keys: %v", err)
	}
	if _, err := p.SelectKeyForGroupModel(2, "nonexistent-model"); !errors.Is(err, ErrNoKeyForModel) {
		t.Fatalf("expected ErrNoKeyForModel, got %v", err)
	}

	// 2. 混合组: 任意模型至少可命中无限制 key
	if _, err := p.SelectKeyForGroupModel(1, "glm-5.2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 0; i < 10; i++ {
		key, err := p.SelectKeyForGroupModel(1, "glm-5.2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key.KeyValue == "sk-only-deepseek" {
			t.Fatalf("selected key not allowed to serve glm-5.2: %s", key.KeyValue)
		}
	}

	// 3. deepseek → 不可能选中 sk-only-glm
	for i := 0; i < 10; i++ {
		key, err := p.SelectKeyForGroupModel(1, "deepseek-v4-pro-0813")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key.KeyValue == "sk-only-glm" {
			t.Fatalf("selected key not allowed to serve deepseek-v4-pro-0813: %s", key.KeyValue)
		}
	}

	// 4. 空模型退化为无过滤选择（应覆盖含受限 key 在内的全部）
	for i := 0; i < 10; i++ {
		if _, err := p.SelectKeyForGroupModel(1, ""); err != nil {
			t.Fatalf("empty model fallback failed: %v", err)
		}
	}

	// 5. 轮换: glm-5.2 只有两个候选, 应都轮流被选中
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		key, _ := p.SelectKeyForGroupModel(1, "glm-5.2")
		seen[key.KeyValue] = true
	}
	if !seen["sk-unrestricted"] || !seen["sk-only-glm"] {
		t.Fatalf("rotation did not cover both eligible keys: %v", seen)
	}
}

func TestCanServeModel(t *testing.T) {
	k := &models.APIKey{AllowedModels: ""}
	if !k.CanServeModel("anything") {
		t.Fatal("empty allowed_models must allow all models")
	}

	k = &models.APIKey{AllowedModels: "glm-5.2, deepseek-v4-pro-0813"}
	if !k.CanServeModel("glm-5.2") || !k.CanServeModel("deepseek-v4-pro-0813") {
		t.Fatal("configured model should be allowed")
	}
	if k.CanServeModel("qwen3.8-max") {
		t.Fatal("non-configured model should be denied")
	}
	if !k.CanServeModel("") {
		t.Fatal("empty model should always be allowed (e.g. /models)")
	}
}