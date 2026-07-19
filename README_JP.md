# GPT-Load

[English](README.md) | [中文](README_CN.md) | 日本語

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
![Go Version](https://img.shields.io/badge/Go-1.25-blue.svg)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

GPT-Loadは、上流AI APIキーを管理し、単一サービスからOpenAI、Anthropic、Geminiのネイティブエンドポイントを公開するGo製のセルフホスト型ゲートウェイです。

公開済みの1.4.xメンテナンスラインについては、[公式ドキュメント](https://www.gpt-load.com/docs?lang=ja)をご覧ください。

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp%2Fgpt-load | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" style="width: 250px; height: 54px;" width="250" height="54" /></a>

## スポンサー

<table>
<tbody>
<tr>
<td width="180"><a href="https://teamorouter.com/?utm_source=gpt_load&utm_medium=referral&utm_campaign=ai_directory"><img src="./screenshot/teamorouter.png" alt="TeamoRouter" width="150"></a></td>
<td>TeamoRouterによる本プロジェクトへのスポンサー支援に感謝します！TeamoRouterはエンタープライズグレードのAgentic LLM gatewayで、開発者、AIチーム、企業がClaude Code、Codex、Gemini CLI、その他のAI agentsに単一の統合APIからアクセスでき、個別のサブスクリプションは不要で、最大90%の割引を利用できます。OpenAI、Anthropic、Vertex、Azure、AWS Bedrockなどの公式プロバイダーおよび信頼できるパートナーに接続し、検証済みのAgent protocol互換性、リクエストのトレーサビリティ、公式に近いTTFT、99.6% SLA、最大5,000 QPMを提供します。集中請求、チーム管理、BYOK、smart routing、analytics、provider optimization、専属サポートも備えています。Teamo Desktopにより、API key管理や手動設定なしでワンクリックセットアップが可能で、新規ユーザーは<a href="https://teamorouter.com/?utm_source=gpt_load&utm_medium=referral&utm_campaign=ai_directory">こちらのリンク</a>から登録すると初回チャージが10%オフになります。</td>
</tr>
<tr>
<td width="180"><a href="https://unity2.ai/register?source=gptload"><img src="./screenshot/unity2ai.jpg" alt="Unity2.ai" width="150"></a></td>
<td>Unity2.aiによる本プロジェクトへのスポンサー支援に感謝します！Unity2.aiは、個人開発者、チーム、企業向けの高性能AIモデルAPI中継プラットフォームです。中国国内の大手企業に長期的にサービスを提供しており、1日あたり300億token超の呼び出しを処理し、5000 RPM級の高並行性をサポートします。残高課金、初回チャージ特典、組み合わせサブスクリプション、企業向け請求書発行、専属連携サポートに対応しています。<a href="https://unity2.ai/register?source=gptload">こちらのリンク</a>から登録すると$2の残高を受け取れ、公式グループ参加でさらに$10の残高、最大$12の無料枠を受け取れます。</td>
</tr>
<tr>
<td width="180"><a href="https://linux.do"><img src="./screenshot/l.png" alt="LINUX DO" width="150"></a></td>
<td>LINUX DOコミュニティからのサポートに心より感謝いたします！</td>
</tr>
<tr>
<td width="180"><a href="https://www.digitalocean.com/?refcode=3d52cff21342&utm_campaign=Referral_Invite&utm_medium=Referral_Program&utm_source=badge"><img src="https://web-platforms.sfo2.cdn.digitaloceanspaces.com/WWW/Badge%202.svg" alt="DigitalOcean Referral Badge" width="150"></a></td>
<td>このプロジェクトはDigitalOceanの支援を受けています。</td>
</tr>
</tbody>
</table>

## 開発状況

> [!WARNING]
> 2.0は未リリースです。`v2`は開発中のグリーンフィールド再構築ブランチです。メンテナンス中の1.4.xリリースラインには`main`ブランチを使用してください。

M1はバックエンドのみのマイルストーンとして完了しています。管理フロントエンドは同梱も提供もされず、M3で再構築されます。

## 現在のM1範囲

- AccessKey認証を備えたOpenAI、Anthropic、Geminiのネイティブデータプレーンルート。
- SQLiteに保存されるGroup、暗号化された上流キー、AccessKey、および再読み込み可能なランタイムスナップショット。
- 現在の管理APIによるGroupの一覧/作成、既存Groupへのキーインポート、2種類のモデル検出操作、およびAccessKey CRUD。
- 明示的なマスターキーがない場合のローカル暗号化keyfileの自動生成。

後続範囲は明確に延期されています。M2でスケジューリングとヘルス動作を完成させ、M3でコントロールプレーンを拡張して管理UIを再構築し、M4で使用量とコストの集計を追加します。これらの機能はM1には含まれません。

## アーキテクチャと実行時の制限

- M1はGoバックエンドのみを提供し、データプレーントラフィックを`/api`管理プレーンから分離します。
- 2.0.0はSQLiteのみをサポートし、単一アプリケーションインスタンスの正しさのみを保証します。
- `DATA_DIR`がデフォルトのSQLiteデータベースと生成keyfileを管理します。`DATABASE_DSN`と`ENCRYPTION_KEY`はそれぞれのデフォルトを明示的に上書きします。
- 上流シークレットは保存時に暗号化され、平文へのフォールバックはありません。

## ビルドと実行

Go 1.25が必要です。

```bash
cp .env.example .env
# 起動前に.envのAUTH_KEYを設定します。
go build -o gpt-load .
./gpt-load
```

race detectorを有効にして開発する場合：

```bash
make dev
```

## 環境変数

| 変数 | デフォルト | 用途 |
|---|---|---|
| `HOST` | `0.0.0.0` | HTTPリッスンアドレス |
| `PORT` | `3001` | HTTPリッスンポート |
| `AUTH_KEY` | 必須 | 管理APIのbearer token。空文字や空白文字を含む値は不可 |
| `DATA_DIR` | `./data` | デフォルトDBと生成される`encryption.key`を管理 |
| `DATABASE_DSN` | `${DATA_DIR}/gpt-load.db` | 設定時にSQLiteパス/DSNを明示的に上書き |
| `ENCRYPTION_KEY` | keyfileを自動生成 | 設定時にマスターキーを明示的に上書き |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | グレースフルシャットダウンの秒数 |
| `READ_TIMEOUT` | `60` | リクエスト全体を読み取る最大秒数 |
| `IDLE_TIMEOUT` | `120` | keep-aliveのアイドルタイムアウト秒数 |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Docker Composeの停止猶予 |
| `LOG_LEVEL` | `info` | アプリケーションログレベル |
| `LOG_FORMAT` | `text` | ログ形式：`text`または`json` |

## データプレーンルート

データプレーンリクエストはAccessKeyを使用します。プロバイダー互換の認証情報は、必要に応じて`Authorization: Bearer`、`x-api-key`、`x-goog-api-key`、またはGeminiの`key`クエリパラメータで渡せます。

| メソッド | パス | プロトコル / 動作 |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions |
| `GET` | `/v1/models` | OpenAIモデル一覧。`anthropic-version`ヘッダーがある場合はAnthropicモデル一覧形式 |
| `POST` | `/v1/messages` | Anthropic Messages |
| `GET` | `/v1beta/models` | Geminiモデル一覧 |
| `POST` | `/v1beta/models/{model}:generateContent` | Geminiコンテンツ生成 |
| `POST` | `/v1beta/models/{model}:streamGenerateContent` | Geminiストリーミングコンテンツ生成 |

GroupはURLパスセグメントではなく、AccessKeyとランタイム設定によって選択されます。

## 管理API

すべての管理ルートで`Authorization: Bearer <AUTH_KEY>`が必要です。

| メソッド | パス | 用途 |
|---|---|---|
| `GET` | `/api/groups` | Group一覧 |
| `POST` | `/api/groups` | Group作成 |
| `POST` | `/api/groups/{group_id}/keys/import` | 既存Groupへのキーインポート |
| `POST` | `/api/groups/{group_id}/models/discover` | 既存Group経由のモデル検出 |
| `POST` | `/api/models/discover` | 明示的な上流設定によるモデル検出 |
| `POST` | `/api/access-keys` | AccessKey作成 |
| `GET` | `/api/access-keys` | AccessKey一覧 |
| `PUT` | `/api/access-keys/{id}` | AccessKey更新 |
| `DELETE` | `/api/access-keys/{id}` | AccessKey削除 |

管理プレーンの失敗レスポンスは`{ "code": string, "message": string, "data"?: any }`です。任意の`data`フィールドは、クライアントが次の操作を決定するために構造化情報を必要とする場合にのみ含まれます。

## Docker Compose

Composeファイルはデフォルトで公開済みイメージを使用します。未リリースの`v2` checkoutを実行するには、最初にローカル`build`ブロックのコメントを解除してから、次を実行します。

```bash
cp .env.example .env
# 起動前に.envのAUTH_KEYを設定します。
docker compose up -d --build
docker compose logs -f gpt-load
```

アップグレードや暗号化キーの変更前に、SQLiteデータベースと`${DATA_DIR}/encryption.key`を一緒にバックアップしてください。`DATABASE_DSN`または`ENCRYPTION_KEY`を設定した場合は、対応する明示的な値を代わりにバックアップします。

## テスト

```bash
go test -race . ./internal/...
go test ./internal/somepkg -run '^TestName$' -v
```

## ライセンスとセキュリティ

GPT-Loadは[MIT License](LICENSE)で公開されています。脆弱性は[SECURITY.md](SECURITY.md)の手順に従って報告してください。
