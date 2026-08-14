package models

type CredentialResetOperationState string

const (
	CredentialResetOperationPrepared       CredentialResetOperationState = "prepared"
	CredentialResetOperationSucceeded      CredentialResetOperationState = "succeeded"
	CredentialResetOperationRejected       CredentialResetOperationState = "rejected"
	CredentialResetOperationOutcomeUnknown CredentialResetOperationState = "outcome_unknown"
)

// CredentialResetOperation durably binds one control-plane idempotency key to
// one non-refundable upstream reset attempt. It never stores credential data.
type CredentialResetOperation struct {
	IdempotencyKey  string                        `gorm:"column:idempotency_key;type:char(36);primaryKey;not null"`
	RequestDigest   []byte                        `gorm:"column:request_digest;not null"`
	GroupID         uint                          `gorm:"column:group_id;not null;index:idx_credential_reset_operations_credential,priority:1"`
	CredentialID    uint                          `gorm:"column:credential_id;not null;index:idx_credential_reset_operations_credential,priority:2"`
	RedeemRequestID string                        `gorm:"column:redeem_request_id;type:char(36);not null;uniqueIndex"`
	State           CredentialResetOperationState `gorm:"type:varchar(32);not null"`
	ResultJSON      JSON                          `gorm:"column:result_json;type:json"`
	ErrorCode       string                        `gorm:"column:error_code;type:varchar(64);not null;default:''"`
	CreatedAtMS     int64                         `gorm:"column:created_at_ms;not null"`
	UpdatedAtMS     int64                         `gorm:"column:updated_at_ms;not null"`
	CompletedAtMS   *int64                        `gorm:"column:completed_at_ms"`
}

func (CredentialResetOperation) TableName() string { return "credential_reset_operations" }
