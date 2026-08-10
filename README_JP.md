# GPT-Load

[English](README.md) | [中文](README_CN.md) | 日本語

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
![Go Version](https://img.shields.io/badge/Go-1.26-blue.svg)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

GPT-Loadは、Goで構築されたセルフホスト型のマルチチャネルAIゲートウェイです。管理UIを内蔵した単一バイナリでチャネルプリセットと暗号化された認証情報を管理し、OpenAI、Anthropic、Gemini互換のクライアントエンドポイントを公開します。

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
> 2.0は現在、**プレリリースのローカル候補**です。M3/M4の候補コードとローカル検証の保存済み証跡はありますが、正式なリリース判断・承認と公開は完了していません。`v2.0.0`タグ、GitHub Release、公開バイナリ、公開コンテナイメージが利用可能だと確認できる証拠はありません。checkoutやブランチの状態はリリースの証拠にはなりません。

2.0は1.xとデータ互換性のないgreenfield rewriteです。既存の2.xデータベースはインプレースで前方移行できます。アップグレード前にサービスを停止し、対応するencryption keyとともにデータベースをバックアップしてください。`main`は引き続き1.4.xメンテナンスラインを提供します。リリース契約では明示的な`2`、`2.0`、`v2.0.0`コンテナタグを予約し、`latest`を自動更新しませんが、これらの名前自体はイメージの公開を意味しません。

## 2.0の機能

- **2つのプレーン**：データプレーンはプロバイダーのネイティブパスを維持し、管理APIは`/api`に統一されます。管理UIは同じGoバイナリに内蔵されます。
- **4つのクライアントプロトコル**：OpenAI Completions、OpenAI Responses、Anthropic Messages、Geminiの公開エンドポイントを維持します。各チャネルはネイティブまたは変換可能なoperationを明示し、未対応の機能組み合わせは送信前に拒否します。
- **チャネルとトラフィックの管理**：ユーザーは検索可能なコード定義チャネルを選び、モデルと暗号化認証情報を入力します。GPT-LoadがAccessKeyフィルター、Groupをまたぐ認証情報スケジューリング、リトライ判断、ヘルス、cooldown、blacklist、自動重み付けを所有します。
- **Provider実行**：公式Bifrost Core Go SDKがProvider固有の認証、request/response変換、streaming、usage正規化、エラー解析を担当します。GPT-Loadは論理attemptごとに1つの認証情報を固定し、Bifrostの設定可能なretryとfallbackを無効にします。
- **制御と可観測性**：ランタイム設定、ルート検査、ヘルス表示、RequestLog、中国語・英語・日本語の管理UI。
- **使用量と推定コスト**：4プロトコルのうち生成usageを返すエンドポイントからusageを取得し、24時間/30日レポート、リクエスト単位の品質状態、利用可能な場合にModels.devから同期する完全一致の4価格スロット、ユーザー管理価格を提供します。

M3のコントロールプレーンUIとM4のusage/pricing範囲はローカル候補に含まれていますが、正式なリリース判断・承認と公開は未完了です。価格とコストは、上流から返されたusageと現在の価格ルールに基づくbest-effortの**推定値**です。billing ledger、請求書、プロバイダー請求ではなく、過去のリクエストを再計算することもありません。

## 2.0.0のサポート境界

- 正しさを保証するのは**単一アプリケーションインスタンス**のみで、複数インスタンスの協調には対応しません。
- 統一された `DATABASE_DSN` で SQLite、MySQL、PostgreSQL をサポートします。現在のリリースはこの3種類の最新安定版を対象とし、その他のデータベース製品には対応しません。
- GroupはAccessKeyとランタイム設定で選択され、データプレーンURLには含まれません。
- AccessKeyのクライアントプロトコルフィルターは`openai-completions`、`openai-responses`、`anthropic`、`gemini`を使用します。Groupは1つの`channel_id`だけを保存し、コード定義プリセットがProvider動作、クライアントプロトコル、認証情報schema、モデル検出、Models.dev対応を決定します。
- OpenAI ResponsesのリソースルーティングにはCredential affinityがありません。`previous_response_id`または`conversation`を使うステートフルな複数ターンと、後続のretrieve/delete/cancel/input-itemsは、Credentialが1つの場合、または上流がCredential間でリソースストレージを共有する場合だけ確実です。それ以外では、選択された上流からresource-not-foundが返る可能性があります。
- チャネル認証情報は必ず保存時に暗号化され、平文へのフォールバックはありません。2.0.0はマスターキーのローテーションに対応せず、`migrate-keys`は明示的に失敗する延期コマンドのままです。
- 1.xデータの自動移行や逆同期には対応しません。既存2.x schemaはmigration ledgerで前方移行します。
- オンライン請求照合、オンラインバックアップAPI、バックアップCLIは提供しません。Models.dev同期が提供するのは推定用メタデータだけで、プロバイダー請求書やインボイスではありません。

## クイックスタート

### Docker Compose

現在の2.x Compose候補契約は`ghcr.io/tbphp/gpt-load:2`、コンテナ内パス`/app/data`、named volume `gpt-load-data`を参照します。これはローカル契約であり、公開イメージが利用可能であることの証拠ではありません。契約は`latest`を使用しません。まず現在のcheckoutを確認します。

```console
cp .env.example .env
docker compose config
```

解決後の設定が次を満たす場合だけ続行してください。imageは`ghcr.io/tbphp/gpt-load:2`、**コンテナ内**環境は`HOST=0.0.0.0`と`DATA_DIR=/app/data`、`DATABASE_DSN`は空または未設定のままでプロセスがmanaged `/app/data/gpt-load.db`を選択し、**ホスト側**は`${BIND_ADDRESS:-127.0.0.1}`に公開され、`/app/data`にはnamed volumeがマウントされます。固定`container_name`はなく、Compose project nameでインスタンスを分離します。公開イメージが利用できない場合は、公開済みと仮定せず、コメントされたローカルbuild overrideを使用してください。

上記の設定とimage/buildの可用性を確認した後：

```console
docker compose up -d
curl --fail http://localhost:3001/health
# 初回起動でAUTH_KEYが生成された場合、安全な端末で一度だけ読み取り、
# 直ちにsecret managerへ保存します。
docker compose exec gpt-load sh -c 'cat /app/data/auth.key'
```

デフォルトのnamed volumeにはSQLite、`auth.key`、`encryption.key`が保持されます。本番環境では、保護されたsecret処理を通じて明示的な`AUTH_KEY`と`ENCRYPTION_KEY`を注入してください。実際のsecretを`.env`、ログ、issueへコミットしないでください。MySQLまたはPostgreSQLの`DATABASE_DSN`はoperator管理の設定として、デプロイのsecret/configuration systemから渡してください。

Composeはコンテナ内でのみ全インターフェースをlistenし、ホスト側はデフォルトでloopbackにだけ公開します。`BIND_ADDRESS=0.0.0.0`、またはネイティブプロセスの`HOST=0.0.0.0`は明示的なopt-inです。本番では、TLS reverse proxyとACL/firewallを備えた管理下のネットワーク境界の内側でのみ公開してください。

### ネイティブバイナリ

公開後、GitHub Releaseからプラットフォームに合うartifactをダウンロードし、`SHA256SUMS`で検証します。release artifactが実際に存在するまでは、「ビルドと検証」に従って現在のcheckoutからビルドし、既に公開済みと仮定しないでください。

Linux amd64の例：

```console
chmod +x ./gpt-load-linux-amd64
mkdir -p ./data
HOST=127.0.0.1 DATA_DIR=./data ./gpt-load-linux-amd64
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
| OpenAI | `POST /v1/chat/completions` | ネイティブOpenAI Completionsリクエスト |
| OpenAI | `/v1/responses`および`/v1/responses/...` | ネイティブOpenAI Responses名前空間。通常のHTTP methodを転送 |
| OpenAI / Anthropic | `GET /v1/models` | デフォルトはOpenAI形式、`anthropic-version`がある場合はAnthropic形式 |
| Anthropic | `POST /v1/messages` | ネイティブAnthropic Messagesリクエスト |
| Gemini | `GET /v1beta/models` | ネイティブGeminiモデル一覧 |
| Gemini | `POST /v1beta/models/{model}:generateContent` | Gemini非ストリーミング生成 |
| Gemini | `POST /v1beta/models/{model}:streamGenerateContent` | Geminiストリーミング生成 |

GroupはAccessKeyとランタイム設定で選択され、URLパスセグメントとして渡しません。選択されたチャネルがoperationをネイティブ実行するか変換するかを決定します。変換はcapability gateを通り、任意JSONの無損失変換を保証しません。

正規のクライアントプロトコルフィルター値と表示名は次のとおりです。

| 設定値 | 表示名 |
|---|---|
| `openai-completions` | OpenAI Completions |
| `openai-responses` | OpenAI Responses |
| `anthropic` | Anthropic |
| `gemini` | Gemini |

組み込み`openai`チャネルは2つのOpenAIクライアントプロトコルをサポートします。その他の公式、主要中継、互換、クラウドチャネルはコードプリセットで宣言されたoperationと機能だけを公開し、Groupごとのプロトコルチェックボックスはありません。

Responsesルーティングはリソースごとのallowlistではなく、名前空間境界で一致します。AccessKey認証後、`/v1/responses`と通常のサブパスは同じスケジューラーおよび実行パイプラインに入ります。デコード済みの`.`または`..`パスセグメントは、正規化やリダイレクトによる認可済み名前空間からの逸脱を防ぐためローカルで拒否します。`OPTIONS`、`CONNECT`、`TRACE`もローカルで拒否し、`GET`、`POST`、`DELETE`、`HEAD`を含むその他のmethodは転送します。パスとqueryはGo URL正規化の範囲内で保持され、デコード済み`URL.Path`は再エンコードされ、`RawPath`は保持されません。GPT-LoadはリソースIDを使って別のCredentialを検索しません。選択された上流の応答（resource-not-foundを含む）は、共通の応答安全境界を通して返されます。

ResponsesをサポートするチャネルのGroupはモデル一覧が空でも、modelを含まないResponsesリソースAPIを処理できます。通常のcreateを含むmodel付き要求には、引き続き一致するモデルルートが必要です。

> [!WARNING]
> 2.0.0はResponses affinityを実装していません。`previous_response_id`または`conversation`を使うステートフルな複数ターン、および既存response IDへのリソース操作は、別のGroup/Credentialに到達して上流404を受ける場合があります。affinity実装までは、単一Credential、`store: false`によるステートレスなitem replay、またはCredential間でリソースストレージを共有する上流を使用してください。

Responsesのcreateとcompactはusage抽出の対象です。retrieve、delete、cancel、input-items、input-token-count、不明な拡張サブパスはRequestLogでusage `not_applicable`として記録されます。モデル検出とvalidationはユーザーのプロトコル選択ではなく、チャネルプリセットが決定します。

OpenAI Completionsの例：

```console
curl http://127.0.0.1:3001/v1/chat/completions \
  -H "Authorization: Bearer $GPT_LOAD_ACCESS_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"<MODEL_ID>","messages":[{"role":"user","content":"Hello"}]}'
```

Responsesの例：

```console
curl http://127.0.0.1:3001/v1/responses \
  -H "Authorization: Bearer $GPT_LOAD_ACCESS_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"<MODEL_ID>","input":"Hello","store":false}'
```

OpenAI公式SDKも同じネイティブエンドポイントを使用できます。

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:3001/v1",
    api_key=os.environ["GPT_LOAD_ACCESS_KEY"],
)
response = client.responses.create(
    model="<MODEL_ID>",
    input="Hello",
    store=False,
)
print(response.output_text)
```

## 管理、使用量、コスト

管理UIは`/`、管理APIは`/api`で提供され、どちらも`AUTH_KEY`を使用します。UIには検索可能なチャネル一覧、Group、暗号化認証情報、AccessKey、ランタイム設定、ヘルス、ログ、ルート検査、Usage、モデル価格管理があります。管理APIの事実源は現在のコードとUIであり、このREADMEでは変化しやすいルート一覧を複製しません。

モデルカタログの自動同期はデフォルトで有効で、コントロールプレーンから固定エンドポイント`https://models.dev/api.json`へアクセスします。起動処理は非同期のままで、永続化したlast-known-goodカタログを利用できます。手動同期も常に利用でき、データプレーンのリクエストがModels.devへアクセスすることはありません。

Usage/Costの品質境界：

- `complete`と`partial`のusageは既知のtoken次元を集計し、`missing` usageはリクエスト数と品質カウントだけに入ります。
- `priced`リクエストは既知の推定コストを集計します。`pricing_partial`は計算可能な部分を保持しながら価格カバレッジ不足を示し、`unpriced`に推測価格を割り当てることはありません。
- ストリームのclean EOFは完全なusageを保証せず、互換中継サービスがプロバイダー公式の終端usageを返さない場合もあります。
- 価格は`(channel_id, 上流モデル)`に完全一致します。Models.devが唯一の自動価格ソースで、ユーザーの明示的overrideと既存fallbackルールは維持されます。4つの平面価格スロットは入力、出力、キャッシュ読み取り、キャッシュ書き込みで、明示的な`0`は無料、未設定は推定不可を表します。
- 価格変更は今後の書き込みにだけ影響し、過去のRequestLogやUsageStatは再計算されません。
- 現在のプロセスにおけるdropped/write-failureカウンターと、データベース期間内の永続集計は異なる範囲です。

## 主要設定

| 変数 | デフォルト | 用途 |
|---|---|---|
| `HOST` | `127.0.0.1` | ネイティブHTTPリッスンアドレス。`0.0.0.0`は明示的なopt-inで、リリースコンテナは内部だけ`0.0.0.0`に上書き |
| `BIND_ADDRESS` | `127.0.0.1` | Composeのホスト側公開アドレス。プロセス設定ではない |
| `PORT` | `3001` | HTTPリッスンポート |
| `DATA_DIR` | `./data` | ネイティブの永続ディレクトリ。コンテナ内では`/app/data`に上書き |
| `DATABASE_DSN` | 空 → `${DATA_DIR}/gpt-load.db` | 空ならmanaged SQLiteを選択。非空値は統一された`sqlite`、`mysql`、`postgres` URLで指定し、operatorが管理 |
| `AUTH_KEY` | keyfileを自動生成 | 管理bearer認証情報。明示値に空白は使用不可。空の場合`${DATA_DIR}/auth.key`を読み取りまたは作成 |
| `ENCRYPTION_KEY` | keyfileを自動生成 | チャネル認証情報の暗号化用マスターキー。空の場合`${DATA_DIR}/encryption.key`を読み取りまたは作成 |
| `MODELS_DEV_AUTO_SYNC_ENABLED` | 未設定 | Models.dev自動同期の任意の厳密なboolean override。未設定ではデフォルト有効のランタイム設定を使用 |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | グレースフルシャットダウンの秒数 |
| `READ_TIMEOUT` | `60` | リクエスト全体を読み取る最大秒数 |
| `IDLE_TIMEOUT` | `120` | keep-aliveのアイドルタイムアウト秒数 |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Composeの停止猶予。アプリケーションのシャットダウン時間より長く設定 |
| `LOG_LEVEL` | `info` | アプリケーションログレベル |
| `LOG_FORMAT` | `text` | ログ形式：`text`または`json` |

プロセス設定の全項目は[`.env.example`](.env.example)を参照してください。接続、最初のバイト、リクエスト、ストリームアイドルの各タイムアウトとRequestLog保持期間は管理UI/APIで扱うランタイム設定であり、追加の環境変数ではありません。

`DATABASE_DSN`は統一されたURL契約を使い、データベース種別変数やデータベース別のアプリ設定は追加しません。例：

```text
sqlite:///var/lib/gpt-load.db
mysql://user:password@db.example:3306/gpt_load?charset=utf8mb4&collation=utf8mb4_bin
postgres://user:password@db.example:5432/gpt_load?sslmode=require
```

空値だけがアプリケーション管理のデータベースファイルモードです。非空のSQLiteパス/URLとすべてのMySQL/PostgreSQL URLはoperator管理です。

## 永続化とセキュリティ

- データベースの所有区分はraw `DATABASE_DSN`だけで決まります。空なら`${DATA_DIR}`配下のmanaged SQLite DB/WAL/SHM、非空ならoperator所有のexternalデータベースです。GPT-Loadはexternalに対してmkdir、chmod、データベースやユーザー作成を行わず、operatorが別途バックアップします。
- secretの所有区分はデータベースとは独立しています。`/api/system/info`がsecretごとにsourceを返します。データベースのsourceにかかわらず、`key_file`なら`DATA_DIR`内の対応する`auth.key` / `encryption.key`をアーカイブし、`environment`なら保護された外部secret systemから別途復元します。
- POSIXではmanaged `${DATA_DIR}`を`0700`、managed DB/WAL/SHMとアプリケーションが作成したkeyファイルを`0600`に制限します。Windowsでは実行ユーザー専用ACLを使用しますが、この候補についてWindows runtimeの停止/ACLゲートは未実行です。
- sourceにかかわらず、対応する`encryption.key`を失うと、暗号化済みのチャネル認証情報は復旧できません。2.0.0には自動修復やマスターキーのローテーションがありません。
- Managed SQLiteはWALを使用します。バックアップ前に新規トラフィックを止め、clean exitを待ちます。POSIXでは`SIGTERM`、WindowsではCtrl+C、Ctrl+Break、またはservice managerの停止操作を使用します。MySQL/PostgreSQLはoperatorのデータベースネイティブなバックアップ手順に従います。
- AUTH_KEY、ENCRYPTION_KEY、AccessKey、チャネル認証情報をログ、公開issue、スクリーンショット、通常のバックアップ一覧に貼り付けないでください。

### 公開運用ベースライン

以下のチェックリストは、プロジェクトの非公開Notionワークスペースへアクセスせずに利用できます。

1. 実際のenvironment、service、container設定からデータベースのsourceと場所を判断し、管理認証付きの`GET /api/system/info`で選択されたデータベースドライバと各secretの安全なsource/pathメタデータを値なしで記録します。このendpointはデータベースのDSN、認証情報、場所を意図的に返しません。
2. 新規トラフィックを止め、上記のPOSIXまたはWindowsの方法でclean exitを待ちます。Composeでは`docker compose stop`を実行し、サービスコンテナが停止したことを確認します。
3. 独立した2軸から完全な復旧セットを作ります。`DATABASE_DSN`が空ならmanaged DB/WAL/SHMをアーカイブし、非空ならoperatorの手順でexternal DBを別途バックアップします。どちらの場合も、auth/encryptionの各`key_file`をアーカイブし、各`environment` secretを保護された外部secret systemから復元します。一意なアーカイブ名を使い、上書きを拒否し、アクセスを制限してSHA-256を記録します。
4. まったく同じバイナリまたはイメージを使い、空のターゲットへデータベースとsecretの両方を復元します。先にchecksumを検証し、完全に対応するencryption keyを復元します。復元とアップグレードを同時に行わないでください。
5. 復元したインスタンスを起動し、`/health`、`/api/system/info`、Group、AccessKey、モデル価格、Usage、RequestLog、実データプレーンcanaryを確認します。Managed SQLiteでは停止後に`PRAGMA quick_check`を実行し、`ok`を必須とします。MySQL/PostgreSQLではoperatorのネイティブな整合性検査を使います。

2.0.0にはbackup CLIがなく、encryption key rotationにも対応しません。既存データベースのencryption keyを置換しないでください。

## 1.xからの切り替え

2.0は1.xデータベースを開く、インポートする、インプレースアップグレードすることができず、1.xの`DATA_DIR`も再利用できません。推奨手順：

1. 1.xを稼働させたまま、バックアップから復元できることを確認します。
2. 2.0には別のポート、`DATA_DIR`、データベース、Compose project、named volumeを用意し、1.xと共有しません。
3. 最小限のGroup、チャネル認証情報、AccessKey、ルールを手動で再構築し、必要なクライアントプロトコル、ログ、usage/costを隔離環境で検証します。
4. メンテナンス時間または小規模なロールアウトで入口トラフィックを切り替えます。失敗した場合は2.0を停止して元の1.xへ戻し、2.0で新たに生成されたデータを1.xへ逆インポートしません。

`latest`は1.xから2.0への安全なアップグレードチャネルではありません。バックアップと復元は上記の公開運用ベースラインに従い、ロールバック期間が終了するまで元の1.xデプロイとデータを保持してください。

## ビルドと検証

基準ツール：Go `1.26.5`、Node.js `>=24.11.0`、pnpm `11.17.0`。

管理UIを内蔵した単一バイナリをビルドします。

```console
make build
```

ローカルの完全な品質ゲート：

```console
make check
```

プロジェクトのワークフローには、フロントエンドのユニットテストとブラウザE2Eテストを含めません。フロントエンドの検証範囲は、依存関係のインストール、lint、format、type-check、buildです。

2.0.0では5つのネイティブraw binaryに加え、`SHA256SUMS`、CycloneDX SBOM、プロジェクトと第三者ライセンス通知を提供する予定です。

- `gpt-load-linux-amd64`
- `gpt-load-linux-arm64`
- `gpt-load-macos-amd64`
- `gpt-load-macos-arm64`
- `gpt-load-windows-amd64.exe`

これらはリリース契約上の予定名であり、ダウンロード可能なGitHub Releaseが現時点で存在するという主張ではありません。

## ライセンスとセキュリティ

GPT-Loadは[MIT License](LICENSE)で公開されています。第三者通知は[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)、完全なライセンス本文は[`LICENSES/`](LICENSES/)にあります。脆弱性は[SECURITY.md](SECURITY.md)の手順に従って報告してください。
