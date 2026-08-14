package models

import (
	"bytes"

	"gorm.io/gorm"

	"gpt-load/internal/connection"
)

// Group is the persisted configuration for an upstream service group.
// API DTOs and runtime state views must be defined outside the storage package.
type Group struct {
	ID              uint           `gorm:"primaryKey;autoIncrement"`
	Name            string         `gorm:"type:varchar(255);not null;uniqueIndex"`
	ChannelID       string         `gorm:"type:varchar(64);not null"`
	ConnectionType  ConnectionType `gorm:"type:varchar(32);not null;default:'api_key';check:chk_group_connection_type,connection_type IN ('api_key','subscription')"`
	Params          JSON           `gorm:"type:json;not null"`
	Models          JSON           `gorm:"type:json;not null"`
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
	if group.ConnectionType == "" {
		group.ConnectionType = ConnectionTypeAPIKey
	}
	trimmed := bytes.TrimSpace(group.Params)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		group.Params = JSON(`{}`)
	}
	return nil
}

// ConnectionType is the product-level way a Group authenticates upstream.
type ConnectionType string

const (
	ConnectionTypeAPIKey       ConnectionType = connection.APIKey
	ConnectionTypeSubscription ConnectionType = connection.Subscription
)

// CredentialStatus is the durable operator-controlled state of channel data.
// Runtime cooldown and failure state belongs to the runtime credential registry.
type CredentialStatus string

const (
	CredentialStatusActive   CredentialStatus = "active"
	CredentialStatusDisabled CredentialStatus = "disabled"
)

// Credential is encrypted channel credential data that belongs to one group.
type Credential struct {
	ID                  uint                `gorm:"primaryKey;autoIncrement"`
	GroupID             uint                `gorm:"not null;uniqueIndex:idx_credentials_group_fingerprint,priority:1;uniqueIndex:idx_credentials_group_identity,priority:1"`
	Data                string              `gorm:"type:text;not null"`
	Fingerprint         string              `gorm:"type:varchar(128);not null;uniqueIndex:idx_credentials_group_fingerprint,priority:2"`
	IdentityFingerprint string              `gorm:"type:varchar(128);not null;uniqueIndex:idx_credentials_group_identity,priority:2"`
	SecretVersion       uint64              `gorm:"not null;default:1;check:chk_credential_secret_version,secret_version > 0"`
	AuthState           CredentialAuthState `gorm:"type:varchar(32);not null;default:'ready';check:chk_credential_auth_state,auth_state IN ('ready','refreshing','reauthorization_required','outcome_unknown')"`
	AuthErrorCode       string              `gorm:"type:varchar(64);not null;default:''"`
	Status              CredentialStatus    `gorm:"type:varchar(32);not null;default:'active';check:chk_credential_status,status IN ('active','disabled')"`
	WeightManual        *int
	Group               *Group `gorm:"foreignKey:GroupID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS         int64  `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_credential_created_at,created_at_ms >= 0"`
	UpdatedAtMS         int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_credential_updated_at,updated_at_ms >= 0"`
}

type CredentialAuthState string

const (
	CredentialAuthStateReady                   CredentialAuthState = "ready"
	CredentialAuthStateRefreshing              CredentialAuthState = "refreshing"
	CredentialAuthStateReauthorizationRequired CredentialAuthState = "reauthorization_required"
	CredentialAuthStateOutcomeUnknown          CredentialAuthState = "outcome_unknown"
)

// BeforeCreate keeps API-key model construction backward compatible while
// subscription callers may provide a stable identity independent of token data.
func (credential *Credential) BeforeCreate(_ *gorm.DB) error {
	if credential.IdentityFingerprint == "" {
		credential.IdentityFingerprint = credential.Fingerprint
	}
	if credential.SecretVersion == 0 {
		credential.SecretVersion = 1
	}
	if credential.AuthState == "" {
		credential.AuthState = CredentialAuthStateReady
	}
	return nil
}
