package locales

// MessagesJaJP contains Japanese control-plane translations.
var MessagesJaJP = map[string]string{
	"common.success":                                  "成功",
	"route.not_found":                                 "ルートが見つかりません",
	"route.method_not_allowed":                        "許可されていないHTTPメソッドです",
	"bad_request":                                     "不正なリクエスト",
	"request_too_large":                               "リクエストボディが大きすぎます",
	"bad_gateway":                                     "上流サービスエラー",
	"internal_error":                                  "内部エラー",
	"idempotency.required":                            "Idempotency-Key が必要です",
	"idempotency.invalid":                             "Idempotency-Key は正規 UUID v4 である必要があります",
	"idempotency.reused":                              "Idempotency-Key は別のリクエストで使用済みです",
	"idempotency.expired":                             "冪等結果の保持期間が終了しました",
	"control.operation_incomplete":                    "リソースは確定しましたが、実行時の復旧が未完了です",
	"control.recovery_pending":                        "以前に確定した操作を復旧中です",
	"settings.precondition_required":                  "If-Match が必要です",
	"settings.version_conflict":                       "読み込み後に設定が変更されました",
	"auth.invalid_key":                                "無効な認証キー",
	"auth.locked":                                     "認証試行回数が多すぎます。しばらくしてから再試行してください",
	"group.not_found":                                 "グループが存在しません",
	"group.name_exists":                               "グループ名が既に存在します",
	"group.in_use":                                    "グループはアクセスキーから参照されています",
	"group.upstream_url_conflict":                     "このアップストリームURLは既存のグループで使用されています",
	"group.upstream_url_change_confirmation_required": "アップストリームURLの変更には明示的な確認が必要です",
	"group.no_active_upstream_key":                    "このグループには利用可能な有効なアップストリームキーがありません",
	"key.not_found":                                   "キーが存在しません",
}
