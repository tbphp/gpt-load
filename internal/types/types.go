package types

// ConfigManager defines the interface for configuration management
type ConfigManager interface {
	IsMaster() bool
	GetAuthConfig() AuthConfig
	GetCORSConfig() CORSConfig
	GetPerformanceConfig() PerformanceConfig
	GetLogConfig() LogConfig
	GetDatabaseConfig() DatabaseConfig
	GetEncryptionKey() string
	GetEffectiveServerConfig() ServerConfig
	GetRedisDSN() string
	Validate() error
	DisplayServerConfig()
	ReloadConfig() error
}

// SystemSettings 定义所有系统配置项
type SystemSettings struct {
	// 基础参数
	AppUrl                         string `json:"app_url" default:"http://localhost:3001" name:"config.app_url" category:"config.category.basic" desc:"config.app_url_desc" validate:"required"`
	ProxyKeys                      string `json:"proxy_keys" name:"config.proxy_keys" category:"config.category.basic" desc:"config.proxy_keys_desc" validate:"required"`
	RequestLogRetentionDays        int    `json:"request_log_retention_days" default:"7" name:"config.log_retention_days" category:"config.category.basic" desc:"config.log_retention_days_desc" validate:"required,min=0"`
	RequestLogWriteIntervalMinutes int    `json:"request_log_write_interval_minutes" default:"1" name:"config.log_write_interval" category:"config.category.basic" desc:"config.log_write_interval_desc" validate:"required,min=0"`
	EnableRequestBodyLogging       bool   `json:"enable_request_body_logging" default:"false" name:"config.enable_request_body_logging" category:"config.category.basic" desc:"config.enable_request_body_logging_desc"`

	// 请求设置
	RequestTimeout        int    `json:"request_timeout" default:"600" name:"config.request_timeout" category:"config.category.request" desc:"config.request_timeout_desc" validate:"required,min=1"`
	ConnectTimeout        int    `json:"connect_timeout" default:"15" name:"config.connect_timeout" category:"config.category.request" desc:"config.connect_timeout_desc" validate:"required,min=1"`
	IdleConnTimeout       int    `json:"idle_conn_timeout" default:"120" name:"config.idle_conn_timeout" category:"config.category.request" desc:"config.idle_conn_timeout_desc" validate:"required,min=1"`
	ResponseHeaderTimeout int    `json:"response_header_timeout" default:"600" name:"config.response_header_timeout" category:"config.category.request" desc:"config.response_header_timeout_desc" validate:"required,min=1"`
	MaxIdleConns          int    `json:"max_idle_conns" default:"100" name:"config.max_idle_conns" category:"config.category.request" desc:"config.max_idle_conns_desc" validate:"required,min=1"`
	MaxIdleConnsPerHost   int    `json:"max_idle_conns_per_host" default:"50" name:"config.max_idle_conns_per_host" category:"config.category.request" desc:"config.max_idle_conns_per_host_desc" validate:"required,min=1"`
	ProxyURL              string `json:"proxy_url" name:"config.proxy_url" category:"config.category.request" desc:"config.proxy_url_desc"`

	// 密钥配置
	MaxRetries                   int `json:"max_retries" default:"3" name:"config.max_retries" category:"config.category.key" desc:"config.max_retries_desc" validate:"required,min=0"`
	BlacklistThreshold           int `json:"blacklist_threshold" default:"3" name:"config.blacklist_threshold" category:"config.category.key" desc:"config.blacklist_threshold_desc" validate:"required,min=0"`
	KeyValidationIntervalMinutes int `json:"key_validation_interval_minutes" default:"60" name:"config.key_validation_interval" category:"config.category.key" desc:"config.key_validation_interval_desc" validate:"required,min=1"`
	KeyValidationConcurrency     int `json:"key_validation_concurrency" default:"10" name:"config.key_validation_concurrency" category:"config.category.key" desc:"config.key_validation_concurrency_desc" validate:"required,min=1"`
	KeyValidationTimeoutSeconds  int `json:"key_validation_timeout_seconds" default:"20" name:"config.key_validation_timeout" category:"config.category.key" desc:"config.key_validation_timeout_desc" validate:"required,min=1"`

	// For cache
	ProxyKeysMap map[string]struct{} `json:"-"`
}

// ServerConfig represents server configuration
type ServerConfig struct {
	Port                    int    `json:"port"`
	Host                    string `json:"host"`
	IsMaster                bool   `json:"is_master"`
	ReadTimeout             int    `json:"read_timeout"`
	WriteTimeout            int    `json:"write_timeout"`
	IdleTimeout             int    `json:"idle_timeout"`
	GracefulShutdownTimeout int    `json:"graceful_shutdown_timeout"`
}

// AuthConfig represents authentication configuration
type AuthConfig struct {
	Key string `json:"key"`
}

// CORSConfig represents CORS configuration
type CORSConfig struct {
	Enabled          bool     `json:"enabled"`
	AllowedOrigins   []string `json:"allowed_origins"`
	AllowedMethods   []string `json:"allowed_methods"`
	AllowedHeaders   []string `json:"allowed_headers"`
	AllowCredentials bool     `json:"allow_credentials"`
}

// PerformanceConfig represents performance configuration
type PerformanceConfig struct {
	MaxConcurrentRequests int `json:"max_concurrent_requests"`
}

// LogConfig represents logging configuration
type LogConfig struct {
	Level      string `json:"level"`
	Format     string `json:"format"`
	EnableFile bool   `json:"enable_file"`
	FilePath   string `json:"file_path"`
}

// DatabaseConfig represents database configuration
type DatabaseConfig struct {
	DSN string `json:"dsn"`
}

type RetryError struct {
	StatusCode         int    `json:"status_code"`
	ErrorMessage       string `json:"error_message"`
	ParsedErrorMessage string `json:"-"`
	KeyValue           string `json:"key_value"`
	Attempt            int    `json:"attempt"`
	UpstreamAddr       string `json:"-"`
}
