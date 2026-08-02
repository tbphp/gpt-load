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
> 2.0は現在、**プレリリースのローカル候補**です。M3/M4の候補コードとローカル検証の保存済み証跡はありますが、正式なリリース判断・承認と公開は完了していません。`v2.0.0`タグ、GitHub Release、公開バイナリ、公開コンテナイメージが利用可能だと確認できる証拠はありません。checkoutやブランチの状態はリリースの証拠にはなりません。

2.0は1.xとデータ互換性のないgreenfield rewriteです。`main`は引き続き1.4.xメンテナンスラインを提供します。リリース契約では明示的な`2`、`2.0`、`v2.0.0`コンテナタグを予約し、`latest`を自動更新しませんが、これらの名前自体はイメージの公開を意味しません。

## 2.0の機能

- **2つのプレーン**：データプレーンはプロバイダーのネイティブパスを維持し、管理APIは`/api`に統一されます。管理UIは同じGoバイナリに内蔵されます。
- **4つの選択可能なネイティブプロトコル**：OpenAI Completions、OpenAI Responses、Anthropic Messages、Geminiのリクエストをそれぞれのプロトコルで転送します。Groupでは任意に複数選択でき、プロトコル間の変換は行いません。
- **キーとトラフィックの管理**：Group、暗号化された上流Key、AccessKey、モデル検出、フィルターとレート制限、スケジューリング、ヘルス状態、cooldown、blacklist、自動重み付け。
- **制御と可観測性**：ランタイム設定、ルート検査、ヘルス表示、RequestLog、中国語・英語・日本語の管理UI。
- **使用量と推定コスト**：4プロトコルのうち生成usageを返すエンドポイントからusageを取得し、24時間/30日レポート、リクエスト単位の品質状態、組み込み価格、ユーザー価格の上書きを提供します。

M3のコントロールプレーンUIとM4のusage/pricing範囲はローカル候補に含まれていますが、正式なリリース判断・承認と公開は未完了です。価格とコストは、上流から返されたusageと現在の価格ルールに基づくbest-effortの**推定値**です。billing ledger、請求書、プロバイダー請求ではなく、過去のリクエストを再計算することもありません。

## 2.0.0のサポート境界

- 正しさを保証するのは**単一アプリケーションインスタンス**のみで、複数インスタンスの協調には対応しません。
- **SQLiteのみ**をサポートし、PostgreSQL、MySQL、その他のデータベースには対応しません。
- GroupはAccessKeyとランタイム設定で選択され、データプレーンURLには含まれません。
- プロトコル設定はclean breakです。使用できる値は`openai-completions`、`openai-responses`、`anthropic`、`gemini`だけです。旧値`openai`、`openai-response`、`openai-chat-completions`に互換処理はありません。
- データベースに旧プロトコル値が1件でも残ると、`ConfigSnapshot`全体のコンパイルが失敗し、起動または設定公開を停止します。エラーにはGroupまたはAccessKeyのIDと不正な値が含まれます。起動前にプレリリース2.0のデータを再構築してください。プロトコル値のインプレース移行はありません。
- OpenAI ResponsesのリソースルーティングにはKey affinityがありません。`previous_response_id`または`conversation`を使うステートフルな複数ターンと、後続のretrieve/delete/cancel/input-itemsは、上流Keyが1つの場合、または上流がKey間でリソースストレージを共有する場合だけ確実です。それ以外では、選択された上流からresource-not-foundが返る可能性があります。
- 上流キーは必ず保存時に暗号化され、平文へのフォールバックはありません。2.0.0はマスターキーのローテーションに対応せず、`migrate-keys`は明示的に失敗する延期コマンドのままです。
- 1.xデータの自動移行、インプレースアップグレード、逆同期には対応しません。
- プロトコル変換、オンライン請求照合、自動価格取得、オンラインバックアップAPI、バックアップCLIは提供しません。

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

デフォルトのnamed volumeにはSQLite、`auth.key`、`encryption.key`が保持されます。本番環境では、保護されたsecret処理を通じて明示的な`AUTH_KEY`と`ENCRYPTION_KEY`を注入してください。実際のsecretを`.env`、ログ、issueへコミットしないでください。コンテナで`DATABASE_DSN`を変更する場合、**コンテナ内**パスと対応するvolume mountをCompose overrideで同時に設定する必要があります。

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

GPT-Loadは方言間の変換を行いません。GroupはAccessKeyとランタイム設定で選択され、URLパスセグメントとして渡しません。

正規のプロトコル設定値と表示名は次のとおりです。

