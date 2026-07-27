# GPT-Load

[English](README.md) | [中文](README_CN.md) | 日本語

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
![Go Version](https://img.shields.io/badge/Go-1.25-blue.svg)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

GPT-Loadは、Goで構築されたセルフホスト型のAI APIキー集約・ネイティブプロトコルゲートウェイです。管理UIを内蔵した単一バイナリでOpenAI、Anthropic、Geminiおよび互換上流のキーを管理し、各プロバイダーのネイティブなデータプレーンエンドポイントを公開します。

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

## 2.0のリリース状況

> [!WARNING]
> 2.0は現在release-readyの最終段階にあります。ただし、`v2.0.0`タグ、GitHub Release、バイナリ、コンテナイメージが公開済みという意味ではありません。デプロイ前に実在するrelease artifactを確認し、リポジトリのブランチ状態をリリース成功の証拠として扱わないでください。

2.0は1.xとデータ互換性のないgreenfield rewriteです。`main`は引き続き1.4.xメンテナンスラインを提供します。2.0は`latest`を自動更新せず、安定したコンテナチャネルには明示的な`2`、`2.0`、`v2.0.0`タグを使用します。

## 2.0の機能

- **2つのプレーン**：データプレーンはプロバイダーのネイティブパスを維持し、管理APIは`/api`に統一されます。管理UIは同じGoバイナリに内蔵されます。
- **3つのネイティブ方言**：OpenAI、Anthropic、Geminiのリクエストをそれぞれのプロトコルで転送し、プロトコル間の変換は行いません。
- **キーとトラフィックの管理**：Group、暗号化された上流Key、AccessKey、モデル検出、フィルターとレート制限、スケジューリング、ヘルス状態、cooldown、blacklist、自動重み付け。
- **制御と可観測性**：ランタイム設定、ルート検査、ヘルス表示、RequestLog、中国語・英語・日本語の管理UI。
- **使用量と推定コスト**：3方言から取得可能なusage、24時間/30日レポート、リクエスト単位の品質状態、組み込み価格、ユーザー価格の上書き。

価格とコストは、上流から返されたusageと現在の価格ルールに基づくbest-effortの**推定値**です。billing ledger、請求書、プロバイダー請求ではなく、過去のリクエストを再計算することもありません。

## 2.0.0のサポート境界

- 正しさを保証するのは**単一アプリケーションインスタンス**のみで、複数インスタンスの協調には対応しません。
- **SQLiteのみ**をサポートし、PostgreSQL、MySQL、その他のデータベースには対応しません。
- GroupはAccessKeyとランタイム設定で選択され、データプレーンURLには含まれません。
- 上流キーは必ず保存時に暗号化され、平文へのフォールバックはありません。2.0.0はマスターキーのローテーションに対応せず、`migrate-keys`は明示的に失敗する延期コマンドのままです。
- 1.xデータの自動移行、インプレースアップグレード、逆同期には対応しません。
- プロトコル変換、オンライン請求照合、自動価格取得、オンラインバックアップAPI、バックアップCLIは提供しません。

## クイックスタート

### Docker Compose

2.xのComposeリリース契約では`ghcr.io/tbphp/gpt-load:2`、コンテナ内パス`/app/data`、named volume `gpt-load-data`を使用し、`latest`は使用しません。まず現在のcheckoutを確認します。

```console
cp .env.example .env
docker compose config
```

解決後の設定でimageが`ghcr.io/tbphp/gpt-load:2`、`DATA_DIR=/app/data`、`DATABASE_DSN=/app/data/gpt-load.db`となり、`/app/data`にnamed volumeがマウントされる場合だけ続行してください。`latest`またはhost bind mountに解決される場合、そのComposeファイルを2.0の本番デプロイに使用せず、`latest`で代用しないでください。

前提条件を満たした後：

```console
docker compose up -d
curl --fail http://localhost:3001/health
# 初回起動でAUTH_KEYが生成された場合、安全な端末で一度だけ読み取り、
# 直ちにsecret managerへ保存します。
docker compose exec gpt-load sh -c 'cat /app/data/auth.key'
```

デフォルトのnamed volumeにはSQLite、`auth.key`、`encryption.key`が保持されます。本番環境では、保護されたsecret処理を通じて明示的な`AUTH_KEY`と`ENCRYPTION_KEY`を注入してください。実際のsecretを`.env`、ログ、issueへコミットしないでください。コンテナで`DATABASE_DSN`を変更する場合、**コンテナ内**パスと対応するvolume mountをCompose overrideで同時に設定する必要があります。

### ネイティブバイナリ

公開後、GitHub Releaseからプラットフォームに合うartifactをダウンロードし、`SHA256SUMS`で検証します。release artifactが実際に存在するまでは、「ビルドと検証」に従って現在のcheckoutからビルドし、既に公開済みと仮定しないでください。

Linux amd64の例：

```console
chmod +x ./gpt-load-linux-amd64
mkdir -p ./data
DATA_DIR=./data ./gpt-load-linux-amd64
```

別の端末で確認します。

```console
curl --fail http://localhost:3001/health
```

ブラウザーで<http://localhost:3001>を開きます。

`AUTH_KEY`と`ENCRYPTION_KEY`はどちらも明示的に設定できます。空の場合、初回起動時にそれぞれ`${DATA_DIR}/auth.key`と`${DATA_DIR}/encryption.key`を作成し、その後も再利用します。アプリケーションは生成したファイルのパスだけをログに記録し、secretの内容は記録しません。

## ネイティブデータプレーン

データプレーンリクエストはAccessKeyを使用します。プロバイダー互換の認証情報は、必要に応じて`Authorization: Bearer`、`x-api-key`、`x-goog-api-key`、またはGeminiの`key`クエリパラメータで渡せます。

| プロバイダー | メソッドとパス | 動作 |
|---|---|---|
| OpenAI | `POST /v1/chat/completions` | ネイティブOpenAI Chat Completionsリクエスト |
| OpenAI / Anthropic | `GET /v1/models` | デフォルトはOpenAI形式、`anthropic-version`がある場合はAnthropic形式 |
| Anthropic | `POST /v1/messages` | ネイティブAnthropic Messagesリクエスト |
| Gemini | `GET /v1beta/models` | ネイティブGeminiモデル一覧 |
| Gemini | `POST /v1beta/models/{model}:generateContent` | Gemini非ストリーミング生成 |
| Gemini | `POST /v1beta/models/{model}:streamGenerateContent` | Geminiストリーミング生成 |

GPT-Loadは方言間の変換を行いません。GroupはAccessKeyとランタイム設定で選択され、URLパスセグメントとして渡しません。

## 管理、使用量、コスト

管理UIは`/`、管理APIは`/api`で提供され、どちらも`AUTH_KEY`を使用します。UIにはGroup、上流キー、AccessKey、ランタイム設定、ヘルス、ログ、ルート検査、Usage、モデル価格管理があります。管理APIの事実源は現在のコードとUIであり、このREADMEでは変化しやすいルート一覧を複製しません。

Usage/Costの品質境界：

- `complete + priced`のリクエストだけが、デフォルトのtoken合計と推定コスト合計に入ります。
- `missing`、`partial`、`unpriced`もリクエスト数と品質カウントには入りますが、デフォルトのtoken/コスト合計には入りません。`complete + unpriced`に推測価格を割り当てることもありません。
- ストリームのclean EOFは完全なusageを保証せず、互換中継サービスがプロバイダー公式の終端usageを返さない場合もあります。
- 価格変更は今後の書き込みにだけ影響し、過去のRequestLogやUsageStatは再計算されません。
- 現在のプロセスにおけるdropped/write-failureカウンターと、データベース期間内の永続集計は異なる範囲です。

## 主要設定

| 変数 | デフォルト | 用途 |
|---|---|---|
| `HOST` | `0.0.0.0` | HTTPリッスンアドレス |
| `PORT` | `3001` | HTTPリッスンポート |
| `DATA_DIR` | `./data` | ネイティブプロセスの永続ディレクトリ。コンテナのリリース契約では`/app/data`に固定 |
| `DATABASE_DSN` | `${DATA_DIR}/gpt-load.db` | SQLiteパス/DSN。コンテナパスはコンテナ名前空間に存在し、対応するvolumeが必要 |
| `AUTH_KEY` | keyfileを自動生成 | 管理bearer認証情報。明示値に空白は使用不可。空の場合`${DATA_DIR}/auth.key`を読み取りまたは作成 |
| `ENCRYPTION_KEY` | keyfileを自動生成 | 上流キー暗号化用マスターキー。空の場合`${DATA_DIR}/encryption.key`を読み取りまたは作成 |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | グレースフルシャットダウンの秒数 |
| `READ_TIMEOUT` | `60` | リクエスト全体を読み取る最大秒数 |
| `IDLE_TIMEOUT` | `120` | keep-aliveのアイドルタイムアウト秒数 |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Composeの停止猶予。アプリケーションのシャットダウン時間より長く設定 |
| `LOG_LEVEL` | `info` | アプリケーションログレベル |
| `LOG_FORMAT` | `text` | ログ形式：`text`または`json` |

プロセス設定の全項目は[`.env.example`](.env.example)を参照してください。接続、最初のバイト、リクエスト、ストリームアイドルの各タイムアウトとRequestLog保持期間は管理UI/APIで扱うランタイム設定であり、追加の環境変数ではありません。

## 永続化とセキュリティ

- デフォルトでは`${DATA_DIR}`にSQLite、`auth.key`、`encryption.key`が含まれます。これらを一組の復旧資産として保護し、バックアップしてください。
- `encryption.key`を失う、または置換すると、暗号化済みの上流キーを復号できません。2.0.0には自動修復やマスターキーのローテーションがありません。
- 外部`DATABASE_DSN`または明示管理するsecretは別途バックアップが必要です。DATA_DIRのバックアップだけでは外部資産を保護できません。
- SQLiteはWALを使用します。バックアップ前に新規トラフィックを止め、`SIGTERM`を送信して正常終了を待ち、その後で永続資産全体をコピーしてください。実行中に`gpt-load.db`だけをコピーしないでください。
- AUTH_KEY、ENCRYPTION_KEY、AccessKey、上流キーをログ、公開issue、スクリーンショット、通常のバックアップ一覧に貼り付けないでください。

### 公開運用ベースライン

以下のチェックリストは、プロジェクトの非公開Notionワークスペースへアクセスせずに利用できます。

1. バックアップまたは切り替え前に、管理認証付きで`GET /api/system/info`を呼び出します。解決済みの`data_dir`、データベースの場所、secretの取得元だけを記録し、secret値は記録しません。
2. 新規トラフィックを止め、`SIGTERM`を送信して正常終了を待ちます。Composeでは`docker compose stop`を実行し、サービスコンテナが停止したことを確認します。
3. 解決済みの`DATA_DIR`全体、または正確なnamed volumeを一組の復旧資産としてアーカイブします。一意な名前を使い、上書きを拒否し、アクセスを制限してSHA-256を記録します。外部`DATABASE_DSN`、`AUTH_KEY`、`ENCRYPTION_KEY`は別途バックアップします。
4. まったく同じバイナリまたはイメージを使い、空のターゲットへ復元します。展開前にchecksumを検証し、対応するencryption keyを復元します。復元とアップグレードを同時に行わないでください。
5. 復元したインスタンスを起動し、`/health`、`/api/system/info`、Group、AccessKey、モデル価格、Usage、RequestLog、実データプレーンcanaryを確認します。`sqlite3`が利用できる場合は停止後に`PRAGMA quick_check`を実行し、`ok`を必須とします。

2.0.0にはbackup CLIがなく、encryption key rotationにも対応しません。既存データベースのencryption keyを置換しないでください。

## 1.xからの切り替え

2.0は1.xデータベースを開く、インポートする、インプレースアップグレードすることができず、1.xの`DATA_DIR`も再利用できません。推奨手順：

1. 1.xを稼働させたまま、バックアップから復元できることを確認します。
2. 2.0には別のポート、`DATA_DIR` / named volume、データベースを用意します。
3. 最小限のGroup、上流キー、AccessKey、ルールを手動で再構築し、3方言、ログ、usage/costを隔離環境で検証します。
4. メンテナンス時間または小規模なロールアウトで入口トラフィックを切り替えます。失敗した場合は2.0を停止して元の1.xへ戻し、2.0で新たに生成されたデータを1.xへ逆インポートしません。

`latest`は1.xから2.0への安全なアップグレードチャネルではありません。バックアップと復元は上記の公開運用ベースラインに従い、ロールバック期間が終了するまで元の1.xデプロイとデータを保持してください。

## ビルドと検証

基準ツール：Go `1.25.12`、Node.js `>=24.11.0`、pnpm `11.15.1`。

管理UIを内蔵した単一バイナリをビルドします。

```console
make build
```

ローカルの完全な品質ゲート：

```console
corepack pnpm --dir web install --frozen-lockfile
corepack pnpm --dir web run lint
corepack pnpm --dir web run format
corepack pnpm --dir web run type-check
corepack pnpm --dir web run test
corepack pnpm --dir web run build
go build -o gpt-load .
go test -race . ./internal/...
corepack pnpm --dir web run test:e2e
```

2.0.0では5つのネイティブraw binaryと`SHA256SUMS`を提供する予定です。

- `gpt-load-linux-amd64`
- `gpt-load-linux-arm64`
- `gpt-load-macos-amd64`
- `gpt-load-macos-arm64`
- `gpt-load-windows-amd64.exe`

これらはリリース契約上の予定名であり、ダウンロード可能なGitHub Releaseが現時点で存在するという主張ではありません。

## ライセンスとセキュリティ

GPT-Loadは[MIT License](LICENSE)で公開されています。脆弱性は[SECURITY.md](SECURITY.md)の手順に従って報告してください。
