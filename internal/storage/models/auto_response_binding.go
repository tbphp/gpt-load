package models

// AutoResponseBinding retains automatic selections across gateway instances.
type AutoResponseBinding struct {
	AccessKeyID  uint   `gorm:"primaryKey;not null"`
	ResponseHash string `gorm:"type:varchar(64);primaryKey;not null"`
	ExpiresAtMS  int64  `gorm:"not null;index:idx_auto_response_bindings_expiry"`
	Payload      JSON   `gorm:"type:json;not null"`
}
