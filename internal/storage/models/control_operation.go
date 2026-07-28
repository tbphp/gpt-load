package models

import "time"

// ControlOperation durably identifies a create/import mutation and its
// post-commit side-effect progress. It never stores plaintext credentials.
type ControlOperation struct {
	CommitSequence     uint64     `gorm:"primaryKey;autoIncrement"`
	OperationID        string     `gorm:"type:char(36);not null;uniqueIndex"`
	IdempotencyKey     string     `gorm:"type:char(36);not null;uniqueIndex"`
	DigestVersion      uint       `gorm:"not null;check:chk_control_operation_digest_version,digest_version > 0"`
	RequestDigest      []byte     `gorm:"type:blob;not null;check:chk_control_operation_digest,length(request_digest) = 32"`
	OperationKind      string     `gorm:"type:varchar(32);not null"`
	ResourceIdentity   string     `gorm:"type:varchar(64);not null"`
	CanonicalResult    []byte     `gorm:"type:blob"`
	RequiredStages     JSON       `gorm:"type:json"`
	LastCompletedStage string     `gorm:"type:varchar(32)"`
	FailedStage        string     `gorm:"type:varchar(32)"`
	CompletedAt        *time.Time `gorm:"index"`
	CompactedAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
