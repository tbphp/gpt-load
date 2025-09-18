package locales

// Messages Japanese translations
var MessagesJaJP = map[string]string{
	// Common messages
	"success":        "操作成功",
	"common.success": "成功",
	"error":          "操作失敗",
	"unauthorized":   "未認証",
	"forbidden":      "アクセス拒否",
	"not_found":      "見つかりません",
	"bad_request":    "不正なリクエスト",
	"internal_error": "内部エラー",
	"invalid_param":  "無効なパラメータ",
	"required_field": "必須フィールド",

	// Authentication related
	"auth.invalid_key":    "無効な認証キー",
	"auth.key_required":   "認証キーが必要です",
	"auth.login_success":  "ログイン成功",
	"auth.logout_success": "ログアウト成功",

	// Group related
	"group.created":     "グループが作成されました",
	"group.updated":     "グループが更新されました",
	"group.deleted":     "グループが削除されました",
	"group.not_found":   "グループが存在しません",
	"group.name_exists": "グループ名が既に存在します",

	// Key related
	"key.created":         "キーが作成されました",
	"key.updated":         "キーが更新されました",
	"key.deleted":         "キーが削除されました",
	"key.not_found":       "キーが存在しません",
	"key.invalid":         "無効なキー",
	"key.check_started":   "キーチェックが開始されました",
	"key.check_completed": "キーチェックが完了しました",

	// Settings related
	"settings.updated": "設定が更新されました",
	"settings.reset":   "設定がリセットされました",

	// Logs related
	"logs.cleared":  "ログがクリアされました",
	"logs.exported": "ログがエクスポートされました",

	// Validation related
	"validation.invalid_group_name":      "無効なグループ名。小文字、数字、ハイフン、アンダースコアのみ使用可能、1-100文字",
	"validation.invalid_test_path":       "無効なテストパス。指定する場合は / で始まる有効なパスであり、完全なURLではない必要があります。",
	"validation.duplicate_header":        "重複ヘッダー: {{.key}}",
	"validation.group_not_found":         "グループが見つかりません",
	"validation.invalid_status_filter":   "無効なステータスフィルター",
	"validation.invalid_group_id":        "無効なグループID形式",
	"validation.test_model_required":     "テストモデルが必要です",
	"validation.invalid_copy_keys_value": "無効なcopy_keys値。'none'、'valid_only'、'all'のいずれかである必要があります",
	"validation.invalid_channel_type":    "無効なチャンネルタイプ。サポートされるタイプ: {{.types}}",
	"validation.test_model_empty":        "テストモデルは空またはスペースのみにできません",
	"validation.invalid_status_value":    "無効なステータス値",
	"validation.invalid_upstreams":       "無効なupstreams設定: {{.error}}",
	"validation.group_id_required":       "group_idクエリパラメータが必要です",
	"validation.invalid_group_id_format": "無効なgroup_id形式",
	"validation.keys_text_empty":         "キーテキストは空にできません",
	"validation.invalid_group_type":      "無効なグループタイプ、'standard'または'aggregate'である必要があります",
	"validation.sub_groups_required":     "集約グループには少なくとも1つのサブグループが必要です",
	"validation.invalid_sub_group_id":    "無効なサブグループID",
	"validation.sub_group_not_found":     "1つ以上のサブグループが見つかりません",
	"validation.sub_group_cannot_be_aggregate": "サブグループは集約グループにできません",
	"validation.sub_group_channel_mismatch": "すべてのサブグループは同じチャンネルタイプを使用する必要があります",
	"validation.sub_group_validation_endpoint_mismatch": "すべてのサブグループは同じ検証エンドポイントを使用する必要があります",
	"validation.sub_group_weight_negative":     "サブグループの重みは負の値にできません",
	"validation.sub_group_weight_max_exceeded": "サブグループの重みは1000を超えることはできません",
	"validation.standard_group_requires_upstreams_testmodel": "標準グループへの変換にはアップストリームサーバーとテストモデルの提供が必要です",

	// Task related
	"task.validation_started": "キー検証タスクが開始されました",
	"task.import_started":     "キーインポートタスクが開始されました",
	"task.delete_started":     "キー削除タスクが開始されました",
	"task.already_running":    "タスクが既に実行中です",
	"task.get_status_failed":  "タスクステータスの取得に失敗しました",

	// Dashboard related
	"dashboard.invalid_keys":                                     "無効なキー",
	"dashboard.success_requests":                                 "成功",
	"dashboard.failed_requests":                                  "失敗",
	"dashboard.auth_key_missing":                                 "AUTH_KEYが設定されていません。システムが正常に動作しません",
	"dashboard.auth_key_required":                                "管理インターフェースを保護するためAUTH_KEYを設定する必要があります",
	"dashboard.encryption_key_missing":                           "ENCRYPTION_KEYが設定されていません。機密データがプレーンテキストで保存されます",
	"dashboard.encryption_key_recommended":                       "APIキーなどの機密データを暗号化するため、ENCRYPTION_KEYの設定を強く推奨します",
	"dashboard.global_proxy_key":                                 "グローバルプロキシキー",
	"dashboard.group_proxy_key":                                  "グループプロキシキー",
	"dashboard.encryption_key_configured_but_data_not_encrypted": "ENCRYPTION_KEYは設定されていますが、データベース内のキーは暗号化されていません。これによりキーが読み取り不能になります（failed-to-decryptと表示）。",
	"dashboard.encryption_key_migration_required":                "サービスを停止し、キー移行コマンドを実行してから再起動してください",
	"dashboard.data_encrypted_but_key_not_configured":            "データベース内のキーは暗号化されていますが、ENCRYPTION_KEYが設定されていません。これによりキーが読み取り不能になります。",
	"dashboard.configure_same_encryption_key":                    "暗号化時に使用した同じENCRYPTION_KEYを設定するか、復号化移行を実行してください",
	"dashboard.encryption_key_mismatch":                          "設定されたENCRYPTION_KEYが暗号化時に使用されたキーと一致しません。これにより復号化が失敗します（failed-to-decryptと表示）。",
	"dashboard.use_correct_encryption_key":                       "正しいENCRYPTION_KEYを使用するか、キー移行を実行してください",

	// Database related
	"database.cannot_get_groups":     "グループリストを取得できません",
	"database.rpm_stats_failed":      "RPM統計の取得に失敗しました",
	"database.current_stats_failed":  "現在の期間統計の取得に失敗しました",
	"database.previous_stats_failed": "前の期間統計の取得に失敗しました",
	"database.chart_data_failed":     "チャートデータの取得に失敗しました",
	"database.group_stats_failed":    "部分統計の取得に失敗しました",

	// Success messages
	"success.group_deleted":        "グループと関連キーが正常に削除されました",
	"success.keys_restored":        "{{.count}}個のキーが復元されました",
	"success.invalid_keys_cleared": "{{.count}}個の無効なキーがクリアされました",
	"success.all_keys_cleared":     "{{.count}}個のキーがクリアされました",

	// Password security related
	"security.password_too_short":         "{{.keyType}}が短すぎます（{{.length}}文字）。少なくとも16文字を推奨します",
	"security.password_short":             "{{.keyType}}が短いです（{{.length}}文字）。32文字以上を推奨します",
	"security.password_weak_pattern":      "{{.keyType}}に一般的な弱いパスワードパターンが含まれています: {{.pattern}}",
	"security.password_low_complexity":    "{{.keyType}}の複雑性が低く、大文字/小文字、数字、特殊文字の組み合わせが不足しています",
	"security.password_recommendation_16": "少なくとも16文字の強力なパスワードを使用してください。32文字以上を推奨します",
	"security.password_recommendation_32": "セキュリティ向上のため32文字以上のパスワードを推奨します",
	"security.password_avoid_common":      "一般的な単語は避け、ランダムに生成された強力なパスワードの使用を推奨します",
	"security.password_complexity":        "パスワード強度を向上させるため、大文字/小文字、数字、特殊文字を含めることを推奨します",

	// Config related
	"config.updated":                          "設定が正常に更新されました",
	"config.app_url":                          "アプリケーションURL",
	"config.app_url_desc":                     "アプリケーションのベースURL。グループエンドポイントアドレスの構築に使用されます。システム設定が環境変数APP_URLより優先されます。",
	"config.proxy_keys":                       "グローバルプロキシキー",
	"config.proxy_keys_desc":                  "すべてのグループプロキシエンドポイントにアクセスするためのグローバルプロキシキー。複数のキーはカンマで区切ります。",
	"config.log_retention_days":               "ログ保存期間（日）",
	"config.log_retention_days_desc":          "データベースにリクエストログを保持する日数、0でログを永久保存。",
	"config.log_write_interval":               "ログ書き込み間隔（分）",
	"config.log_write_interval_desc":          "リクエストログをキャッシュからデータベースに書き込む間隔（分）、0でリアルタイム書き込み。",
	"config.enable_request_body_logging":      "リクエストボディログを有効化",
	"config.enable_request_body_logging_desc": "完全なリクエストボディの内容をログに記録するかどうか。有効にするとメモリとストレージの使用量が増加します。",

	// Request settings related
	"config.request_timeout":              "リクエストタイムアウト（秒）",
	"config.request_timeout_desc":         "転送リクエストの完全なライフサイクルタイムアウト（秒）。",
	"config.connect_timeout":              "接続タイムアウト（秒）",
	"config.connect_timeout_desc":         "上流サービスへの新しい接続を確立するためのタイムアウト（秒）。",
	"config.idle_conn_timeout":            "アイドル接続タイムアウト（秒）",
	"config.idle_conn_timeout_desc":       "HTTPクライアントのアイドル接続のタイムアウト（秒）。",
	"config.response_header_timeout":      "レスポンスヘッダータイムアウト（秒）",
	"config.response_header_timeout_desc": "上流サービスからのレスポンスヘッダーを待つ最大時間（秒）。",
	"config.max_idle_conns":               "最大アイドル接続数",
	"config.max_idle_conns_desc":          "HTTPクライアント接続プールで許可される最大アイドル接続総数。",
	"config.max_idle_conns_per_host":      "ホストごとの最大アイドル接続数",
	"config.max_idle_conns_per_host_desc": "HTTPクライアント接続プールで各上流ホストに許可される最大アイドル接続数。",
	"config.proxy_url":                    "プロキシサーバーURL",
	"config.proxy_url_desc":               "グローバルHTTP/HTTPSプロキシサーバーURL。例：http://user:pass@host:port。空の場合は環境変数設定を使用。",

	// Key config related
	"config.max_retries":                     "最大リトライ数",
	"config.max_retries_desc":                "異なるキーを使用した単一リクエストの最大リトライ数、0でリトライなし。",
	"config.blacklist_threshold":             "ブラックリストしきい値",
	"config.blacklist_threshold_desc":        "キーがブラックリストに入るまでの連続失敗回数、0でブラックリスト無効。",
	"config.key_validation_interval":         "キー検証間隔（分）",
	"config.key_validation_interval_desc":    "バックグラウンドキー検証のデフォルト間隔（分）。",
	"config.key_validation_concurrency":      "キー検証並行数",
	"config.key_validation_concurrency_desc": "バックグラウンドで無効なキーを検証する際の並行数。SQLiteや低性能環境では20以下を維持し、データ不整合を回避してください。",
	"config.key_validation_timeout":          "キー検証タイムアウト（秒）",
	"config.key_validation_timeout_desc":     "バックグラウンドで単一キーを検証する際のAPIリクエストタイムアウト（秒）。",

	// Category labels
	"config.category.basic":   "基本設定",
	"config.category.request": "リクエスト設定",
	"config.category.key":     "キー設定",

	// Internal error messages (for fmt.Errorf usage)
	"error.upstreams_required":       "upstreamsフィールドは必須です",
	"error.invalid_upstreams_format": "無効なupstreams形式",
	"error.at_least_one_upstream":    "少なくとも1つのupstreamが必要です",
	"error.upstream_url_empty":       "upstream URLは空にできません",
	"error.upstream_weight_positive": "upstreamの重みは正の整数である必要があります",
	"error.marshal_upstreams_failed": "クリーンアップされたupstreamsのシリアル化に失敗しました",
	"error.invalid_config_format":    "無効な設定形式: {{.error}}",
	"error.process_header_rules":     "ヘッダールールの処理に失敗しました: {{.error}}",
	"error.invalidate_group_cache":   "グループキャッシュの無効化に失敗しました",
	"error.unmarshal_header_rules":   "ヘッダールールのアンマーシャルに失敗しました",
	"error.delete_group_cache":       "グループの削除に失敗: キャッシュをクリーンアップできません",
	"error.decrypt_key_copy":         "グループコピー中のキー復号化に失敗、スキップします",
	"error.start_import_task":        "グループコピー用の非同期キーインポートタスクの開始に失敗しました",
	"error.export_logs":              "ログのエクスポートに失敗しました",

	// Login related
	"auth.invalid_request":           "無効なリクエスト形式",
	"auth.authentication_successful": "認証成功",
	"auth.authentication_failed":     "認証失敗",

	// Settings success message
	"settings.update_success": "設定が正常に更新されました。設定はすべてのインスタンスでバックグラウンドで再読み込みされます。",

	// Sub-groups related
	"success.sub_groups_added":         "サブグループが正常に追加されました",
	"success.sub_group_weight_updated": "サブグループの重みが正常に更新されました",
	"success.sub_group_deleted":        "サブグループが正常に削除されました",
	"group.not_aggregate":              "グループはアグリゲートグループではありません",
	"group.sub_group_already_exists":   "サブグループ{{.sub_group_id}}は既に存在します",
	"group.sub_group_not_found":        "サブグループが見つかりません",
}
