package models

import "time"

// UpstreamKeyStatus is the durable operator-controlled state of a credential.
// Runtime cooldown and failure state belongs to state.KeyRegistry.
type UpstreamKeyStatus string

const (
	UpstreamKeyStatusActive   UpstreamKeyStatus = "active"
	UpstreamKeyStatusDisabled UpstreamKeyStatus = "disabled"
)

// Group is the persisted configuration for an upstream service group.
// API DTOs and runtime state views must be defined outside the storage package.
type Group struct {
	ID              uint   `gorm:"primaryKey;autoIncrement"`
	Name            string `gorm:"type:varchar(255);not null;uniqueIndex"`
	UpstreamURL     string `gorm:"type:text;not null"`
	Protocols       JSON   `gorm:"type:json;not null"`
	Models          JSON   `gorm:"type:json;not null"`
	ConvertEnabled  bool   `gorm:"not null;default:false"`
	WeightManual    *int
	ValidationModel *string       `gorm:"type:varchar(255)"`
	Config          JSON          `gorm:"type:json"`
	Enabled         bool          `gorm:"not null;default:true"`
	UpstreamKeys    []UpstreamKey `gorm:"foreignKey:GroupID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// UpstreamKey is an encrypted provider credential that belongs to one group.
type UpstreamKey struct {
	ID           uint              `gorm:"primaryKey;autoIncrement"`
	GroupID      uint              `gorm:"not null;uniqueIndex:idx_upstream_keys_group_hash,priority:1"`
	KeyValue     string            `gorm:"type:text;not null"`
	KeyHash      string            `gorm:"type:varchar(128);not null;uniqueIndex:idx_upstream_keys_group_hash,priority:2"`
	Status       UpstreamKeyStatus `gorm:"type:varchar(32);not null;default:'active';check:chk_upstream_key_status,status IN ('active','disabled')"`
	WeightManual *int
	RequestCount int64   `gorm:"not null;default:0"`
	TokensTotal  int64   `gorm:"not null;default:0"`
	CostTotal    float64 `gorm:"not null;default:0"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