| 設定値 | 表示名 |
|---|---|
| `openai-completions` | OpenAI Completions |
| `openai-responses` | OpenAI Responses |
| `anthropic` | Anthropic |
| `gemini` | Gemini |

組み込みOpenAIプロバイダープリセットは、preset IDとして`openai`を維持し、URLに`https://api.openai.com/v1`を使用して、デフォルトで2つのOpenAIプロトコルを有効にします。両方とも通常の独立した選択肢で、どちらか一方または両方を選択できます。

Responsesルーティングはリソースごとのallowlistではなく、名前空間境界で一致します。AccessKey認証後、`/v1/responses`と通常のサブパスは同じスケジューラーおよび転送パイプラインに入ります。デコード済みの`.`または`..`パスセグメントは、正規化やリダイレクトによる認可済み名前空間からの逸脱を防ぐためローカルで拒否します。`OPTIONS`、`CONNECT`、`TRACE`もローカルで拒否し、`GET`、`POST`、`DELETE`、`HEAD`を含むその他のmethodは転送します。パスとqueryはGo URL正規化の範囲内で保持され、デコード済み`URL.Path`は再エンコードされ、`RawPath`は保持されません。GPT-LoadはリソースIDを使って別のKeyを検索しません。選択された上流の応答（resource-not-foundを含む）は、共通の応答安全境界を通して返されます。

Responsesを有効にしたGroupはモデル一覧が空でも、modelを含まないResponsesリソースAPIを処理できます。通常のcreateを含むmodel付き要求には、引き続き一致するモデルルートが必要です。

> [!WARNING]
> 2.0.0はResponses affinityを実装していません。`previous_response_id`または`conversation`を使うステートフルな複数ターン、および既存response IDへのリソース操作は、別のGroup/Keyに到達して上流404を受ける場合があります。affinity実装までは、単一Key、`store: false`によるステートレスなitem replay、またはKey間でリソースストレージを共有する上流を使用してください。

Responsesのcreateとcompactはusage抽出の対象です。retrieve、delete、cancel、input-items、input-token-count、不明な拡張サブパスはRequestLogでusage `not_applicable`として記録されます。`InjectUsageOptions`は引き続きcapabilityベースです。Responses dialectはOpenAI Completionsの`stream_options.include_usage`を実装しないため、このGroup設定はResponsesでは無視されます。Responsesだけを選択したGroupは`input: "ping"`、`max_output_tokens: 16`、`store: false`でprobeします。2つのOpenAIプロトコルを選択した場合は、OpenAI CompletionsをGroup/Keyの代表probeとして使用します。プロトコル単位のヘルス状態はありません。

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

管理UIは`/`、管理APIは`/api`で提供され、どちらも`AUTH_KEY`を使用します。UIにはGroup、上流キー、AccessKey、ランタイム設定、ヘルス、ログ、ルート検査、Usage、モデル価格管理があります。管理APIの事実源は現在のコードとUIであり、このREADMEでは変化しやすいルート一覧を複製しません。

Usage/Costの品質境界：

- `complete + priced`のリクエストだけが、デフォルトのtoken合計と推定コスト合計に入ります。
- `missing`、`partial`、`unpriced`もリクエスト数と品質カウントには入りますが、デフォルトのtoken/コスト合計には入りません。`complete + unpriced`に推測価格を割り当てることもありません。
- ストリームのclean EOFは完全なusageを保証せず、互換中継サービスがプロバイダー公式の終端usageを返さない場合もあります。
- APIの`pricing_policy`は読み取り専用です。UIは表示のみを行い、ユーザー定義価格ルールから内部pricing policyを宣言することはできません。
- 価格変更は今後の書き込みにだけ影響し、過去のRequestLogやUsageStatは再計算されません。
- 現在のプロセスにおけるdropped/write-failureカウンターと、データベース期間内の永続集計は異なる範囲です。

## 主要設定

| 変数 | デフォルト | 用途 |
|---|---|---|
| `HOST` | `127.0.0.1` | ネイティブHTTPリッスンアドレス。`0.0.0.0`は明示的なopt-inで、リリースコンテナは内部だけ`0.0.0.0`に上書き |
| `BIND_ADDRESS` | `127.0.0.1` | Composeのホスト側公開アドレス。プロセス設定ではない |
| `PORT` | `3001` | HTTPリッスンポート |
| `DATA_DIR` | `./data` | ネイティブの永続ディレクトリ。コンテナ内では`/app/data`に上書き |
| `DATABASE_DSN` | 空 → `${DATA_DIR}/gpt-load.db` | 空ならmanaged SQLiteを選択。デフォルトと同じ文字列でも、operatorが設定した非空値はすべてexternal |
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

