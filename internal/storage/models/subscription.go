package models

type CredentialStageStatus string

const (
	CredentialStagePendingAuthorization CredentialStageStatus = "pending_authorization"
	CredentialStageExchanging           CredentialStageStatus = "exchanging"
	CredentialStageReady                CredentialStageStatus = "ready"
	CredentialStageConsumed             CredentialStageStatus = "consumed"
	CredentialStageFailed               CredentialStageStatus = "failed"
	CredentialStageCancelled            CredentialStageStatus = "cancelled"
	CredentialStageExpired              CredentialStageStatus = "expired"
	CredentialStageOutcomeUnknown       CredentialStageStatus = "outcome_unknown"
)

type CredentialStage struct {
	ID                   string                `gorm:"type:varchar(36);primaryKey;not null"`
	ChannelID            string                `gorm:"type:varchar(64);not null"`
	ConnectionType       ConnectionType        `gorm:"type:varchar(32);not null"`
	AuthorizationMethod  string                `gorm:"type:varchar(32);not null"`
	Status               CredentialStageStatus `gorm:"type:varchar(32);not null"`
	EncryptedPayload     string                `gorm:"type:text;not null"`
	PayloadSchemaVersion uint                  `gorm:"not null;default:1"`
	SafeSummaryJSON      JSON                  `gorm:"column:safe_summary_json;type:json;not null"`
	IdentityFingerprint  string                `gorm:"type:varchar(128);not null;default:''"`
	OAuthStateHash       *string               `gorm:"column:oauth_state_hash;type:varchar(128)"`
	ExpiresAtMS          int64                 `gorm:"column:expires_at_ms;not null"`
	ConsumedAtMS         *int64                `gorm:"column:consumed_at_ms"`
	ConsumedGroupID      *uint
	ErrorCode            string `gorm:"type:varchar(64);not null;default:''"`
	CreatedAtMS          int64  `gorm:"column:created_at_ms;not null"`
	UpdatedAtMS          int64  `gorm:"column:updated_at_ms;not null"`
}

type CredentialObservationState string

const (
	CredentialObservationFresh       CredentialObservationState = "fresh"
	CredentialObservationStale       CredentialObservationState = "stale"
	CredentialObservationRefreshing  CredentialObservationState = "refreshing"
	CredentialObservationError       CredentialObservationState = "error"
	CredentialObservationUnavailable CredentialObservationState = "unavailable"
)

type CredentialObservation struct {
	CredentialID        uint                       `gorm:"primaryKey;not null"`
	Credential          *Credential                `gorm:"foreignKey:CredentialID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	IdentityFingerprint string                     `gorm:"type:varchar(128);not null"`
	SchemaVersion       uint                       `gorm:"not null;default:1"`
	ObservationVersion  uint64                     `gorm:"not null;default:1"`
	SnapshotJSON        JSON                       `gorm:"column:snapshot_json;type:json;not null"`
	State               CredentialObservationState `gorm:"type:varchar(32);not null"`
	ObservedAtMS        *int64                     `gorm:"column:observed_at_ms"`
	FreshUntilMS        *int64                     `gorm:"column:fresh_until_ms"`
	LastAttemptAtMS     *int64                     `gorm:"column:last_attempt_at_ms"`
	NextAllowedAtMS     *int64                     `gorm:"column:next_allowed_at_ms"`
	LastErrorCode       string                     `gorm:"type:varchar(64);not null;default:''"`
	UpdatedAtMS         int64                      `gorm:"column:updated_at_ms;not null"`
}
