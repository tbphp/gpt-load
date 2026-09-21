package models

import (
	"crypto/sha256"
	"encoding/hex"
)

type ClientModelOverride struct {
	ModelHash   string `gorm:"type:varchar(64);primaryKey;not null"`
	ClientModel string `gorm:"type:text;not null"`
	Overrides   JSON   `gorm:"type:text;not null"`
}

func (ClientModelOverride) TableName() string { return "client_model_overrides" }

func ClientModelHash(model string) string {
	digest := sha256.Sum256([]byte(model))
	return hex.EncodeToString(digest[:])
}