- データベースの所有区分はraw `DATABASE_DSN`だけで決まります。空なら`${DATA_DIR}`配下のmanaged DB/WAL/SHM、非空ならoperator所有のexternalデータベースです。externalではGPT-Loadはmkdir/chmodを行わず、operatorが別途バックアップします。
- secretの所有区分はデータベースとは独立しています。`/api/system/info`がsecretごとにsourceを返します。データベースのsourceにかかわらず、`key_file`なら`DATA_DIR`内の対応する`auth.key` / `encryption.key`をアーカイブし、`environment`なら保護された外部secret systemから別途復元します。
- POSIXではmanaged `${DATA_DIR}`を`0700`、managed DB/WAL/SHMとアプリケーションが作成したkeyファイルを`0600`に制限します。Windowsでは実行ユーザー専用ACLを使用しますが、この候補についてWindows runtimeの停止/ACLゲートは未実行です。
- sourceにかかわらず、対応する`encryption.key`を失うと、暗号化済みの上流キーは復旧できません。2.0.0には自動修復やマスターキーのローテーションがありません。
- SQLiteはWALを使用します。バックアップ前に新規トラフィックを止め、clean exitを待ちます。POSIXでは`SIGTERM`、WindowsではCtrl+C、Ctrl+Break、またはservice managerの停止操作を使用します。実行中に`gpt-load.db`だけをhot copyしないでください。
- AUTH_KEY、ENCRYPTION_KEY、AccessKey、上流キーをログ、公開issue、スクリーンショット、通常のバックアップ一覧に貼り付けないでください。

### 公開運用ベースライン

以下のチェックリストは、プロジェクトの非公開Notionワークスペースへアクセスせずに利用できます。

1. 実際のenvironment、service、container設定からデータベースのsourceと場所を判断し、管理認証付きの`GET /api/system/info`で各secretの安全なsource/pathメタデータを値なしで記録します。このendpointはdatabase source、DSN、場所を意図的に返しません。
2. 新規トラフィックを止め、上記のPOSIXまたはWindowsの方法でclean exitを待ちます。Composeでは`docker compose stop`を実行し、サービスコンテナが停止したことを確認します。
3. 独立した2軸から完全な復旧セットを作ります。`DATABASE_DSN`が空ならmanaged DB/WAL/SHMをアーカイブし、非空ならoperatorの手順でexternal DBを別途バックアップします。どちらの場合も、auth/encryptionの各`key_file`をアーカイブし、各`environment` secretを保護された外部secret systemから復元します。一意なアーカイブ名を使い、上書きを拒否し、アクセスを制限してSHA-256を記録します。
4. まったく同じバイナリまたはイメージを使い、空のターゲットへデータベースとsecretの両方を復元します。先にchecksumを検証し、完全に対応するencryption keyを復元します。復元とアップグレードを同時に行わないでください。
5. 復元したインスタンスを起動し、`/health`、`/api/system/info`、Group、AccessKey、モデル価格、Usage、RequestLog、実データプレーンcanaryを確認します。`sqlite3`が利用できる場合は停止後に`PRAGMA quick_check`を実行し、`ok`を必須とします。

2.0.0にはbackup CLIがなく、encryption key rotationにも対応しません。既存データベースのencryption keyを置換しないでください。

## 1.xからの切り替え

2.0は1.xデータベースを開く、インポートする、インプレースアップグレードすることができず、1.xの`DATA_DIR`も再利用できません。推奨手順：

1. 1.xを稼働させたまま、バックアップから復元できることを確認します。
2. 2.0には別のポート、`DATA_DIR`、データベース、Compose project、named volumeを用意し、1.xと共有しません。
3. 最小限のGroup、上流キー、AccessKey、ルールを手動で再構築し、4つのプロトコル、ログ、usage/costを隔離環境で検証します。
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

2.0.0では5つのネイティブraw binaryと`SHA256SUMS`を提供する予定です。

- `gpt-load-linux-amd64`
- `gpt-load-linux-arm64`
- `gpt-load-macos-amd64`
- `gpt-load-macos-arm64`
- `gpt-load-windows-amd64.exe`

これらはリリース契約上の予定名であり、ダウンロード可能なGitHub Releaseが現時点で存在するという主張ではありません。

## ライセンスとセキュリティ

GPT-Loadは[MIT License](LICENSE)で公開されています。脆弱性は[SECURITY.md](SECURITY.md)の手順に従って報告してください。
