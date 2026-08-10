package models

import (
	"bytes"

	"gorm.io/gorm"
)

// Group is the persisted configuration for an upstream service group.
// API DTOs and runtime state views must be defined outside the storage package.
type Group struct {
	ID              uint   `gorm:"primaryKey;autoIncrement"`
	Name            string `gorm:"type:varchar(255);not null;uniqueIndex"`
	ChannelID       string `gorm:"type:varchar(64);not null"`
	Params          JSON   `gorm:"type:json;not null"`
	Models          JSON   `gorm:"type:json;not null"`
	WeightManual    *int
	ValidationModel *string      `gorm:"type:varchar(255)"`
	Overrides       JSON         `gorm:"type:json"`
	Enabled         bool         `gorm:"not null;default:true"`
	Credentials     []Credential `gorm:"foreignKey:GroupID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS     int64        `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_group_created_at,created_at_ms >= 0"`
	UpdatedAtMS     int64        `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_group_updated_at,updated_at_ms >= 0"`
}

// BeforeSave keeps channel parameters representable. Channel-specific shape
// validation belongs to the code-owned channel registry.
func (group *Group) BeforeSave(_ *gorm.DB) error {
	trimmed := bytes.TrimSpace(group.Params)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		group.Params = JSON(`{}`)
	}
	return nil
}

// CredentialStatus is the durable operator-controlled state of channel data.
// Runtime cooldown and failure state belongs to the runtime credential registry.
type CredentialStatus string

const (
	CredentialStatusActive   CredentialStatus = "active"
	CredentialStatusDisabled CredentialStatus = "disabled"
)

// Credential is encrypted channel credential data that belongs to one group.
type Credential struct {
	ID           uint             `gorm:"primaryKey;autoIncrement"`
	GroupID      uint             `gorm:"not null;uniqueIndex:idx_credentials_group_fingerprint,priority:1"`
	Data         string           `gorm:"type:text;not null"`
	Fingerprint  string           `gorm:"type:varchar(128);not null;uniqueIndex:idx_credentials_group_fingerprint,priority:2"`
	Status       CredentialStatus `gorm:"type:varchar(32);not null;default:'active';check:chk_credential_status,status IN ('active','disabled')"`
	WeightManual *int
	Group        *Group `gorm:"foreignKey:GroupID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS  int64  `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_credential_created_at,created_at_ms >= 0"`
	UpdatedAtMS  int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_credential_updated_at,updated_at_ms >= 0"`
}
