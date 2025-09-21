# GPT-Load

[English](README.md) | [中文](README_CN.md) | 日本語

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
![Go Version](https://img.shields.io/badge/Go-1.23+-blue.svg)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

複数のAIサービスを統合する必要がある企業や開発者向けに特別に設計された、高性能でエンタープライズグレードのAI APIトランスペアレントプロキシサービス。Goで構築され、インテリジェントなキー管理、ロードバランシング、包括的な監視機能を備え、高並行性の本番環境向けに設計されています。

詳細なドキュメントについては、[公式ドキュメント](https://www.gpt-load.com/docs?lang=ja)をご覧ください。

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp%2Fgpt-load | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" style="width: 250px; height: 54px;" width="250" height="54" /></a>

## 特徴

- **トランスペアレントプロキシ**: ネイティブAPIフォーマットの完全な保持、OpenAI、Google Gemini、Anthropic Claudeなどのフォーマットをサポート
- **インテリジェントキー管理**: グループベース管理、自動ローテーション、障害復旧を備えた高性能キープール
- **ロードバランシング**: サービスの可用性を向上させる複数のアップストリームエンドポイント間の重み付けロードバランシング
- **スマート障害処理**: サービスの継続性を確保する自動キーブラックリスト管理と復旧メカニズム
- **動的設定**: システム設定とグループ設定は再起動不要のホットリロードをサポート
- **エンタープライズアーキテクチャ**: 水平スケーリングと高可用性をサポートする分散リーダー・フォロワーデプロイメント
- **モダンな管理**: Vue 3ベースの直感的で使いやすいWeb管理インターフェース
- **包括的な監視**: リアルタイム統計、ヘルスチェック、詳細なリクエストログ
- **高性能設計**: ゼロコピーストリーミング、接続プール再利用、アトミック操作
- **本番対応**: グレースフルシャットダウン、エラー復旧、包括的なセキュリティメカニズム
- **デュアル認証**: 管理とプロキシの分離認証、プロキシ認証はグローバルおよびグループレベルのキーをサポート

## サポートされているAIサービス

GPT-Loadは、さまざまなAIサービスプロバイダーのネイティブAPIフォーマットを完全に保持するトランスペアレントプロキシサービスとして機能します：

- **OpenAIフォーマット**: 公式OpenAI API、Azure OpenAI、その他のOpenAI互換サービス
- **Google Geminiフォーマット**: Gemini Pro、Gemini Pro VisionなどのモデルのネイティブAPI
- **Anthropic Claudeフォーマット**: Claudeシリーズモデル、高品質な会話とテキスト生成をサポート

## クイックスタート

### システム要件

- Go 1.23+（ソースビルド用）
- Docker（コンテナ化デプロイメント用）
- MySQL、PostgreSQL、またはSQLite（データベースストレージ用）
- Redis（キャッシュと分散調整用、オプション）

### 方法1: Dockerクイックスタート

```bash
docker run -d --name gpt-load \
    -p 3001:3001 \
    -e AUTH_KEY=your-secure-key-here \
    -v "$(pwd)/data":/app/data \
    ghcr.io/tbphp/gpt-load:latest
```

> `your-secure-key-here`を強力なパスワードに変更してください（デフォルト値は絶対に使用しないでください）。その後、管理インターフェースにログインできます：<http://localhost:3001>

### 方法2: Docker Composeを使用（推奨）

**インストールコマンド：**

```bash
# ディレクトリを作成
mkdir -p gpt-load && cd gpt-load

# 設定ファイルをダウンロード
wget https://raw.githubusercontent.com/tbphp/gpt-load/refs/heads/main/docker-compose.yml
wget -O .env https://raw.githubusercontent.com/tbphp/gpt-load/refs/heads/main/.env.example

# .envファイルを編集し、AUTH_KEYを強力なパスワードに変更します。デフォルトやsk-123456のような単純なキーは絶対に使用しないでください。

# サービスを開始
docker compose up -d
```

デプロイメント前に、デフォルトの管理キー（AUTH_KEY）を必ず変更してください。推奨フォーマット：sk-prod-[32文字のランダム文字列]。

デフォルトのインストールは、軽量な単一インスタンスアプリケーションに適したSQLiteバージョンを使用します。

MySQL、PostgreSQL、Redisをインストールする必要がある場合は、`docker-compose.yml`ファイルで必要なサービスのコメントを解除し、対応する環境変数を設定して、再起動してください。

**その他のコマンド：**

```bash
# サービスステータスを確認
docker compose ps

# ログを表示
docker compose logs -f

# サービスを再起動
docker compose down && docker compose up -d

# 最新バージョンに更新
docker compose pull && docker compose down && docker compose up -d
```

デプロイメント後：

- Web管理インターフェースにアクセス：<http://localhost:3001>
- APIプロキシアドレス：<http://localhost:3001/proxy>

> 変更したAUTH_KEYを使用して管理インターフェースにログインしてください。

### 方法3: ソースビルド

ソースビルドには、ローカルにインストールされたデータベース（SQLite、MySQL、またはPostgreSQL）とRedis（オプション）が必要です。

```bash
# クローンとビルド
git clone https://github.com/tbphp/gpt-load.git
cd gpt-load
go mod tidy

# 設定を作成
cp .env.example .env

# .envファイルを編集し、AUTH_KEYを強力なパスワードに変更します。デフォルトやsk-123456のような単純なキーは絶対に使用しないでください。
# .envでDATABASE_DSNとREDIS_DSNの設定を変更
# REDIS_DSNはオプションです。設定されていない場合、メモリストレージが有効になります

# 実行
make run
```

デプロイメント後：

- Web管理インターフェースにアクセス：<http://localhost:3001>
- APIプロキシアドレス：<http://localhost:3001/proxy>

> 変更したAUTH_KEYを使用して管理インターフェースにログインしてください。

### 方法4: クラスターデプロイメント

クラスターデプロイメントでは、すべてのノードが同じMySQL（またはPostgreSQL）とRedisに接続する必要があり、Redisは必須です。統一された分散MySQLとRedisクラスターの使用を推奨します。

**デプロイメント要件：**

- すべてのノードは同一の`AUTH_KEY`、`DATABASE_DSN`、`REDIS_DSN`を設定する必要があります
- リーダー・フォロワーアーキテクチャで、フォロワーノードは環境変数を設定する必要があります：`IS_SLAVE=true`

詳細については、[クラスターデプロイメントドキュメント](https://www.gpt-load.com/docs/cluster?lang=ja)を参照してください。

## 設定システム

### 設定アーキテクチャの概要

GPT-Loadは二層設定アーキテクチャを採用しています：

#### 1. 静的設定（環境変数）

- **特性**: アプリケーション起動時に読み込まれ、実行時は不変、有効にするにはアプリケーションの再起動が必要
- **用途**: データベース接続、サーバーポート、認証キーなどのインフラストラクチャ設定
- **管理**: `.env`ファイルまたはシステム環境変数を介して設定

#### 2. 動的設定（ホットリロード）

- **システム設定**: データベースに保存され、アプリケーション全体に統一された動作標準を提供
- **グループ設定**: 特定のグループ用にカスタマイズされた動作パラメータ、システム設定を上書き可能
- **設定優先度**: グループ設定 > システム設定 > 環境設定
- **特性**: ホットリロードをサポート、変更後はアプリケーションの再起動なしで即座に有効

<details>
<summary>静的設定（環境変数）</summary>

**サーバー設定：**

| 設定                     | 環境変数                           | デフォルト      | 説明                                       |
| ----------------------- | ---------------------------------- | -------------- | ----------------------------------------- |
| サービスポート           | `PORT`                             | 3001           | HTTPサーバーリスニングポート                |
| サービスアドレス         | `HOST`                             | 0.0.0.0        | HTTPサーバーバインディングアドレス           |
| 読み取りタイムアウト     | `SERVER_READ_TIMEOUT`              | 60             | HTTPサーバー読み取りタイムアウト（秒）       |
| 書き込みタイムアウト     | `SERVER_WRITE_TIMEOUT`             | 600            | HTTPサーバー書き込みタイムアウト（秒）       |
| アイドルタイムアウト     | `SERVER_IDLE_TIMEOUT`              | 120            | HTTP接続アイドルタイムアウト（秒）          |
| グレースフルシャットダウンタイムアウト | `SERVER_GRACEFUL_SHUTDOWN_TIMEOUT` | 10   | サービスグレースフルシャットダウン待機時間（秒）|
| フォロワーモード         | `IS_SLAVE`                         | false          | クラスターデプロイメント用フォロワーノード識別子|
| タイムゾーン            | `TZ`                               | `Asia/Shanghai` | タイムゾーンを指定                          |

**セキュリティ設定：**

| 設定        | 環境変数            | デフォルト | 説明                                                                              |
| ---------- | ------------------- | --------- | -------------------------------------------------------------------------------- |
| 管理キー    | `AUTH_KEY`          | -         | **管理端末**のアクセス認証キー、強力なパスワードに変更してください                    |
| 暗号化キー  | `ENCRYPTION_KEY`    | -         | APIキーを保存時に暗号化。任意の文字列をサポート、空の場合は暗号化を無効化。[データ暗号化移行](#データ暗号化移行)を参照 |

**データベース設定：**

| 設定               | 環境変数         | デフォルト            | 説明                                    |
| ----------------- | ---------------- | -------------------- | --------------------------------------- |
| データベース接続   | `DATABASE_DSN`   | `./data/gpt-load.db` | データベース接続文字列（DSN）またはファイルパス |
| Redis接続         | `REDIS_DSN`      | -                    | Redis接続文字列、空の場合はメモリストレージを使用 |

**パフォーマンス＆CORS設定：**

| 設定                   | 環境変数                  | デフォルト                     | 説明                                    |
| --------------------- | ------------------------- | ----------------------------- | --------------------------------------- |
| 最大同時リクエスト数    | `MAX_CONCURRENT_REQUESTS` | 100                          | システムが許可する最大同時リクエスト数      |
| CORS有効化            | `ENABLE_CORS`             | false                         | クロスオリジンリソース共有を有効にするか    |
| 許可されたオリジン     | `ALLOWED_ORIGINS`         | -                            | 許可されたオリジン、カンマ区切り           |
| 許可されたメソッド     | `ALLOWED_METHODS`         | `GET,POST,PUT,DELETE,OPTIONS` | 許可されたHTTPメソッド                   |
| 許可されたヘッダー     | `ALLOWED_HEADERS`         | `*`                          | 許可されたリクエストヘッダー、カンマ区切り   |
| 認証情報の許可        | `ALLOW_CREDENTIALS`       | false                        | 認証情報の送信を許可するか                |

**ログ設定：**

| 設定                | 環境変数          | デフォルト             | 説明                              |
| ------------------ | ----------------- | --------------------- | --------------------------------- |
| ログレベル          | `LOG_LEVEL`       | `info`                | ログレベル：debug, info, warn, error |
| ログフォーマット    | `LOG_FORMAT`      | `text`                | ログフォーマット：text, json        |
| ファイルログ有効化   | `LOG_ENABLE_FILE` | false                 | ファイルログ出力を有効にするか        |
| ログファイルパス    | `LOG_FILE_PATH`   | `./data/logs/app.log` | ログファイル保存パス                 |

**プロキシ設定：**

GPT-Loadは、アップストリームAIプロバイダーへのリクエストを行うために環境変数からプロキシ設定を自動的に読み取ります。

| 設定        | 環境変数       | デフォルト | 説明                                         |
| ---------- | -------------- | --------- | -------------------------------------------- |
| HTTPプロキシ | `HTTP_PROXY`  | -         | HTTPリクエスト用のプロキシサーバーアドレス       |
| HTTPSプロキシ| `HTTPS_PROXY` | -         | HTTPSリクエスト用のプロキシサーバーアドレス      |
| プロキシなし | `NO_PROXY`    | -         | プロキシをバイパスするホストまたはドメインのカンマ区切りリスト |

サポートされているプロキシプロトコルフォーマット：

- **HTTP**: `http://user:pass@host:port`
- **HTTPS**: `https://user:pass@host:port`
- **SOCKS5**: `socks5://user:pass@host:port`
</details>

<details>
<summary>動的設定（ホットリロード）</summary>

**基本設定：**

| 設定                | フィールド名                        | デフォルト              | グループ上書き | 説明                                    |
| ------------------ | ---------------------------------- | ---------------------- | ------------ | --------------------------------------- |
| プロジェクトURL     | `app_url`                          | `http://localhost:3001` | ❌           | プロジェクトベースURL                     |
| グローバルプロキシキー | `proxy_keys`                      | `AUTH_KEY`の初期値       | ❌           | グローバルに有効なプロキシキー、カンマ区切り |
| ログ保持日数        | `request_log_retention_days`       | 7                      | ❌           | リクエストログ保持日数、0でクリーンアップなし |
| ログ書き込み間隔    | `request_log_write_interval_minutes` | 1                    | ❌           | データベースへのログ書き込みサイクル（分）   |
| リクエストボディログ有効化 | `enable_request_body_logging` | false                 | ✅           | リクエストログに完全なリクエストボディコンテンツを記録するか |

**リクエスト設定：**

| 設定                        | フィールド名               | デフォルト | グループ上書き | 説明                                                      |
| -------------------------- | ------------------------- | --------- | ------------ | --------------------------------------------------------- |
| リクエストタイムアウト       | `request_timeout`         | 600       | ✅           | 転送リクエストの完全なライフサイクルタイムアウト（秒）          |
| 接続タイムアウト            | `connect_timeout`         | 15        | ✅           | アップストリームサービスとの接続確立のタイムアウト（秒）        |
| アイドル接続タイムアウト     | `idle_conn_timeout`       | 120       | ✅           | HTTPクライアントアイドル接続タイムアウト（秒）                |
| レスポンスヘッダータイムアウト | `response_header_timeout` | 600      | ✅           | アップストリームレスポンスヘッダーの待機タイムアウト（秒）      |
| 最大アイドル接続数          | `max_idle_conns`          | 100       | ✅           | 接続プールの最大総アイドル接続数                             |
| ホストごとの最大アイドル接続数 | `max_idle_conns_per_host` | 50       | ✅           | アップストリームホストごとの最大アイドル接続数                |
| プロキシURL                | `proxy_url`               | -         | ✅           | 転送リクエスト用のHTTP/HTTPSプロキシ、空の場合は環境を使用    |

**キー設定：**

| 設定                    | フィールド名                        | デフォルト | グループ上書き | 説明                                                        |
| ---------------------- | ---------------------------------- | --------- | ------------ | ----------------------------------------------------------- |
| 最大リトライ回数        | `max_retries`                      | 3         | ✅           | 単一リクエストで異なるキーを使用する最大リトライ回数              |
| ブラックリストしきい値   | `blacklist_threshold`              | 3         | ✅           | キーがブラックリストに入る前の連続失敗回数                       |
| キー検証間隔            | `key_validation_interval_minutes`  | 60        | ✅           | バックグラウンドスケジュールキー検証サイクル（分）                |
| キー検証並行数          | `key_validation_concurrency`       | 10        | ✅           | 無効なキーのバックグラウンド検証の並行数                         |
| キー検証タイムアウト     | `key_validation_timeout_seconds`   | 20        | ✅           | バックグラウンドでの個別キー検証のAPIリクエストタイムアウト（秒）  |

</details>

## データ暗号化移行

GPT-LoadはAPIキーの暗号化保存をサポートしています。いつでも暗号化を有効化、無効化、または暗号化キーを変更できます。

<details>
<summary>データ暗号化移行の詳細を表示</summary>

### 移行シナリオ

- **暗号化を有効化**: プレーンテキストデータを暗号化して保存 - `--to <新しいキー>`を使用
- **暗号化を無効化**: 暗号化されたデータをプレーンテキストに復号化 - `--from <現在のキー>`を使用
- **暗号化キーを変更**: 暗号化キーを置き換える - `--from <現在のキー> --to <新しいキー>`を使用

### 操作手順

#### Docker Composeデプロイメント

```bash
# 1. イメージを更新（最新バージョンを使用していることを確認）
docker compose pull

# 2. サービスを停止
docker compose down

# 3. データベースをバックアップ（強く推奨）
# 移行前に、操作や例外によるキーの損失を避けるため、データベースを手動でバックアップするか、キーをエクスポートする必要があります。

# 4. 移行コマンドを実行
# 暗号化を有効化（your-32-char-secret-keyはあなたのキー、32文字以上のランダム文字列の使用を推奨）
docker compose run --rm gpt-load migrate-keys --to "your-32-char-secret-key"

# 暗号化を無効化
docker compose run --rm gpt-load migrate-keys --from "your-current-key"

# 暗号化キーを変更
docker compose run --rm gpt-load migrate-keys --from "old-key" --to "new-32-char-secret-key"

# 5. 設定ファイルを更新
# .envファイルを編集し、ENCRYPTION_KEYを--toパラメータと一致させる
# 暗号化を無効にする場合は、ENCRYPTION_KEYを削除するか空に設定
vim .env
# 追加または変更: ENCRYPTION_KEY=your-32-char-secret-key

# 6. サービスを再起動
docker compose up -d
```

#### ソースビルドデプロイメント

```bash
# 1. サービスを停止
# 実行中のサービスプロセスを停止（Ctrl+Cまたはプロセスをkill）

# 2. データベースをバックアップ（強く推奨）
# 移行前に、操作や例外によるキーの損失を避けるため、データベースを手動でバックアップするか、キーをエクスポートする必要があります。

# 3. 移行コマンドを実行
# 暗号化を有効化
make migrate-keys ARGS="--to your-32-char-secret-key"

# 暗号化を無効化
make migrate-keys ARGS="--from your-current-key"

# 暗号化キーを変更
make migrate-keys ARGS="--from old-key --to new-32-char-secret-key"

# 4. 設定ファイルを更新
# .envファイルを編集し、ENCRYPTION_KEYを--toパラメータと一致させる
echo "ENCRYPTION_KEY=your-32-char-secret-key" >> .env

# 5. サービスを再起動
make run
```

### 重要な注意事項

⚠️ **重要な注意事項**：
- **ENCRYPTION_KEYが失われると、暗号化されたデータは復元できません！** このキーを安全にバックアップしてください。パスワードマネージャーまたは安全なキー管理システムの使用を検討してください
- データの不整合を避けるため、移行前に**サービスを停止する必要があります**
- 移行が失敗して復旧が必要な場合に備えて、**データベースをバックアップ**することを強く推奨します
- セキュリティのため、キーは**32文字以上のランダム文字列**を使用してください
- 移行後、`.env`の`ENCRYPTION_KEY`が`--to`パラメータと一致していることを確認してください
- 暗号化を無効にする場合は、`ENCRYPTION_KEY`設定を削除またはクリアしてください

### キー生成の例

```bash
# 安全なランダムキーを生成（32文字）
openssl rand -base64 32 | tr -d "=+/" | cut -c1-32
```

</details>

## Web管理インターフェース

管理コンソールにアクセス：<http://localhost:3001>（デフォルトアドレス）

### インターフェースの概要

<img src="screenshot/dashboard.png" alt="ダッシュボード" width="600"/>

<br/>

<img src="screenshot/keys.png" alt="キー管理" width="600"/>

<br/>

Web管理インターフェースは以下の機能を提供します：

- **ダッシュボード**: リアルタイム統計とシステムステータスの概要
- **キー管理**: AIサービスプロバイダーグループの作成と設定、APIキーの追加、削除、監視
- **リクエストログ**: 詳細なリクエスト履歴とデバッグ情報
- **システム設定**: グローバル設定管理とホットリロード

## API使用ガイド

<details>
<summary>プロキシインターフェースの呼び出し</summary>

GPT-Loadはグループ名を通じてリクエストを異なるAIサービスにルーティングします。使用方法は以下の通りです：

### 1. プロキシエンドポイントフォーマット

```text
http://localhost:3001/proxy/{group_name}/{original_api_path}
```

- `{group_name}`: 管理インターフェースで作成されたグループ名
- `{original_api_path}`: 元のAIサービスパスと完全に一致を保つ

### 2. 認証方法

Web管理インターフェースで**プロキシキー**を設定します。システムレベルとグループレベルのプロキシキーをサポートします。

- **認証方法**: ネイティブAPIと一致しますが、元のキーを設定されたプロキシキーに置き換えます。
- **キーのスコープ**: システム設定で設定された**グローバルプロキシキー**はすべてのグループで使用できます。グループで設定された**グループプロキシキー**は現在のグループでのみ有効です。
- **フォーマット**: 複数のキーはカンマで区切られます。

### 3. OpenAIインターフェースの例

`openai`という名前のグループが作成されたと仮定：

**元の呼び出し：**

```bash
curl -X POST https://api.openai.com/v1/chat/completions \
  -H "Authorization: Bearer sk-your-openai-key" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4.1-mini", "messages": [{"role": "user", "content": "Hello"}]}'
```

**プロキシ呼び出し：**

```bash
curl -X POST http://localhost:3001/proxy/openai/v1/chat/completions \
  -H "Authorization: Bearer your-proxy-key" \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4.1-mini", "messages": [{"role": "user", "content": "Hello"}]}'
```

**必要な変更：**

- `https://api.openai.com`を`http://localhost:3001/proxy/openai`に置き換える
- 元のAPIキーを**プロキシキー**に置き換える

### 4. Geminiインターフェースの例

`gemini`という名前のグループが作成されたと仮定：

**元の呼び出し：**

```bash
curl -X POST https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:generateContent?key=your-gemini-key \
  -H "Content-Type: application/json" \
  -d '{"contents": [{"parts": [{"text": "Hello"}]}]}'
```

**プロキシ呼び出し：**

```bash
curl -X POST http://localhost:3001/proxy/gemini/v1beta/models/gemini-2.5-pro:generateContent?key=your-proxy-key \
  -H "Content-Type: application/json" \
  -d '{"contents": [{"parts": [{"text": "Hello"}]}]}'
```

**必要な変更：**

- `https://generativelanguage.googleapis.com`を`http://localhost:3001/proxy/gemini`に置き換える
- URLパラメータの`key=your-gemini-key`を**プロキシキー**に置き換える

### 5. Anthropicインターフェースの例

`anthropic`という名前のグループが作成されたと仮定：

**元の呼び出し：**

```bash
curl -X POST https://api.anthropic.com/v1/messages \
  -H "x-api-key: sk-ant-api03-your-anthropic-key" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model": "claude-sonnet-4-20250514", "messages": [{"role": "user", "content": "Hello"}]}'
```

**プロキシ呼び出し：**

```bash
curl -X POST http://localhost:3001/proxy/anthropic/v1/messages \
  -H "x-api-key: your-proxy-key" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model": "claude-sonnet-4-20250514", "messages": [{"role": "user", "content": "Hello"}]}'
```

**必要な変更：**

- `https://api.anthropic.com`を`http://localhost:3001/proxy/anthropic`に置き換える
- `x-api-key`ヘッダーの元のAPIキーを**プロキシキー**に置き換える

### 6. サポートされているインターフェース

**OpenAIフォーマット：**

- `/v1/chat/completions` - チャット会話
- `/v1/completions` - テキスト補完
- `/v1/embeddings` - テキスト埋め込み
- `/v1/models` - モデルリスト
- その他すべてのOpenAI互換インターフェース

**Geminiフォーマット：**

- `/v1beta/models/*/generateContent` - コンテンツ生成
- `/v1beta/models` - モデルリスト
- その他すべてのGeminiネイティブインターフェース

**Anthropicフォーマット：**

- `/v1/messages` - メッセージ会話
- `/v1/models` - モデルリスト（利用可能な場合）
- その他すべてのAnthropicネイティブインターフェース

### 7. クライアントSDK設定

**OpenAI Python SDK：**

```python
from openai import OpenAI

client = OpenAI(
    api_key="your-proxy-key",  # プロキシキーを使用
    base_url="http://localhost:3001/proxy/openai"  # プロキシエンドポイントを使用
)

response = client.chat.completions.create(
    model="gpt-4.1-mini",
    messages=[{"role": "user", "content": "Hello"}]
)
```

**Google Gemini SDK (Python)：**

```python
import google.generativeai as genai

# APIキーとベースURLを設定
genai.configure(
    api_key="your-proxy-key",  # プロキシキーを使用
    client_options={"api_endpoint": "http://localhost:3001/proxy/gemini"}
)

model = genai.GenerativeModel('gemini-2.5-pro')
response = model.generate_content("Hello")
```

**Anthropic SDK (Python)：**

```python
from anthropic import Anthropic

client = Anthropic(
    api_key="your-proxy-key",  # プロキシキーを使用
    base_url="http://localhost:3001/proxy/anthropic"  # プロキシエンドポイントを使用
)

response = client.messages.create(
    model="claude-sonnet-4-20250514",
    messages=[{"role": "user", "content": "Hello"}]
)
```

> **重要な注意**: トランスペアレントプロキシサービスとして、GPT-Loadはさまざまなアイサービスのネイティブ APIフォーマットと認証方法を完全に保持します。エンドポイントアドレスを置き換え、管理インターフェースで設定された**プロキシキー**を使用するだけで、シームレスな移行が可能です。

</details>

## 貢献

GPT-Loadに貢献してくださったすべての開発者に感謝します！

[![Contributors](https://contrib.rocks/image?repo=tbphp/gpt-load)](https://github.com/tbphp/gpt-load/graphs/contributors)

## ライセンス

MITライセンス - 詳細は[LICENSE](LICENSE)ファイルを参照してください。

## スター履歴

[![Stargazers over time](https://starchart.cc/tbphp/gpt-load.svg?variant=adaptive)](https://starchart.cc/tbphp/gpt-load)
