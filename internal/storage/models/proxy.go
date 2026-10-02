package models

// Proxy 保存可复用的加密出站配置；引用保留在既有 proxy_config 中。
type Proxy struct {
	ID                 uint   `gorm:"primaryKey;autoIncrement"`
	Name               string `gorm:"type:varchar(255);not null"`
	Config             string `gorm:"type:text;not null"`
	Fingerprint        string `gorm:"type:varchar(128);not null;uniqueIndex:ux_proxies_fingerprint"`
	Enabled            bool   `gorm:"not null;default:true;index:idx_proxies_enabled"`
	LastTestURL        string `gorm:"column:last_test_url;type:varchar(2048);not null;default:''"`
	LastTestAtMS       *int64 `gorm:"column:last_test_at_ms"`
	LastTestDurationMS *int64 `gorm:"column:last_test_duration_ms"`
	LastTestStatusCode *int   `gorm:"column:last_test_status_code"`
	LastTestError      string `gorm:"type:varchar(64);not null;default:''"`
	CreatedAtMS        int64  `gorm:"column:created_at_ms;not null;autoCreateTime:milli"`
	UpdatedAtMS        int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli"`
}
