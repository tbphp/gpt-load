package models

import (
	"gpt-load/internal/types"
	"time"

	"gorm.io/datatypes"
)

// Key status
const (
	KeyStatusActive  = "active"
	KeyStatusInvalid = "invalid"
)

// SystemSetting corresponds to system_settings table
type SystemSetting struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	SettingKey   string    `gorm:"type:varchar(255);not null;unique" json:"setting_key"`
	SettingValue string    `gorm:"type:text;not null" json:"setting_value"`
	Description  string    `gorm:"type:varchar(512)" json:"description"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// GroupConfig stores configuration specific to a group
type GroupConfig struct {
	RequestTimeout               *int `json:"request_timeout,omitempty"`
	IdleConnTimeout              *int `json:"idle_conn_timeout,omitempty"`
	ConnectTimeout               *int `json:"connect_timeout,omitempty"`
	MaxIdleConns                 *int `json:"max_idle_conns,omitempty"`
	MaxIdleConnsPerHost          *int `json:"max_idle_conns_per_host,omitempty"`
	ResponseHeaderTimeout        *int `json:"response_header_timeout,omitempty"`
	MaxRetries                   *int `json:"max_retries,omitempty"`
	BlacklistThreshold           *int `json:"blacklist_threshold,omitempty"`
	KeyValidationIntervalMinutes *int `json:"key_validation_interval_minutes,omitempty"`
	KeyValidationConcurrency     *int `json:"key_validation_concurrency,omitempty"`
	KeyValidationTimeoutSeconds  *int `json:"key_validation_timeout_seconds,omitempty"`
}

// Group corresponds to groups table
type Group struct {
	ID              uint                 `gorm:"primaryKey;autoIncrement" json:"id"`
	EffectiveConfig types.SystemSettings `gorm:"-" json:"effective_config,omitempty"`
	Name            string               `gorm:"type:varchar(255);not null;unique" json:"name"`
	Endpoint        string               `gorm:"-" json:"endpoint"`
	DisplayName     string               `gorm:"type:varchar(255)" json:"display_name"`
	Description     string               `gorm:"type:varchar(512)" json:"description"`
	Upstreams       datatypes.JSON       `gorm:"type:json;not null" json:"upstreams"`
	ChannelType     string               `gorm:"type:varchar(50);not null" json:"channel_type"`
	Sort            int                  `gorm:"default:0" json:"sort"`
	TestModel       string               `gorm:"type:varchar(255);not null" json:"test_model"`
	ParamOverrides  datatypes.JSONMap    `gorm:"type:json" json:"param_overrides"`
	Config          datatypes.JSONMap    `gorm:"type:json" json:"config"`
	APIKeys         []APIKey             `gorm:"foreignKey:GroupID" json:"api_keys"`
	LastValidatedAt *time.Time           `json:"last_validated_at"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

// APIKey corresponds to api_keys table
type APIKey struct {
	ID           uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	KeyValue     string     `gorm:"type:varchar(512);not null;uniqueIndex:idx_group_key" json:"key_value"`
	GroupID      uint       `gorm:"not null;uniqueIndex:idx_group_key" json:"group_id"`
	Status       string     `gorm:"type:varchar(50);not null;default:'active'" json:"status"`
	RequestCount int64      `gorm:"not null;default:0" json:"request_count"`
	FailureCount int64      `gorm:"not null;default:0" json:"failure_count"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// RequestLog corresponds to request_logs table
type RequestLog struct {
	ID           string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	Timestamp    time.Time `gorm:"not null;index" json:"timestamp"`
	GroupID      uint      `gorm:"not null;index" json:"group_id"`
	GroupName    string    `gorm:"type:varchar(255);index" json:"group_name"`
	KeyValue     string    `gorm:"type:varchar(512)" json:"key_value"`
	IsSuccess    bool      `gorm:"not null" json:"is_success"`
	SourceIP     string    `gorm:"type:varchar(45)" json:"source_ip"`
	StatusCode   int       `gorm:"not null" json:"status_code"`
	RequestPath  string    `gorm:"type:varchar(500)" json:"request_path"`
	Duration     int64     `gorm:"not null" json:"duration_ms"`
	ErrorMessage string    `gorm:"type:text" json:"error_message"`
	UserAgent    string    `gorm:"type:varchar(512)" json:"user_agent"`
	Retries      int       `gorm:"not null" json:"retries"`
	UpstreamAddr string    `gorm:"type:varchar(500)" json:"upstream_addr"`
	IsStream     bool      `gorm:"not null" json:"is_stream"`
}

// StatCard used for individual statistical card data on the dashboard
type StatCard struct {
	Value         float64 `json:"value"`
	SubValue      int64   `json:"sub_value,omitempty"`
	SubValueTip   string  `json:"sub_value_tip,omitempty"`
	Trend         float64 `json:"trend"`
	TrendIsGrowth bool    `json:"trend_is_growth"`
}

// DashboardStatsResponse used for API response of basic dashboard statistics
type DashboardStatsResponse struct {
	KeyCount     StatCard `json:"key_count"`
	GroupCount   StatCard `json:"group_count"`
	RequestCount StatCard `json:"request_count"`
	ErrorRate    StatCard `json:"error_rate"`
}

// ChartDataset used for chart dataset
type ChartDataset struct {
	Label string  `json:"label"`
	Data  []int64 `json:"data"`
	Color string  `json:"color"`
}

// ChartData used for chart API response
type ChartData struct {
	Labels   []string       `json:"labels"`
	Datasets []ChartDataset `json:"datasets"`
}

// GroupHourlyStat corresponds to group_hourly_stats table, used to store hourly request statistics for each group
type GroupHourlyStat struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Time         time.Time `gorm:"not null;uniqueIndex:idx_group_time" json:"time"` // Hour timestamp
	GroupID      uint      `gorm:"not null;uniqueIndex:idx_group_time" json:"group_id"`
	SuccessCount int64     `gorm:"not null;default:0" json:"success_count"`
	FailureCount int64     `gorm:"not null;default:0" json:"failure_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
