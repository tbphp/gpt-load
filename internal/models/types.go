package models

import (
	"gpt-load/internal/types"
	"time"

	"gorm.io/datatypes"
)

// Key状态
const (
	KeyStatusActive  = "active"
	KeyStatusInvalid = "invalid"
)

// SystemSetting 对应 system_settings 表
type SystemSetting struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	SettingKey   string    `gorm:"type:varchar(255);not null;unique" json:"setting_key"`
	SettingValue string    `gorm:"type:text;not null" json:"setting_value"`
	Description  string    `gorm:"type:varchar(512)" json:"description"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// GroupConfig 存储特定于分组的配置
type GroupConfig struct {
	RequestTimeout               *int    `json:"request_timeout,omitempty"`
	IdleConnTimeout              *int    `json:"idle_conn_timeout,omitempty"`
	ConnectTimeout               *int    `json:"connect_timeout,omitempty"`
	MaxIdleConns                 *int    `json:"max_idle_conns,omitempty"`
	MaxIdleConnsPerHost          *int    `json:"max_idle_conns_per_host,omitempty"`
	ResponseHeaderTimeout        *int    `json:"response_header_timeout,omitempty"`
	ProxyURL                     *string `json:"proxy_url,omitempty"`
	MaxRetries                   *int    `json:"max_retries,omitempty"`
	BlacklistThreshold           *int    `json:"blacklist_threshold,omitempty"`
	KeyValidationIntervalMinutes *int    `json:"key_validation_interval_minutes,omitempty"`
	KeyValidationConcurrency     *int    `json:"key_validation_concurrency,omitempty"`
	KeyValidationTimeoutSeconds  *int    `json:"key_validation_timeout_seconds,omitempty"`
}

// Group 对应 groups 表
type Group struct {
	ID                 uint                 `gorm:"primaryKey;autoIncrement" json:"id"`
	EffectiveConfig    types.SystemSettings `gorm:"-" json:"effective_config,omitempty"`
	Name               string               `gorm:"type:varchar(255);not null;unique" json:"name"`
	Endpoint           string               `gorm:"-" json:"endpoint"`
	DisplayName        string               `gorm:"type:varchar(255)" json:"display_name"`
	ProxyKeys          string               `gorm:"type:text" json:"proxy_keys"`
	Description        string               `gorm:"type:varchar(512)" json:"description"`
	Upstreams          datatypes.JSON       `gorm:"type:json;not null" json:"upstreams"`
	ValidationEndpoint string               `gorm:"type:varchar(255)" json:"validation_endpoint"`
	ChannelType        string               `gorm:"type:varchar(50);not null" json:"channel_type"`
	Sort               int                  `gorm:"default:0" json:"sort"`
	TestModel          string               `gorm:"type:varchar(255);not null" json:"test_model"`
	ParamOverrides     datatypes.JSONMap    `gorm:"type:json" json:"param_overrides"`
	Config             datatypes.JSONMap    `gorm:"type:json" json:"config"`
	APIKeys            []APIKey             `gorm:"foreignKey:GroupID" json:"api_keys"`
	LastValidatedAt    *time.Time           `json:"last_validated_at"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`

	// For cache
	ProxyKeysMap map[string]struct{} `gorm:"-" json:"-"`
}

// APIKey 对应 api_keys 表
type APIKey struct {
	ID           uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	KeyValue     string     `gorm:"type:varchar(700);not null;uniqueIndex:idx_group_key" json:"key_value"`
	GroupID      uint       `gorm:"not null;uniqueIndex:idx_group_key" json:"group_id"`
	Status       string     `gorm:"type:varchar(50);not null;default:'active'" json:"status"`
	RequestCount int64      `gorm:"not null;default:0" json:"request_count"`
	FailureCount int64      `gorm:"not null;default:0" json:"failure_count"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// RequestLog 对应 request_logs 表
type RequestLog struct {
	ID           string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	Timestamp    time.Time `gorm:"not null;index" json:"timestamp"`
	GroupID      uint      `gorm:"not null;index" json:"group_id"`
	GroupName    string    `gorm:"type:varchar(255);index" json:"group_name"`
	KeyValue     string    `gorm:"type:varchar(700)" json:"key_value"`
	Model        string    `gorm:"type:varchar(255);index" json:"model"`
	IsSuccess    bool      `gorm:"not null" json:"is_success"`
	SourceIP     string    `gorm:"type:varchar(64)" json:"source_ip"`
	StatusCode   int       `gorm:"not null" json:"status_code"`
	RequestPath  string    `gorm:"type:varchar(500)" json:"request_path"`
	Duration     int64     `gorm:"not null" json:"duration_ms"`
	ErrorMessage string    `gorm:"type:text" json:"error_message"`
	UserAgent    string    `gorm:"type:varchar(512)" json:"user_agent"`
	Retries      int       `gorm:"not null" json:"retries"`
	UpstreamAddr string    `gorm:"type:varchar(500)" json:"upstream_addr"`
	IsStream     bool      `gorm:"not null" json:"is_stream"`
}

// StatCard 用于仪表盘的单个统计卡片数据
type StatCard struct {
	Value         float64 `json:"value"`
	SubValue      int64   `json:"sub_value,omitempty"`
	SubValueTip   string  `json:"sub_value_tip,omitempty"`
	Trend         float64 `json:"trend"`
	TrendIsGrowth bool    `json:"trend_is_growth"`
}

// DashboardStatsResponse 用于仪表盘基础统计的API响应
type DashboardStatsResponse struct {
	KeyCount     StatCard `json:"key_count"`
	RPM          StatCard `json:"rpm"`
	RequestCount StatCard `json:"request_count"`
	ErrorRate    StatCard `json:"error_rate"`
}

// ChartDataset 用于图表的数据集
type ChartDataset struct {
	Label string  `json:"label"`
	Data  []int64 `json:"data"`
	Color string  `json:"color"`
}

// ChartData 用于图表的API响应
type ChartData struct {
	Labels   []string       `json:"labels"`
	Datasets []ChartDataset `json:"datasets"`
}

// GroupHourlyStat 对应 group_hourly_stats 表，用于存储每个分组每小时的请求统计
type GroupHourlyStat struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Time         time.Time `gorm:"not null;uniqueIndex:idx_group_time" json:"time"` // 整点时间
	GroupID      uint      `gorm:"not null;uniqueIndex:idx_group_time" json:"group_id"`
	SuccessCount int64     `gorm:"not null;default:0" json:"success_count"`
	FailureCount int64     `gorm:"not null;default:0" json:"failure_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
