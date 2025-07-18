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

// SystemSettings defines all system configuration options
type SystemSettings struct {
	// Basic parameters
	AppUrl                         string `json:"app_url" default:"http://localhost:3001" name:"Project URL" category:"Basic Parameters" desc:"Base URL of the project, used to concatenate group endpoint addresses. System config takes precedence over APP_URL environment variable."`
	RequestLogRetentionDays        int    `json:"request_log_retention_days" default:"7" name:"Log Retention Period (days)" category:"Basic Parameters" desc:"Number of days request logs are kept in the database, 0 means logs are not cleaned up." validate:"min=0"`
	RequestLogWriteIntervalMinutes int    `json:"request_log_write_interval_minutes" default:"1" name:"Log Write Delay Interval (minutes)" category:"Basic Parameters" desc:"Interval (in minutes) for writing request logs from cache to database, 0 means real-time writing." validate:"min=0"`

	// Request settings
	RequestTimeout        int `json:"request_timeout" default:"600" name:"Request Timeout (seconds)" category:"Request Settings" desc:"Complete lifecycle timeout for forwarded requests (seconds)." validate:"min=1"`
	ConnectTimeout        int `json:"connect_timeout" default:"15" name:"Connection Timeout (seconds)" category:"Request Settings" desc:"Timeout for establishing new connections to upstream services (seconds)." validate:"min=1"`
	IdleConnTimeout       int `json:"idle_conn_timeout" default:"120" name:"Idle Connection Timeout (seconds)" category:"Request Settings" desc:"Timeout for idle connections in HTTP client (seconds)." validate:"min=1"`
	ResponseHeaderTimeout int `json:"response_header_timeout" default:"600" name:"Response Header Timeout (seconds)" category:"Request Settings" desc:"Maximum time to wait for upstream service response headers (seconds)." validate:"min=1"`
	MaxIdleConns          int `json:"max_idle_conns" default:"100" name:"Max Idle Connections" category:"Request Settings" desc:"Maximum number of idle connections allowed in HTTP client pool." validate:"min=1"`
	MaxIdleConnsPerHost   int `json:"max_idle_conns_per_host" default:"50" name:"Max Idle Connections Per Host" category:"Request Settings" desc:"Maximum number of idle connections allowed per upstream host in HTTP client pool." validate:"min=1"`

	// Key configuration
	MaxRetries                   int `json:"max_retries" default:"3" name:"Max Retry Count" category:"Key Configuration" desc:"Maximum number of retries using different keys for a single request, 0 means no retry." validate:"min=0"`
	BlacklistThreshold           int `json:"blacklist_threshold" default:"3" name:"Blacklist Threshold" category:"Key Configuration" desc:"Number of consecutive failures after which a key is blacklisted, 0 means no blacklisting." validate:"min=0"`
	KeyValidationIntervalMinutes int `json:"key_validation_interval_minutes" default:"60" name:"Key Validation Interval (minutes)" category:"Key Configuration" desc:"Default interval (minutes) for background key validation." validate:"min=30"`
	KeyValidationConcurrency     int `json:"key_validation_concurrency" default:"10" name:"Key Validation Concurrency" category:"Key Configuration" desc:"Concurrency level for periodic validation of invalid keys in the background." validate:"min=1"`
	KeyValidationTimeoutSeconds  int `json:"key_validation_timeout_seconds" default:"20" name:"Key Validation Timeout (seconds)" category:"Key Configuration" desc:"API request timeout (seconds) when validating individual keys in the background." validate:"min=5"`
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
