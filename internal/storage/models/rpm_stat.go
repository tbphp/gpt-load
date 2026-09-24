package models

// RPMStat 保存每分钟的请求数与 60 秒滑窗峰值，不包含密钥或请求正文。
// SessionID 隔离进程重启前后的绝对快照，重复写入同一快照不会重复累加。
type RPMStat struct {
	ID        uint   `gorm:"primaryKey;autoIncrement"`
	Kind      string `gorm:"type:varchar(16);not null;check:chk_rpm_stats_kind,kind IN ('access_key','credential');uniqueIndex:idx_rpm_stats_identity,priority:1"`
	KeyID     uint   `gorm:"not null;check:chk_rpm_stats_key,key_id > 0;uniqueIndex:idx_rpm_stats_identity,priority:2"`
	MinuteMS  int64  `gorm:"column:minute_ms;not null;check:chk_rpm_stats_minute,minute_ms >= 0;uniqueIndex:idx_rpm_stats_identity,priority:3;index:idx_rpm_stats_retention"`
	SessionID string `gorm:"type:varchar(32);not null;uniqueIndex:idx_rpm_stats_identity,priority:4"`
	Requests  int64  `gorm:"not null;check:chk_rpm_stats_requests,requests >= 0"`
	Rejected  int64  `gorm:"not null;check:chk_rpm_stats_rejected,rejected >= 0 AND rejected <= requests"`
	Peak      int64  `gorm:"not null;check:chk_rpm_stats_peak,peak >= 0"`
}

func (RPMStat) TableName() string { return "rpm_stats" }
