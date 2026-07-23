package locales

// MessagesZhCN contains Simplified Chinese control-plane translations.
var MessagesZhCN = map[string]string{
	"common.success":               "操作成功",
	"bad_request":                  "请求错误",
	"request_too_large":            "请求体过大",
	"bad_gateway":                  "上游服务错误",
	"internal_error":               "内部错误",
	"auth.invalid_key":             "无效的授权密钥",
	"auth.locked":                  "认证尝试过多，请稍后重试",
	"group.not_found":              "分组不存在",
	"group.name_exists":            "分组名称已存在",
	"group.upstream_url_conflict":  "已有分组使用该上游地址",
	"group.no_active_upstream_key": "该分组没有可用的活跃上游密钥",
	"key.not_found":                "密钥不存在",
}
