<div align="center">

<img src="./web/public/favicon.svg" alt="GPT-Load" width="96">

# GPT-Load

**マルチチャネル・マルチ認証情報向けのセルフホスト AI ゲートウェイ**

API キー、サブスクリプションアカウント、トラフィック制御、障害処理、リクエストログ、使用量集計を一つの入口にまとめます。

[English](README.md) · [中文](README_CN.md) · 日本語 | [公式サイト](https://www.gpt-load.com)

[![Release](https://img.shields.io/github/v/tag/tbphp/gpt-load?filter=v2.*)](https://github.com/tbphp/gpt-load/releases)
[![Docker](https://img.shields.io/badge/Docker-ghcr.io%2Ftbphp%2Fgpt--load%3A2-2496ED?logo=docker&logoColor=white)](https://github.com/tbphp/gpt-load/pkgs/container/gpt-load)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp/gpt-load | Trendshift" width="220" height="48"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" width="220" height="47"/></a>

</div>

---

## スポンサー

<sub>[スポンサーになる](mailto:tangb7420@gmail.com)</sub>

<details open>
<summary>スポンサー詳細（折りたたみ可能）</summary>

<table>
<tbody>
<tr>
<td width="180"><a href="https://www.axisnow.io/zh"><img src="./screenshot/axisnow.jpg" alt="AxisNow" width="150"></a></td>
<td>ウェブサイトと API を保護・高速化し、<strong>中国本土</strong>および世界各地のアクセス体験にも配慮し、クライアント SDK を通じて高速化とセキュリティの機能をネイティブ／モバイルアプリにまで拡張します — <strong>自社構築・プライベート運用 CDN｜サブスクリプション型高防御 CDN｜自主的に制御でき、柔軟に組み合わせられる CDN ネットワーク。</strong></td>
</tr>
<tr>
<td width="180"><a href="https://go.apimart.ai/gh-gpt-load"><img src="./screenshot/apimart.png" alt="APIMart" width="150"></a></td>
<td>APIMartによる本プロジェクトへのスポンサー支援に感謝します！APIMartはAI画像・動画生成に特化した低価格APIプラットフォームで、GPT-Image-2は1枚$0.006から、1ドルで160枚以上の画像を生成できます。画像と動画の両方に対応する1つの非同期APIで、タスクを送信してIDを取得し、ポーリングまたはコールバックで結果を取得できます。数万枚規模の一括処理でもタイムアウトせず、モデルを切り替えてもコードを変更する必要はありません。従量課金制で月額料金は不要です。<a href="https://go.apimart.ai/gh-gpt-load">こちらの登録リンク</a>から登録して、すぐにご利用いただけます。</td>
</tr>
</tbody>
</table>

</details>

## GPT-Load を選ぶ理由

アプリケーション側で必要なのは、一つの Base URL と一つの AccessKey だけです。プロバイダー、アカウント、認証情報、モデル、ルーティングポリシーはすべて管理画面で設定します。

<img src="./screenshot/architecture-overview.png" alt="GPT-Load の統合アクセスとアップストリームルーティング構成図" width="860">

- **単一ゲートウェイでネイティブプロトコルを維持** — 公式 API、クラウド基盤、モデルサービス、互換中継を一元管理しながら、クライアントは OpenAI、Anthropic、Gemini のネイティブインターフェイスをそのまま使えます。
- **API キーとサブスクリプションを統一管理** — Codex、Claude、Antigravity、Grok と API キーチャネルで、認証情報管理・スケジューリング・健全性管理を共通化します。
- **スケジューリングと障害分離を内蔵** — 複数認証情報のスケジューリング、自動ウェイト、リトライ、クールダウン、ブラックリスト、セッションアフィニティにより、過負荷や失効の影響を抑えます。
- **可観測で導入しやすく、データを自己管理** — 健全性、ルート、ログ、使用量、コスト概算を確認でき、SQLite、MySQL、PostgreSQL とローカル認証情報暗号化を単一バイナリで利用できます。

## クイックスタート

> [!WARNING]
> 1.x を使用している場合は、先に[「1.x からの移行」](#1x-からの移行)を確認してください。2.0 は 1.x データを開く・インポートする・その場で移行することはできません。

### 1. サービスを起動する

Docker と Docker Compose が必要です。

```bash
git clone --depth 1 https://github.com/tbphp/gpt-load.git
cd gpt-load

cp .env.example .env
docker compose up -d
```

起動を確認します：

```bash
curl --fail http://127.0.0.1:3001/health
```

初回起動時に管理キーが自動生成されます。読み出して安全に保管してください：

```bash
docker compose exec gpt-load sh -c 'cat /app/data/auth.key'
```

<http://127.0.0.1:3001> を開き、そのキーでコンソールにログインします。

> 起動前に `.env` の `AUTH_KEY` で管理キーを明示的に指定することもできます。既定ではローカルアドレスのみを待ち受け、インターネットには直接公開されません。

### 2. 初期設定を行う

初期設定は 3 ステップです：

1. **チャネルを追加** — アップストリームサービスを選び、API キーを 1 つ以上登録します。サブスクリプションチャネルは画面の案内に従って OAuth 認可または認証情報のインポートを行います。
2. **Group を作成** — チャネルを選び、利用可能なモデルと実行ポリシーを設定します。
3. **AccessKey を作成** — アクセスを許可する Group とクライアントプロトコルを設定し、生成された AccessKey をアプリケーションに渡します。

<details>
<summary>サブスクリプションチャネルの OAuth コールバックポート</summary>

Codex、Claude、Antigravity の OAuth クライアントは固定のコールバックポートを使用します。Compose はそれらを `HOST` で設定したアドレスに公開し、既定値は `127.0.0.1` です。`HOST=0.0.0.0` を設定すると、これらのコールバックポートもホストの全ネットワークインターフェイスに公開されます。ポートはアップストリームのクライアント側で固定されているため、同一ホスト上で同時に実行できる既定の Compose インスタンスは 1 つだけです。

SSH やリモートブラウザ経由で操作する場合、ブラウザの `localhost` から GPT-Load に到達できないことがあります。その場合はコールバック URL 全体を認可ダイアログに貼り付けることでフローを完了できます。

</details>

## 画面プレビュー

**グループ概要** — チャネル、モデル、認証情報数、健全性をまとめて確認

<img src="./screenshot/groups-overview.png" alt="GPT-Load グループ概要" width="860">

**サブスクリプションアカウント** — アカウントの可用性、クォータ期間、リセット時刻、実行診断を確認

<img src="./screenshot/subscription-accounts.png" alt="GPT-Load サブスクリプションアカウントとクォータ状態" width="860">

**AccessKey 読み取り専用ホーム** — AccessKey でログインし、そのキーに限定されたグループ、モデル、リクエスト、使用量、費用上限を表示

<img src="./screenshot/access-key-home.png" alt="GPT-Load AccessKey 読み取り専用ホーム" width="860">

**使用量とコスト** — リクエスト傾向、キャッシュヒット率、Token 分類、コスト概算を確認

<img src="./screenshot/usage-cost.png" alt="GPT-Load 使用量とコストのモニタリング" width="860">

## サポート範囲

### クライアントプロトコル

| プロトコル              | 主なエンドポイント                     |
| ----------------------- | -------------------------------------- |
| OpenAI Chat Completions | `POST /v1/chat/completions`            |
| OpenAI Responses        | `/v1/responses` およびそのリソースパス |
| OpenAI Images           | `POST /v1/images/...`                  |
| OpenAI Embeddings       | `POST /v1/embeddings`                  |
| Anthropic Messages      | `POST /v1/messages`                    |
| Gemini                  | `/v1beta/models/...`                   |

各チャネルは実行可能なプロトコルと機能を明示的に宣言します。GPT-Load はサポート対象の機能間で変換を行いますが、任意のプロトコル・任意の JSON を扱う汎用コンバーターではありません。

Embeddings は初期段階では OpenAI、OpenRouter、OpenAI Compatible の API Key チャネルでのみネイティブな OpenAI 互換ワイヤーを提供し、サブスクリプションチャネルとプロトコル変換には対応していません。プロトコルフィルターを設定していない AccessKey は既存の「有効なプロトコルをすべて許可する」動作を維持するため、アップグレード後に Embeddings へのアクセス権も得ます。最小権限で運用する場合は、プロトコルフィルターを明示的に設定してください。

### 組み込みチャネル

- **公式・クラウド**：OpenAI、Anthropic、Gemini、xAI、Azure OpenAI、AWS Bedrock、Google Vertex AI
- **モデルサービス**：DeepSeek、Moonshot AI、SiliconFlow、Zhipu AI、Alibaba、Volcengine、OpenRouter、Groq
- **サブスクリプション**：Codex、Claude、Antigravity、Grok
- **カスタム**：OpenAI Compatible（任意の互換中継）

## デプロイとデータ

Docker Compose は既定でアプリケーション管理の SQLite を使用します。データは `gpt-load-data` という名前付きボリュームに保存され、データベース、`auth.key`、`encryption.key` を含みます。

> [!IMPORTANT]
> `encryption.key` はチャネル認証情報の復号に使われます。バックアップや移行の際は、データベースと鍵を**必ず一緒に保管**してください。鍵を失ったり置き換えたりすると、既存の暗号化済み認証情報は復元できません。また現行バージョンはマスターキーのローテーションに対応していません。

<details>
<summary>外部データベースを使う</summary>

統一された `DATABASE_DSN` で SQLite、MySQL、PostgreSQL に接続します：

```text
mysql://user:password@db.example:3306/gpt_load?charset=utf8mb4&collation=utf8mb4_bin
postgres://user:password@db.example:5432/gpt_load?sslmode=require
```

</details>

よく使う運用コマンド：

```bash
docker compose logs -f      # ログを確認
docker compose pull && docker compose up -d   # 最新の 2.x イメージへ更新
docker compose stop         # サービスを停止
```

公式 Compose は `ghcr.io/tbphp/gpt-load:2` を使用します。GA 前の `2` は検証済みの 2.0 Beta / RC を追跡し、GA 後は安定版 2.x のみを追跡します。イメージの完全なタグからは Git tag の `v` 接頭辞を除き（例：`2.0.0-beta.25`）、`2.0-beta` は 2.0 Beta チャネルとして残します。`latest` は引き続き 1.x を指します。

<details>
<summary>ネイティブバイナリを使う</summary>

[GitHub Releases](https://github.com/tbphp/gpt-load/releases) からプラットフォームに合ったファイルをダウンロードし、同梱の `SHA256SUMS` で検証してから使用してください：

```bash
chmod +x ./gpt-load-linux-amd64

HOST=127.0.0.1 DATA_DIR=./data ./gpt-load-linux-amd64
```

起動後 <http://127.0.0.1:3001> にアクセスします。Linux、macOS（amd64 / arm64）、Windows の計 5 種類のポータブルビルドを提供し、`gpt-load-windows-amd64.exe` は従来どおりフォアグラウンドで動作します。

Windows の一般ユーザーは代わりに `gpt-load-windows-setup.exe` を利用できます。管理者権限を一度承認すると、低権限の Windows サービスをインストールして起動し、自動起動を設定したうえで、デスクトップとスタートメニューに GPT-Load 管理画面へのショートカットを作成します。インストール中に生成された管理キーが表示されるため、画面を閉じる前に保存してください。保護されたキーは `%ProgramData%\GPT-Load\data\auth.key` に残ります。サービス設定と `.env` は `%ProgramData%\GPT-Load`、永続データは `%ProgramData%\GPT-Load\data` に保存されます。

新しい Setup による上書きインストールでは、サービスを安全に停止してから更新します。Windows のアンインストールはプログラムとサービスを削除しますが、データは保持します。上級ユーザーは `gpt-load-windows-amd64.exe service start|stop|restart|status` でもサービスを管理できます。

</details>

### 環境設定

アプリケーションは起動時にカレントディレクトリの `.env` を読み込み、既存のプロセス環境変数を優先します。特記がない限り、変更後はプロセスまたはコンテナを再起動してください。一般的な設定テンプレートは [`.env.example`](.env.example) を参照してください。

<details>
<summary>すべての環境変数を表示</summary>

| 変数 | 既定値 | 説明 |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | Native モードの待受アドレスであり、Compose のメインポートと OAuth コールバックポートをホストに公開する既定アドレスです。Compose コンテナ内部では常に `0.0.0.0` で待ち受けます。 |
| `PORT` | `3001` | HTTP サービスポート。`1–65535` である必要があり、Compose はコンテナポート、ホスト公開ポート、ヘルスチェックにも使用します。 |
| `BIND_ADDRESS` | 空、`HOST` を継承 | Compose のみ。OAuth コールバックポートを変更せず、メインサービスポートのホスト公開アドレスを上書きします。 |
| `OAUTH_CALLBACK_BIND_ADDRESS` | 空、`HOST` を継承 | Compose のみ。固定 OAuth コールバックポート `1455`、`54545`、`51121` のホスト公開アドレスを上書きします。 |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | 停止シグナル後にリクエストの完了を待つ最大時間。正の整数、単位は秒です。 |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Compose がコンテナを強制停止する前に待つ Docker duration。`GRACEFUL_SHUTDOWN_TIMEOUT` より長くすることを推奨します。 |
| `READ_TIMEOUT` | `60` | HTTP リクエストの読み取りタイムアウト。正の整数、単位は秒です。 |
| `IDLE_TIMEOUT` | `120` | HTTP keep-alive アイドル接続のタイムアウト。正の整数、単位は秒です。 |
| `DATA_DIR` | `./data` | 管理対象データベース、`auth.key`、`encryption.key`、実行状態ファイルのディレクトリ。公式 Compose では `/app/data`、Windows Setup サービスでは `%ProgramData%\GPT-Load\data` を使用します。 |
| `DATABASE_DSN` | 空、`${DATA_DIR}/gpt-load.db` を使用 | 空の場合はアプリケーション管理の SQLite を使用します。空でない場合は SQLite のパスまたは URL、MySQL URL、PostgreSQL URL に対応し、運用者管理の外部データベースとして扱います。コンテナ内のファイルパスはマウント済みディレクトリ内である必要があります。 |
| `DATABASE_MAX_OPEN_CONNECTIONS` | `10` | MySQL と PostgreSQL の最大オープン接続数。正の整数である必要があります。SQLite は常に単一接続を使用します。 |
| `DATABASE_MAX_IDLE_CONNECTIONS` | `5` | MySQL と PostgreSQL の最大アイドル接続数。正の整数かつ `DATABASE_MAX_OPEN_CONNECTIONS` 以下である必要があります。SQLite は常に単一接続を使用します。 |
| `AUTH_KEY` | 空、`${DATA_DIR}/auth.key` を読み込むか生成 | 管理画面と `/api` 管理 API の Bearer キー。データプレーンの AccessKey とは異なります。 |
| `ENCRYPTION_KEY` | 空、`${DATA_DIR}/encryption.key` を読み込むか生成 | チャネル認証情報を暗号化します。変更または紛失すると既存の認証情報を復号できないため、データベースと一緒にバックアップしてください。 |
| `HTTP_PROXY` | 空 | HTTP アップストリームリクエストの環境プロキシ。 |
| `HTTPS_PROXY` | 空 | HTTPS アップストリームリクエストの環境プロキシ。 |
| `NO_PROXY` | 空 | 環境プロキシをバイパスするホスト、ドメイン、IP のカンマ区切りリスト。 |
| `LOG_LEVEL` | `info` | `panic`、`fatal`、`error`、`warn`、`warning`、`info`、`debug`、`trace` に対応します。無効な値は警告を出して `info` に戻ります。 |
| `LOG_FORMAT` | `text` | `text` と `json` に対応します。それ以外の値では起動に失敗します。 |
| `MODELS_DEV_AUTO_SYNC_ENABLED` | 未設定、初期既定値 `true` | 未設定時は管理画面に保存された設定を使用します。設定時は Models.dev の自動同期を強制的に有効または無効にし、管理画面の同名オプションを読み取り専用にします。 |

環境プロキシは、認証情報、Group、グローバル設定のいずれにもプロキシが指定されていない場合にのみ適用されます。

</details>

## 本番運用の注意事項

- 既定では `127.0.0.1` のみを待ち受けます。リモートアクセスが必要な場合は、管理されたネットワークまたは TLS 対応のリバースプロキシ経由で公開し、ACL とファイアウォールを設定してください。
- `AUTH_KEY` と `ENCRYPTION_KEY` は厳重に管理し、実際のキーをリポジトリ、ログ、スクリーンショット、公開 Issue に含めないでください。
- 2.0 は**単一アプリケーションインスタンス**を前提に設計されています。インスタンス間で状態を共有しないため、そのままの水平スケールには対応していません。
- 使用量とコストはアップストリームの応答に基づく**概算**です。運用分析やリソース評価には使えますが、プロバイダーの請求書や会計上の照合結果とは一致しません。
- サブスクリプションチャネルはアップストリームの OAuth と互換プロトコルに依存し、アップストリームの変更に伴って調整が必要になる場合があります。利用権限のあるアカウントのみを接続し、各プロバイダーの規約に従ってください。
- OpenAI Responses で `previous_response_id`、`conversation`、既存のリソース ID に依存するステートフルなリクエストは、単一の認証情報を使う場合、またはアップストリームが認証情報間でリソースを共有している場合にのみ確実に動作します。

## 1.x からの移行

> [!WARNING]
> GPT-Load 2.0 は完全な書き直しです。1.x のデータを開く・インポートする・その場で移行することは**できません**。

2.0 は独立したデータベース、`DATA_DIR`、ポート、Docker ボリュームでデプロイしてください。検証完了後に本番トラフィックを切り替え、ロールバック期間が終了するまで既存の 1.x 環境を保持してください。1.4.x メンテナンスラインのドキュメントは[公式ドキュメント](https://www.gpt-load.com/docs?lang=ja)にあります。

## オープンソース依存

GPT-Load の一部機能は以下のプロジェクトを基盤としています。感謝いたします：

| プロジェクト                                                | 役割                                                                            | ライセンス |
| ----------------------------------------------------------- | ------------------------------------------------------------------------------- | ---------- |
| [Bifrost Core](https://github.com/maximhq/bifrost)          | 各プロバイダーの認証、リクエスト/レスポンス変換、ストリーミング、使用量の正規化 | Apache-2.0 |
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | サブスクリプションチャネルの OAuth と実行アダプター                             | MIT        |
| [Lobe Icons](https://github.com/lobehub/lobe-icons)         | 管理画面のチャネルブランドアイコン                                              | MIT        |

認証情報の保存、アカウント選択、スケジューリング、リトライ、健全性、アフィニティ、ログ、使用量ポリシーは GPT-Load が担います。サードパーティ表記は [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)、ライセンス全文は [`LICENSES/`](LICENSES/) にあり、各リリースには Go 依存関係を対象とした CycloneDX SBOM が付属します。

チャネルアイコンは対応するアップストリームプロバイダーを識別するために使用しています。商標権は各所有者に帰属し、本プロジェクトはこれらのプロバイダーと提携関係や推奨関係にはありません。

## プロジェクト支援

<table>
<tbody>
<tr>
<td align="center" width="33%">
<a href="https://openai.com/">
<picture>
<source media="(prefers-color-scheme: dark)" srcset="./screenshot/sponsor-openai-lockup-white.svg">
<source media="(prefers-color-scheme: light)" srcset="./screenshot/sponsor-openai-lockup-black.svg">
<img src="./screenshot/sponsor-openai-lockup-black.svg" alt="OpenAI" width="120">
</picture>
</a>
<br><sub>プラットフォーム支援</sub>
</td>
<td align="center" width="33%">
<a href="https://linux.do"><img src="./screenshot/l.png" alt="LINUX DO" width="120"></a>
<br><sub>コミュニティ支援</sub>
</td>
<td align="center" width="33%">
<a href="https://www.digitalocean.com/?refcode=3d52cff21342&utm_campaign=Referral_Invite&utm_medium=Referral_Program&utm_source=badge"><img src="https://web-platforms.sfo2.cdn.digitaloceanspaces.com/WWW/Badge%202.svg" alt="DigitalOcean" width="120"></a>
<br><sub>インフラ支援</sub>
</td>
</tr>
</tbody>
</table>

---

[MIT License](LICENSE) · [サードパーティ表記](THIRD_PARTY_NOTICES.md) · [セキュリティポリシー](SECURITY.md)
