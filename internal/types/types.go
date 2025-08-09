package types

// ConfigManager defines the interface for configuration management
type ConfigManager interface {
	IsMaster() bool
	GetAuthConfig() AuthConfig
	GetCORSConfig() CORSConfig
	GetPerformanceConfig() PerformanceConfig
	GetLogConfig() LogConfig
	GetDatabaseConfig() DatabaseConfig
	GetEffectiveServerConfig() ServerConfig
	GetRedisDSN() string
	Validate() error
	DisplayServerConfig()
	ReloadConfig() error
}

// SystemSettings 定义所有系统配置项
type SystemSettings struct {
	// 基础参数
	AppUrl                         string `json:"app_url" default:"http://localhost:3001" name:"项目地址" category:"基础参数" desc:"项目的基础 URL，用于拼接分组终端节点地址。系统配置优先于环境变量 APP_URL。" validate:"required"`
	RequestLogRetentionDays        int    `json:"request_log_retention_days" default:"7" name:"日志保留时长（天）" category:"基础参数" desc:"请求日志在数据库中的保留天数，0为不清理日志。" validate:"required,min=0"`
	RequestLogWriteIntervalMinutes int    `json:"request_log_write_interval_minutes" default:"1" name:"日志延迟写入周期（分钟）" category:"基础参数" desc:"请求日志从缓存写入数据库的周期（分钟），0为实时写入数据。" validate:"required,min=0"`
	ProxyKeys                      string `json:"proxy_keys" name:"全局代理密钥" category:"基础参数" desc:"全局代理密钥，用于访问所有分组的代理端点。多个密钥请用逗号分隔。" validate:"required"`

	// 请求设置
	RequestTimeout        int    `json:"request_timeout" default:"600" name:"请求超时（秒）" category:"请求设置" desc:"转发请求的完整生命周期超时（秒）等。" validate:"required,min=1"`
	ConnectTimeout        int    `json:"connect_timeout" default:"15" name:"连接超时（秒）" category:"请求设置" desc:"与上游服务建立新连接的超时时间（秒）。" validate:"required,min=1"`
	IdleConnTimeout       int    `json:"idle_conn_timeout" default:"120" name:"空闲连接超时（秒）" category:"请求设置" desc:"HTTP 客户端中空闲连接的超时时间（秒）。" validate:"required,min=1"`
	ResponseHeaderTimeout int    `json:"response_header_timeout" default:"600" name:"响应头超时（秒）" category:"请求设置" desc:"等待上游服务响应头的最长时间（秒）。" validate:"required,min=1"`
	MaxIdleConns          int    `json:"max_idle_conns" default:"100" name:"最大空闲连接数" category:"请求设置" desc:"HTTP 客户端连接池中允许的最大空闲连接总数。" validate:"required,min=1"`
	MaxIdleConnsPerHost   int    `json:"max_idle_conns_per_host" default:"50" name:"每主机最大空闲连接数" category:"请求设置" desc:"HTTP 客户端连接池对每个上游主机允许的最大空闲连接数。" validate:"required,min=1"`
	ProxyURL              string `json:"proxy_url" name:"代理服务器地址" category:"请求设置" desc:"全局 HTTP/HTTPS 代理服务器地址，例如：http://user:pass@host:port。如果为空，则使用环境变量配置。"`

	// 密钥配置
	MaxRetries                   int `json:"max_retries" default:"3" name:"最大重试次数" category:"密钥配置" desc:"单个请求使用不同 Key 的最大重试次数，0为不重试。" validate:"required,min=0"`
	BlacklistThreshold           int `json:"blacklist_threshold" default:"3" name:"黑名单阈值" category:"密钥配置" desc:"一个 Key 连续失败多少次后进入黑名单，0为不拉黑。" validate:"required,min=0"`
	KeyValidationIntervalMinutes int `json:"key_validation_interval_minutes" default:"60" name:"密钥验证间隔（分钟）" category:"密钥配置" desc:"后台验证密钥的默认间隔（分钟）。" validate:"required,min=1"`
	KeyValidationConcurrency     int `json:"key_validation_concurrency" default:"10" name:"密钥验证并发数" category:"密钥配置" desc:"后台定时验证无效 Key 时的并发数，如果使用SQLite或者运行环境性能不佳，请尽量保证20以下，避免过高的并发导致数据不一致问题。" validate:"required,min=1"`
	KeyValidationTimeoutSeconds  int `json:"key_validation_timeout_seconds" default:"20" name:"密钥验证超时（秒）" category:"密钥配置" desc:"后台定时验证单个 Key 时的 API 请求超时时间（秒）。" validate:"required,min=1"`

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
