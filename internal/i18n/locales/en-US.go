package locales

// Messages English (US) translations
var MessagesEnUS = map[string]string{
	// Common messages
	"success":        "Operation successful",
	"common.success": "Success",
	"error":          "Operation failed",
	"unauthorized":   "Unauthorized",
	"forbidden":      "Forbidden",
	"not_found":      "Not found",
	"bad_request":    "Bad request",
	"internal_error": "Internal error",
	"invalid_param":  "Invalid parameter",
	"required_field": "Required field",

	// Authentication related
	"auth.invalid_key":    "Invalid authorization key",
	"auth.key_required":   "Authorization key required",
	"auth.login_success":  "Login successful",
	"auth.logout_success": "Logout successful",

	// Group related
	"group.created":     "Group created successfully",
	"group.updated":     "Group updated successfully",
	"group.deleted":     "Group deleted successfully",
	"group.not_found":   "Group not found",
	"group.name_exists": "Group name already exists",

	// Key related
	"key.created":         "Key created successfully",
	"key.updated":         "Key updated successfully",
	"key.deleted":         "Key deleted successfully",
	"key.not_found":       "Key not found",
	"key.invalid":         "Invalid key",
	"key.check_started":   "Key check started",
	"key.check_completed": "Key check completed",

	// Settings related
	"settings.updated": "Settings updated successfully",
	"settings.reset":   "Settings reset",

	// Logs related
	"logs.cleared":  "Logs cleared",
	"logs.exported": "Logs exported successfully",

	// Validation related
	"validation.invalid_group_name":      "Invalid group name. Can only contain lowercase letters, numbers, hyphens or underscores, 1-100 characters",
	"validation.invalid_test_path":       "Invalid test path. If provided, must be a valid path starting with / and not a full URL.",
	"validation.duplicate_header":        "Duplicate header: {{.key}}",
	"validation.group_not_found":         "Group not found",
	"validation.invalid_status_filter":   "Invalid status filter",
	"validation.invalid_group_id":        "Invalid group ID format",
	"validation.test_model_required":     "Test model is required",
	"validation.invalid_copy_keys_value": "Invalid copy_keys value. Must be 'none', 'valid_only', or 'all'",
	"validation.invalid_channel_type":    "Invalid channel type. Supported types: {{.types}}",
	"validation.test_model_empty":        "Test model cannot be empty or contain only spaces",
	"validation.invalid_status_value":    "Invalid status value",
	"validation.invalid_upstreams":       "Invalid upstreams configuration: {{.error}}",
	"validation.group_id_required":       "group_id query parameter is required",
	"validation.invalid_group_id_format": "Invalid group_id format",
	"validation.keys_text_empty":         "Keys text cannot be empty",
	"validation.invalid_group_type":      "Invalid group type, must be 'standard' or 'aggregate'",
	"validation.sub_groups_required":     "Aggregate group must contain at least one sub-group",
	"validation.invalid_sub_group_id":    "Invalid sub-group ID",
	"validation.sub_group_not_found":     "One or more sub-groups not found",
	"validation.sub_group_cannot_be_aggregate": "Sub-groups cannot be aggregate groups",
	"validation.sub_group_channel_mismatch": "All sub-groups must use the same channel type",
	"validation.sub_group_validation_endpoint_mismatch": "All sub-groups must use the same validation endpoint",
	"validation.sub_group_weight_negative": "Sub-group weight cannot be negative",
	"validation.standard_group_requires_upstreams_testmodel": "Converting to standard group requires providing upstreams and test model",

	// Task related
	"task.validation_started": "Key validation task started",
	"task.import_started":     "Key import task started",
	"task.delete_started":     "Key deletion task started",
	"task.already_running":    "A task is already running",
	"task.get_status_failed":  "Failed to get task status",

	// Dashboard related
	"dashboard.invalid_keys":                                     "Invalid Keys",
	"dashboard.success_requests":                                 "Success",
	"dashboard.failed_requests":                                  "Failed",
	"dashboard.auth_key_missing":                                 "AUTH_KEY is not set, system cannot function properly",
	"dashboard.auth_key_required":                                "AUTH_KEY must be set to protect the admin interface",
	"dashboard.encryption_key_missing":                           "ENCRYPTION_KEY is not set, sensitive data will be stored in plain text",
	"dashboard.encryption_key_recommended":                       "It is strongly recommended to set ENCRYPTION_KEY to encrypt sensitive data like API keys",
	"dashboard.global_proxy_key":                                 "Global Proxy Key",
	"dashboard.group_proxy_key":                                  "Group Proxy Key",
	"dashboard.encryption_key_configured_but_data_not_encrypted": "ENCRYPTION_KEY is configured but the keys in database are not encrypted. This will cause keys to be unreadable (shown as failed-to-decrypt).",
	"dashboard.encryption_key_migration_required":                "Please stop the service, run the key migration command and restart",
	"dashboard.data_encrypted_but_key_not_configured":            "Keys in database are encrypted but ENCRYPTION_KEY is not configured. This will cause keys to be unreadable.",
	"dashboard.configure_same_encryption_key":                    "Please configure the same ENCRYPTION_KEY used for encryption, or run decryption migration",
	"dashboard.encryption_key_mismatch":                          "The configured ENCRYPTION_KEY does not match the key used for encryption. This will cause decryption to fail (shown as failed-to-decrypt).",
	"dashboard.use_correct_encryption_key":                       "Please use the correct ENCRYPTION_KEY, or run key migration",

	// Database related
	"database.cannot_get_groups":     "Cannot get groups list",
	"database.rpm_stats_failed":      "Failed to get RPM statistics",
	"database.current_stats_failed":  "Failed to get current period statistics",
	"database.previous_stats_failed": "Failed to get previous period statistics",
	"database.chart_data_failed":     "Failed to get chart data",
	"database.group_stats_failed":    "Failed to get partial statistics",

	// Success messages
	"success.group_deleted":        "Group and related keys deleted successfully",
	"success.keys_restored":        "{{.count}} keys restored",
	"success.invalid_keys_cleared": "{{.count}} invalid keys cleared",
	"success.all_keys_cleared":     "{{.count}} keys cleared",

	// Password security related
	"security.password_too_short":         "{{.keyType}} is too short ({{.length}} characters), recommend at least 16 characters",
	"security.password_short":             "{{.keyType}} is short ({{.length}} characters), recommend 32+ characters",
	"security.password_weak_pattern":      "{{.keyType}} contains common weak password patterns: {{.pattern}}",
	"security.password_low_complexity":    "{{.keyType}} has low complexity, missing combination of upper/lowercase, numbers, or special characters",
	"security.password_recommendation_16": "Use a strong password with at least 16 characters, 32+ recommended",
	"security.password_recommendation_32": "Recommend using passwords with 32+ characters for better security",
	"security.password_avoid_common":      "Avoid common words, suggest using randomly generated strong passwords",
	"security.password_complexity":        "Suggest including upper/lowercase letters, numbers and special characters to improve password strength",

	// Config related
	"config.updated":                          "Configuration updated successfully",
	"config.app_url":                          "Application URL",
	"config.app_url_desc":                     "Base URL of the application, used for constructing group endpoint addresses. System config takes precedence over APP_URL environment variable.",
	"config.proxy_keys":                       "Global Proxy Keys",
	"config.proxy_keys_desc":                  "Global proxy keys for accessing all group proxy endpoints. Separate multiple keys with commas.",
	"config.log_retention_days":               "Log Retention Days",
	"config.log_retention_days_desc":          "Number of days to retain request logs in database, 0 to keep logs forever.",
	"config.log_write_interval":               "Log Write Interval (minutes)",
	"config.log_write_interval_desc":          "Interval (in minutes) for writing request logs from cache to database, 0 for real-time writes.",
	"config.enable_request_body_logging":      "Enable Request Body Logging",
	"config.enable_request_body_logging_desc": "Whether to log complete request body content. Enabling this will increase memory and storage usage.",

	// Request settings related
	"config.request_timeout":              "Request Timeout (seconds)",
	"config.request_timeout_desc":         "Complete lifecycle timeout (seconds) for forwarded requests.",
	"config.connect_timeout":              "Connect Timeout (seconds)",
	"config.connect_timeout_desc":         "Timeout (seconds) for establishing new connections to upstream services.",
	"config.idle_conn_timeout":            "Idle Connection Timeout (seconds)",
	"config.idle_conn_timeout_desc":       "Timeout (seconds) for idle connections in the HTTP client.",
	"config.response_header_timeout":      "Response Header Timeout (seconds)",
	"config.response_header_timeout_desc": "Maximum time (seconds) to wait for response headers from upstream services.",
	"config.max_idle_conns":               "Max Idle Connections",
	"config.max_idle_conns_desc":          "Maximum number of idle connections allowed in the HTTP client connection pool.",
	"config.max_idle_conns_per_host":      "Max Idle Connections Per Host",
	"config.max_idle_conns_per_host_desc": "Maximum number of idle connections allowed per upstream host in the HTTP client connection pool.",
	"config.proxy_url":                    "Proxy Server URL",
	"config.proxy_url_desc":               "Global HTTP/HTTPS proxy server URL, e.g., http://user:pass@host:port. If empty, uses environment variable configuration.",

	// Key config related
	"config.max_retries":                     "Max Retries",
	"config.max_retries_desc":                "Maximum number of retries for a single request using different keys, 0 for no retries.",
	"config.blacklist_threshold":             "Blacklist Threshold",
	"config.blacklist_threshold_desc":        "Number of consecutive failures before a key is blacklisted, 0 to disable blacklisting.",
	"config.key_validation_interval":         "Key Validation Interval (minutes)",
	"config.key_validation_interval_desc":    "Default interval (minutes) for background key validation.",
	"config.key_validation_concurrency":      "Key Validation Concurrency",
	"config.key_validation_concurrency_desc": "Concurrency level for background invalid key validation. Keep below 20 for SQLite or low-performance environments to avoid data consistency issues.",
	"config.key_validation_timeout":          "Key Validation Timeout (seconds)",
	"config.key_validation_timeout_desc":     "API request timeout (seconds) when validating a single key in the background.",

	// Category labels
	"config.category.basic":   "Basic",
	"config.category.request": "Request Settings",
	"config.category.key":     "Key Configuration",

	// Internal error messages (for fmt.Errorf usage)
	"error.upstreams_required":       "upstreams field is required",
	"error.invalid_upstreams_format": "invalid upstreams format",
	"error.at_least_one_upstream":    "at least one upstream is required",
	"error.upstream_url_empty":       "upstream URL cannot be empty",
	"error.upstream_weight_positive": "upstream weight must be a positive integer",
	"error.marshal_upstreams_failed": "failed to marshal cleaned upstreams",
	"error.invalid_config_format":    "Invalid config format: {{.error}}",
	"error.process_header_rules":     "Failed to process header rules: {{.error}}",
	"error.invalidate_group_cache":   "failed to invalidate group cache",
	"error.unmarshal_header_rules":   "Failed to unmarshal header rules",
	"error.delete_group_cache":       "Failed to delete group: unable to clean up cache",
	"error.decrypt_key_copy":         "Failed to decrypt key during group copy, skipping",
	"error.start_import_task":        "Failed to start async key import task for group copy",
	"error.export_logs":              "Failed to export logs",

	// Login related
	"auth.invalid_request":           "Invalid request format",
	"auth.authentication_successful": "Authentication successful",
	"auth.authentication_failed":     "Authentication failed",

	// Settings success message
	"settings.update_success": "Settings updated successfully. Configuration will be reloaded in the background across all instances.",
}
