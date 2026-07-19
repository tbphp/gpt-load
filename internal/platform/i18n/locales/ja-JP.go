package locales

// MessagesJaJP contains Japanese control-plane translations.
var MessagesJaJP = map[string]string{
	"common.success":               "成功",
	"bad_request":                  "不正なリクエスト",
	"bad_gateway":                  "上流サービスエラー",
	"internal_error":               "内部エラー",
	"auth.invalid_key":             "無効な認証キー",
	"group.not_found":              "グループが存在しません",
	"group.name_exists":            "グループ名が既に存在します",
	"group.upstream_url_conflict":  "このアップストリームURLは既存のグループで使用されています",
	"group.no_active_upstream_key": "このグループには利用可能な有効なアップストリームキーがありません",
	"key.not_found":                "キーが存在しません",
}
