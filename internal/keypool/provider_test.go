package keypool

import (
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"gpt-load/internal/types"
)

type synchronizedReadStore struct {
	store.Store
	targetKey string
	wantReads int32
	reads     atomic.Int32
	release   chan struct{}
}

func (s *synchronizedReadStore) HGetAll(key string) (map[string]string, error) {
	values, err := s.Store.HGetAll(key)
	if err != nil || key != s.targetKey {
		return values, err
	}

	if s.reads.Add(1) == s.wantReads {
		close(s.release)
	}
	<-s.release

	return values, nil
}

type providerTestState struct {
	db                *gorm.DB
	store             *store.MemoryStore
	apiKey            *models.APIKey
	group             *models.Group
	keyHashKey        string
	activeKeysListKey string
}

func newProviderTestState(t *testing.T, blacklistThreshold int) *providerTestState {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get database connection: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&models.APIKey{}); err != nil {
		t.Fatalf("migrate API key table: %v", err)
	}

	apiKey := &models.APIKey{
		ID:       1,
		KeyValue: "test-key",
		GroupID:  1,
		Status:   models.KeyStatusActive,
	}
	if err := db.Create(apiKey).Error; err != nil {
		t.Fatalf("create API key: %v", err)
	}

	keyHashKey := "key:1"
	activeKeysListKey := "group:1:active_keys"
	memoryStore := store.NewMemoryStore()
	if err := memoryStore.HSet(keyHashKey, map[string]any{
		"failure_count": 0,
		"status":        models.KeyStatusActive,
	}); err != nil {
		t.Fatalf("seed key state: %v", err)
	}
	if err := memoryStore.LPush(activeKeysListKey, apiKey.ID); err != nil {
		t.Fatalf("seed active key list: %v", err)
	}

	return &providerTestState{
		db:     db,
		store:  memoryStore,
		apiKey: apiKey,
		group: &models.Group{
			ID: 1,
			EffectiveConfig: types.SystemSettings{
				BlacklistThreshold: blacklistThreshold,
			},
		},
		keyHashKey:        keyHashKey,
		activeKeysListKey: activeKeysListKey,
	}
}

func TestHandleFailureCountsConcurrentFailuresAndBlacklists(t *testing.T) {
	const (
		concurrentFailures = 4
		blacklistThreshold = 3
	)

	state := newProviderTestState(t, blacklistThreshold)
	synchronizedStore := &synchronizedReadStore{
		Store:     state.store,
		targetKey: state.keyHashKey,
		wantReads: concurrentFailures,
		release:   make(chan struct{}),
	}
	provider := NewProvider(state.db, synchronizedStore, nil, nil)

	start := make(chan struct{})
	errCh := make(chan error, concurrentFailures)
	for range concurrentFailures {
		go func() {
			<-start
			errCh <- provider.handleFailure(state.apiKey, state.group, state.keyHashKey, state.activeKeysListKey)
		}()
	}
	close(start)

	for range concurrentFailures {
		if err := <-errCh; err != nil {
			t.Fatalf("handle failure: %v", err)
		}
	}

	var persisted models.APIKey
	if err := state.db.First(&persisted, state.apiKey.ID).Error; err != nil {
		t.Fatalf("load API key: %v", err)
	}
	if persisted.FailureCount != concurrentFailures {
		t.Errorf("persisted failure count = %d, want %d", persisted.FailureCount, concurrentFailures)
	}
	if persisted.Status != models.KeyStatusInvalid {
		t.Errorf("persisted status = %q, want %q", persisted.Status, models.KeyStatusInvalid)
	}

	cached, err := state.store.HGetAll(state.keyHashKey)
	if err != nil {
		t.Fatalf("load cached key: %v", err)
	}
	cachedFailureCount, err := strconv.ParseInt(cached["failure_count"], 10, 64)
	if err != nil {
		t.Fatalf("parse cached failure count: %v", err)
	}
	if cachedFailureCount != concurrentFailures {
		t.Errorf("cached failure count = %d, want %d", cachedFailureCount, concurrentFailures)
	}
	if cached["status"] != models.KeyStatusInvalid {
		t.Errorf("cached status = %q, want %q", cached["status"], models.KeyStatusInvalid)
	}

	activeKeyCount, err := state.store.LLen(state.activeKeysListKey)
	if err != nil {
		t.Fatalf("count active keys: %v", err)
	}
	if activeKeyCount != 0 {
		t.Errorf("active key count = %d, want 0", activeKeyCount)
	}
}
