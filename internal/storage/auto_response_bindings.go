package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type autoBindingStore struct{ db *gorm.DB }

func NewResponseBindings(db *gorm.DB) *state.ResponseBindings {
	bindings := state.NewResponseBindings()
	bindings.SetStore(&autoBindingStore{db: db})
	return bindings
}

func responseHash(id string) string {
	value := sha256.Sum256([]byte(id))
	return hex.EncodeToString(value[:])
}

func (store *autoBindingStore) Lookup(accessKeyID uint, responseID string) (state.ResponseBinding, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var row models.AutoResponseBinding
	if err := store.db.WithContext(ctx).Where("access_key_id = ? AND response_hash = ? AND expires_at_ms > ?", accessKeyID, responseHash(responseID), time.Now().UnixMilli()).Take(&row).Error; err != nil {
		return state.ResponseBinding{}, false
	}
	var binding state.ResponseBinding
	if json.Unmarshal(row.Payload, &binding) != nil || binding.AccessKeyID != accessKeyID || binding.ResponseID != responseID || binding.AutoSelection == nil {
		return state.ResponseBinding{}, false
	}
	return binding, true
}

func (store *autoBindingStore) Record(binding state.ResponseBinding) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	raw, err := json.Marshal(binding)
	if err != nil {
		return false
	}
	if err := store.db.WithContext(ctx).Where("expires_at_ms <= ?", time.Now().UnixMilli()).Delete(&models.AutoResponseBinding{}).Error; err != nil {
		return false
	}
	row := models.AutoResponseBinding{AccessKeyID: binding.AccessKeyID, ResponseHash: responseHash(binding.ResponseID), ExpiresAtMS: binding.ExpiresAt.UnixMilli(), Payload: models.JSON(raw)}
	if err := store.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return false
	}
	existing, found := store.Lookup(binding.AccessKeyID, binding.ResponseID)
	if !found || existing.GroupID != binding.GroupID || existing.CredentialID != binding.CredentialID || existing.IdentityGeneration != binding.IdentityGeneration {
		return false
	}
	a, _ := json.Marshal(existing.AutoSelection)
	b, _ := json.Marshal(binding.AutoSelection)
	return string(a) == string(b)
}
