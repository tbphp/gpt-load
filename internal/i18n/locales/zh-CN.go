package locales

// Messages Chinese (Simplified) translations
var MessagesZhCN = map[string]string{
	// Common messages
	"success":        "操作成功",
	"common.success": "操作成功",
	"error":          "操作失败",
	"unauthorized":   "未授权",
	"forbidden":      "禁止访问",
	"not_found":      "未找到",
	"bad_request":    "请求错误",
	"internal_error": "内部错误",
	"invalid_param":  "参数无效",
	"required_field": "必填字段",

	// Authentication related
	"auth.invalid_key":    "无效的授权密钥",
	"auth.key_required":   "需要授权密钥",
	"auth.login_success":  "登录成功",
	"auth.logout_success": "退出成功",

	// Group related
	"group.created":     "分组创建成功",
	"group.updated":     "分组更新成功",
	"group.deleted":     "分组删除成功",
	"group.not_found":   "分组不存在",
	"group.name_exists": "分组名称已存在",

	// Key related
	"key.created":         "密钥创建成功",
	"key.updated":         "密钥更新成功",
	"key.deleted":         "密钥删除成功",
	"key.not_found":       "密钥不存在",
	"key.invalid":         "密钥无效",
	"key.check_started":   "密钥检查已开始",
	"key.check_completed": "密钥检查完成",

	// Settings related
	"settings.updated": "设置更新成功",
	"settings.reset":   "设置已重置",

	// Logs related
	"logs.cleared":  "日志已清除",
	"logs.exported": "日志导出成功",

	// Validation related
	"validation.invalid_group_name":      "无效的分组名称。只能包含小写字母、数字、中划线或下划线，长度1-100位",
	"validation.invalid_test_path":       "无效的测试路径。如果提供，必须是以 / 开头的有效路径，且不能是完整的URL。",
	"validation.duplicate_header":        "重复的请求头: {{.key}}",
	"validation.group_not_found":         "分组不存在",
	"validation.invalid_status_filter":   "无效的状态过滤器",
	"validation.invalid_group_id":        "无效的分组ID格式",
	"validation.test_model_required":     "测试模型是必需的",
	"validation.invalid_copy_keys_value": "无效的copy_keys值。必须是'none'、'valid_only'或'all'",
	"validation.invalid_channel_type":    "无效的通道类型。支持的类型有: {{.types}}",
	"validation.test_model_empty":        "测试模型不能为空或只有空格",
	"validation.invalid_status_value":    "无效的状态值",
	"validation.invalid_upstreams":       "upstreams配置错误: {{.error}}",
	"validation.group_id_required":       "需要提供group_id参数",
	"validation.invalid_group_id_format": "无效的group_id格式",
	"validation.keys_text_empty":         "密钥文本不能为空",
	"validation.invalid_group_type":      "无效的分组类型，必须为'standard'或'aggregate'",
	"validation.sub_groups_required":     "聚合分组必须包含至少一个子分组",
	"validation.invalid_sub_group_id":    "无效的子分组ID",
	"validation.sub_group_not_found":     "一个或多个子分组不存在",
	"validation.sub_group_cannot_be_aggregate": "子分组不能是聚合分组",
	"validation.sub_group_channel_mismatch": "所有子分组必须使用相同的渠道类型",
	"validation.sub_group_validation_endpoint_mismatch": "所有子分组必须使用相同的验证端点",
	"validation.sub_group_weight_negative":     "子分组权重不能为负数",
	"validation.sub_group_weight_max_exceeded": "子分组权重不能超过1000",
	"validation.standard_group_requires_upstreams_testmodel": "转换为标准分组需要提供上游服务器和测试模型",

	// Task related
	"task.validation_started": "密钥验证任务已开始",
	"task.import_started":     "密钥导入任务已开始",
	"task.delete_started":     "密钥删除任务已开始",
	"task.already_running":    "已有任务正在运行",
	"task.get_status_failed":  "获取任务状态失败",

	// Dashboard related
	"dashboard.invalid_keys":                                     "无效密钥数量",
	"dashboard.success_requests":                                 "成功请求",
	"dashboard.failed_requests":                                  "失败请求",
	"dashboard.auth_key_missing":                                 "AUTH_KEY未设置，系统无法正常工作",
	"dashboard.auth_key_required":                                "必须设置AUTH_KEY以保护管理界面",
	"dashboard.encryption_key_missing":                           "未设置ENCRYPTION_KEY，敏感数据将明文存储",
	"dashboard.encryption_key_recommended":                       "强烈建议设置ENCRYPTION_KEY以加密保护API密钥等敏感数据",
	"dashboard.global_proxy_key":                                 "全局代理密钥",
	"dashboard.group_proxy_key":                                  "分组代理密钥",
	"dashboard.encryption_key_configured_but_data_not_encrypted": "检测到您已配置 ENCRYPTION_KEY，但数据库中的密钥尚未加密。这会导致密钥无法正常读取（显示为 failed-to-decrypt）。",
	"dashboard.encryption_key_migration_required":                "请停止服务，执行密钥迁移命令后重启",
	"dashboard.data_encrypted_but_key_not_configured":            "检测到数据库中的密钥已加密，但未配置 ENCRYPTION_KEY。这会导致密钥无法正常读取。",
	"dashboard.configure_same_encryption_key":                    "请配置与加密时相同的 ENCRYPTION_KEY，或执行解密迁移",
	"dashboard.encryption_key_mismatch":                          "检测到您配置的 ENCRYPTION_KEY 与数据加密时使用的密钥不匹配。这会导致密钥解密失败（显示为 failed-to-decrypt）。",
	"dashboard.use_correct_encryption_key":                       "请使用正确的 ENCRYPTION_KEY，或执行密钥迁移",

	// Database related
	"database.cannot_get_groups":     "无法获取分组列表",
	"database.rpm_stats_failed":      "获取RPM统计失败",
	"database.current_stats_failed":  "获取当前期间统计失败",
	"database.previous_stats_failed": "获取上一期间统计失败",
	"database.chart_data_failed":     "获取图表数据失败",
	"database.group_stats_failed":    "获取部分统计信息失败",

	// Success messages
	"success.group_deleted":        "分组及相关密钥删除成功",
	"success.keys_restored":        "{{.count}}个密钥已恢复",
	"success.invalid_keys_cleared": "{{.count}}个无效密钥已清除",
	"success.all_keys_cleared":     "{{.count}}个密钥已清除",

	// Password security related
	"security.password_too_short":         "{{.keyType}}长度不足（{{.length}}字符），建议至少16字符",
	"security.password_short":             "{{.keyType}}长度偏短（{{.length}}字符），建议32字符以上",
	"security.password_weak_pattern":      "{{.keyType}}包含常见弱密码模式：{{.pattern}}",
	"security.password_low_complexity":    "{{.keyType}}复杂度不足，缺少大小写字母、数字或特殊字符的组合",
	"security.password_recommendation_16": "使用至少16个字符的强密码，推荐32字符以上",
	"security.password_recommendation_32": "推荐使用32个字符以上的密码以提高安全性",
	"security.password_avoid_common":      "避免使用常见单词，建议使用随机生成的强密码",
	"security.password_complexity":        "建议包含大小写字母、数字和特殊字符以提高密码强度",

	// Config related
	"config.updated":                          "配置更新成功",
	"config.app_url":                          "项目地址",
	"config.app_url_desc":                     "项目的基础 URL，用于拼接分组终端节点地址。系统配置优先于环境变量 APP_URL。",
	"config.proxy_keys":                       "全局代理密钥",
	"config.proxy_keys_desc":                  "全局代理密钥，用于访问所有分组的代理端点。多个密钥请用逗号分隔。",
	"config.log_retention_days":               "日志保留时长（天）",
	"config.log_retention_days_desc":          "请求日志在数据库中的保留天数，0为不清理日志。",
	"config.log_write_interval":               "日志延迟写入周期（分钟）",
	"config.log_write_interval_desc":          "请求日志从缓存写入数据库的周期（分钟），0为实时写入数据。",
	"config.enable_request_body_logging":      "启用日志详情",
	"config.enable_request_body_logging_desc": "是否在请求日志中记录完整的请求体内容。启用此功能会增加内存以及存储空间的占用。",

	// Request settings related
	"config.request_timeout":              "请求超时（秒）",
	"config.request_timeout_desc":         "转发请求的完整生命周期超时（秒）等。",
	"config.connect_timeout":              "连接超时（秒）",
	"config.connect_timeout_desc":         "与上游服务建立新连接的超时时间（秒）。",
	"config.idle_conn_timeout":            "空闲连接超时（秒）",
	"config.idle_conn_timeout_desc":       "HTTP 客户端中空闲连接的超时时间（秒）。",
	"config.response_header_timeout":      "响应头超时（秒）",
	"config.response_header_timeout_desc": "等待上游服务响应头的最长时间（秒）。",
	"config.max_idle_conns":               "最大空闲连接数",
	"config.max_idle_conns_desc":          "HTTP 客户端连接池中允许的最大空闲连接总数。",
	"config.max_idle_conns_per_host":      "每主机最大空闲连接数",
	"config.max_idle_conns_per_host_desc": "HTTP 客户端连接池对每个上游主机允许的最大空闲连接数。",
	"config.proxy_url":                    "代理服务器地址",
	"config.proxy_url_desc":               "全局 HTTP/HTTPS 代理服务器地址，例如：http://user:pass@host:port。如果为空，则使用环境变量配置。",

	// Key config related
	"config.max_retries":                     "最大重试次数",
	"config.max_retries_desc":                "单个请求使用不同 Key 的最大重试次数，0为不重试。",
	"config.blacklist_threshold":             "黑名单阈值",
	"config.blacklist_threshold_desc":        "一个 Key 连续失败多少次后进入黑名单，0为不拉黑。",
	"config.key_validation_interval":         "密钥验证间隔（分钟）",
	"config.key_validation_interval_desc":    "后台验证密钥的默认间隔（分钟）。",
	"config.key_validation_concurrency":      "密钥验证并发数",
	"config.key_validation_concurrency_desc": "后台定时验证无效 Key 时的并发数，如果使用SQLite或者运行环境性能不佳，请尽量保证20以下，避免过高的并发导致数据不一致问题。",
	"config.key_validation_timeout":          "密钥验证超时（秒）",
	"config.key_validation_timeout_desc":     "后台定时验证单个 Key 时的 API 请求超时时间（秒）。",

	// Category labels
	"config.category.basic":   "基础参数",
	"config.category.request": "请求设置",
	"config.category.key":     "密钥配置",

	// Internal error messages (for fmt.Errorf usage)
	"error.upstreams_required":       "upstreams字段是必需的",
	"error.invalid_upstreams_format": "upstreams格式无效",
	"error.at_least_one_upstream":    "至少需要一个upstream",
	"error.upstream_url_empty":       "upstream URL不能为空",
	"error.upstream_weight_positive": "upstream权重必须是正整数",
	"error.marshal_upstreams_failed": "序列化清理后的upstreams失败",
	"error.invalid_config_format":    "无效的配置格式: {{.error}}",
	"error.process_header_rules":     "处理请求头规则失败: {{.error}}",
	"error.invalidate_group_cache":   "刷新分组缓存失败",
	"error.unmarshal_header_rules":   "解析请求头规则失败",
	"error.delete_group_cache":       "删除分组失败: 无法清理缓存",
	"error.decrypt_key_copy":         "解密密钥时失败，跳过该密钥",
	"error.start_import_task":        "启动异步密钥导入任务失败",
	"error.export_logs":              "导出日志失败",

	// Login related
	"auth.invalid_request":           "无效的请求格式",
	"auth.authentication_successful": "认证成功",
	"auth.authentication_failed":     "认证失败",

	// Settings success message
	"settings.update_success": "设置更新成功。配置将在后台在所有实例间重新加载。",

	// Sub-groups related
	"success.sub_groups_added":         "子分组添加成功",
	"success.sub_group_weight_updated": "子分组权重更新成功",
	"success.sub_group_deleted":        "子分组删除成功",
	"group.not_aggregate":              "该分组不是聚合分组",
	"group.sub_group_already_exists":   "子分组{{.sub_group_id}}已存在",
	"group.sub_group_not_found":        "子分组不存在",
}
