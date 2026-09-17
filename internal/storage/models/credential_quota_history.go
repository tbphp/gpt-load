package models

// CredentialQuotaHistory 是有效被动额度观测，不包含凭据或原始响应头。
// TargetIdentity 隔离同一凭据切换账号或上游目标后的历史。
type CredentialQuotaHistory struct {
	ID              uint   `gorm:"primaryKey;autoIncrement"`
	GroupID         uint   `gorm:"not null;check:chk_quota_history_group,group_id > 0"`
	CredentialID    uint   `gorm:"not null;uniqueIndex:idx_quota_history_identity,priority:1;check:chk_quota_history_credential,credential_id > 0"`
	TargetIdentity  string `gorm:"type:varchar(16);not null;uniqueIndex:idx_quota_history_identity,priority:2"`
	WindowKey       string `gorm:"type:varchar(64);not null;uniqueIndex:idx_quota_history_identity,priority:3"`
	WindowID        string `gorm:"type:varchar(128);not null"`
	SourceID        string `gorm:"type:varchar(128);not null;default:''"`
	Label           string `gorm:"type:varchar(255);not null"`
	LabelKey        string `gorm:"type:varchar(64);not null;default:''"`
	Scope           string `gorm:"type:varchar(255);not null"`
	ObservedAtMS    int64  `gorm:"column:observed_at_ms;not null;uniqueIndex:idx_quota_history_identity,priority:4;index:idx_quota_history_retention;check:chk_quota_history_time,observed_at_ms >= 0"`
	UsedBasisPoints int64  `gorm:"not null;check:chk_quota_history_percent,used_basis_points >= 0 AND used_basis_points <= 10000"`
	ResetAtMS       *int64 `gorm:"column:reset_at_ms;check:chk_quota_history_reset,reset_at_ms IS NULL OR reset_at_ms >= 0"`
	WindowSeconds   *int64 `gorm:"check:chk_quota_history_period,window_seconds IS NULL OR window_seconds > 0"`
}
