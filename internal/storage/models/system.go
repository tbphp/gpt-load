package models

// InternalSystemSettingPrefix reserves persistence metadata that must never
// become user-visible runtime configuration.
const InternalSystemSettingPrefix = "_internal."

// SystemSetting stores a dynamically configurable setting as a key-value pair.
type SystemSetting struct {
	Key         string `gorm:"type:varchar(255);primaryKey;not null"`
	Value       string `gorm:"type:text;not null"`
	UpdatedAtMS int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli"`
}

// Job is one durable background operation and its execution history.
type Job struct {
	ID           string `gorm:"type:varchar(36);primaryKey;not null"`
	Type         string `gorm:"type:varchar(64);not null;index"`
	Status       string `gorm:"type:varchar(32);not null;default:'pending';index"`
	Payload      JSON   `gorm:"type:json"`
	Result       JSON   `gorm:"type:json"`
	Error        string `gorm:"type:text"`
	CreatedAtMS  int64  `gorm:"column:created_at_ms;not null;index;autoCreateTime:milli"`
	StartedAtMS  *int64 `gorm:"column:started_at_ms"`
	FinishedAtMS *int64 `gorm:"column:finished_at_ms"`
}
