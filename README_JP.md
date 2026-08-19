<div align="center">

<img src="./web/public/favicon.svg" alt="GPT-Load" width="96">

# GPT-Load

**マルチチャネル・マルチ認証情報向けのセルフホスト AI ゲートウェイ**

API キー、サブスクリプションアカウント、トラフィック制御、障害処理、リクエストログ、使用量集計を一つの入口にまとめます。

[English](README.md) · [中文](README_CN.md) · 日本語

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
[![Docker](https://img.shields.io/badge/Docker-ghcr.io%2Ftbphp%2Fgpt--load%3A2-2496ED?logo=docker&logoColor=white)](https://github.com/tbphp/gpt-load/pkgs/container/gpt-load)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp/gpt-load | Trendshift" width="220" height="48"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" width="220" height="47"/></a>

</div>

---

アプリケーション側で必要なのは、一つの Base URL と一つの AccessKey だけです。プロバイダー、アカウント、認証情報、モデル、ルーティングポリシーはすべて管理画面で設定します。

```mermaid
flowchart LR
    C["アプリ / AI クライアント"]
    G["GPT-Load<br/>————————<br/>ネイティブプロトコル入口<br/>スケジューリング · リトライ · 健全性隔離<br/>ログ · 使用量 · コスト概算"]
    U1["公式 API"]
    U2["クラウド基盤"]
    U3["互換中継"]
    U4["サブスクリプション"]

    C -->|"一つの Base URL<br/>一つの AccessKey"| G
    G --> U1
    G --> U2
    G --> U3
    G --> U4
```

## GPT-Load を選ぶ理由

- **複数のアップストリームを一元管理** — 公式 API、クラウド基盤、主要なモデルサービス、OpenAI 互換中継を同じゲートウェイで管理できます。
- **API キーとサブスクリプションを同じ仕組みで扱う** — Codex、Claude、Antigravity、Grok などのサブスクリプションチャネルも、API キーチャネルと同じ認証情報管理・スケジューリング・健全性管理を使います。
- **認証情報プールを使い切る** — 複数認証情報のスケジューリング、自動ウェイト、リトライ、クールダウン、ブラックリスト、セッションアフィニティにより、単一の認証情報の過負荷や失効による影響を抑えます。
- **クライアントのネイティブプロトコルを維持** — OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Gemini ネイティブ API をそのまま利用でき、コード変更は不要です。
- **すべての呼び出しを可視化** — 健全性状態、ルート検査、リクエストログ、使用量集計、モデル別コスト概算により、問題の特定と消費量の把握が容易になります。
- **導入が簡単でデータは自己管理** — 管理画面は単一の Go バイナリに組み込み済み。既定は SQLite で、MySQL や PostgreSQL にも接続できます。チャネル認証情報はローカルで暗号化して保存されます。

## 画面プレビュー

**ホーム** — グループと認証情報の概要、クライアントへのワンクリック接続、直近 30 日のコスト概算

<img src="./screenshot/screenshot1.png" alt="GPT-Load ホーム">

**モニタリング** — リクエスト数、キャッシュ率、トークン分類、コスト概算、使用量の品質

<img src="./screenshot/screenshot2.png" alt="GPT-Load モニタリング">

<!-- 【TODO: サブスクリプションチャネルのスクリーンショット】
     screenshot3.png はサブスクリプションのクォータウィンドウと診断情報を示す重要な
     訴求ポイントだが、アカウントカードに実際のメールアドレスが平文で含まれている。
     サンプルアドレスに差し替えて撮り直すか、マスクすること。
     対応後ここに挿入し、3 言語を同期する。 -->

## サポート範囲

### クライアントプロトコル

| プロトコル | 主なエンドポイント |
|---|---|
| OpenAI Chat Completions | `POST /v1/chat/completions` |
| OpenAI Responses | `/v1/responses` およびそのリソースパス |
| Anthropic Messages | `POST /v1/messages` |
| Gemini | `/v1beta/models/...` |

各チャネルは実行可能なプロトコルと機能を明示的に宣言します。GPT-Load はサポート対象の機能間で変換を行いますが、任意のプロトコル・任意の JSON を扱う汎用コンバーターではありません。

### 組み込みチャネル

<details>
<summary>組み込みチャネル 20 種すべてを表示</summary>

- **公式・クラウド**：OpenAI、Anthropic、Gemini、xAI、Azure OpenAI、AWS Bedrock、Google Vertex AI
- **モデルサービス**：DeepSeek、Moonshot AI、SiliconFlow、Zhipu AI、Alibaba、Volcengine、OpenRouter、Groq
- **サブスクリプション**：Codex、Claude、Antigravity、Grok
- **カスタム**：OpenAI Compatible（任意の互換中継）

</details>

## クイックスタート

### 1. サービスを起動する

Docker と Docker Compose が必要です。

```bash
git clone --depth 1 --branch v2 https://github.com/tbphp/gpt-load.git
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

コンソールの設定は 3 層構造です：

```mermaid
flowchart LR
    A["① チャネル<br/>API キー登録<br/>または OAuth 認可"] --> B["② Group<br/>チャネル選択<br/>モデルとポリシー設定"] --> C["③ AccessKey<br/>Group とプロトコル選択<br/>アプリへ渡す"]
```

1. **チャネルを追加** — アップストリームサービスを選び、API キーを 1 つ以上登録します。サブスクリプションチャネルは画面の案内に従って OAuth 認可または認証情報のインポートを行います。
2. **Group を作成** — チャネルを選び、利用可能なモデルと実行ポリシーを設定します。
3. **AccessKey を作成** — アクセスを許可する Group とクライアントプロトコルを設定し、生成された AccessKey をアプリケーションに渡します。

<details>
<summary>サブスクリプションチャネルの OAuth コールバックポート</summary>

Codex、Claude、Antigravity の OAuth クライアントは固定のコールバックポートを使用します。Compose は既定でそれぞれ `127.0.0.1:1455`、`127.0.0.1:54545`、`127.0.0.1:51121` に公開します。ポートはアップストリームのクライアント側で固定されているため、同一ホスト上で同時に実行できる既定の Compose インスタンスは 1 つだけです。

SSH やリモートブラウザ経由で操作する場合、ブラウザの `localhost` から GPT-Load に到達できないことがあります。その場合はコールバック URL 全体を認可ダイアログに貼り付けることでフローを完了できます。

</details>

### 3. 最初のリクエストを送る

AccessKey とモデル ID はコンソールの実際の値に置き換えてください：

```bash
export GPT_LOAD_ACCESS_KEY="your-access-key"

curl http://127.0.0.1:3001/v1/chat/completions \
  -H "Authorization: Bearer ${GPT_LOAD_ACCESS_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "your-model-id",
    "messages": [{ "role": "user", "content": "こんにちは、自己紹介をお願いします。" }]
  }'
```

## 既存クライアントからの利用

OpenAI SDK や OpenAI 互換クライアントでは、通常 2 か所を変更するだけです：

```text
Base URL: http://127.0.0.1:3001/v1
API Key:  GPT-Load で作成した AccessKey
```

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:3001/v1",
    api_key="your-access-key",
)

response = client.responses.create(
    model="your-model-id",
    input="こんにちは",
    store=False,
)

print(response.output_text)
```

Anthropic クライアントは `/v1/messages`、Gemini クライアントは `/v1beta/models/...` を使います。認証は各クライアントの慣習どおり、`Authorization: Bearer`、`x-api-key`、`x-goog-api-key`、または Gemini の `key` クエリパラメータで行えます。

## デプロイとデータ

Docker Compose は既定でアプリケーション管理の SQLite を使用します。データは `gpt-load-data` という名前付きボリュームに保存され、データベース、`auth.key`、`encryption.key` を含みます。

> [!IMPORTANT]
> `encryption.key` はチャネル認証情報の復号に使われます。バックアップや移行の際は、データベースと鍵を**必ず一緒に保管**してください。鍵を失ったり置き換えたりすると、既存の暗号化済み認証情報は復元できません。また現行バージョンはマスターキーのローテーションに対応していません。

外部データベースを使う場合は、統一された `DATABASE_DSN` で SQLite、MySQL、PostgreSQL に接続します：

```text
mysql://user:password@db.example:3306/gpt_load?charset=utf8mb4&collation=utf8mb4_bin
postgres://user:password@db.example:5432/gpt_load?sslmode=require
```

設定項目の詳細は [`.env.example`](.env.example) を参照してください。

よく使う運用コマンド：

```bash
docker compose logs -f      # ログを確認
docker compose pull && docker compose up -d   # 最新の 2.x イメージへ更新
docker compose stop         # サービスを停止
```

公式の 2.x Compose は `ghcr.io/tbphp/gpt-load:v2beta` を使用し、`latest` タグには依存しません。

<details>
<summary>ネイティブバイナリを使う</summary>

[GitHub Releases](https://github.com/tbphp/gpt-load/releases) からプラットフォームに合ったファイルをダウンロードし、同梱の `SHA256SUMS` で検証してから使用してください：

```bash
chmod +x ./gpt-load-linux-amd64
mkdir -p ./data

HOST=127.0.0.1 DATA_DIR=./data ./gpt-load-linux-amd64
```

起動後 <http://127.0.0.1:3001> にアクセスします。Linux、macOS（amd64 / arm64）、Windows の計 5 種類のビルドを提供しています。

</details>

## 利用前の注意

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

| プロジェクト | 役割 | ライセンス |
|---|---|---|
| [Bifrost Core](https://github.com/maximhq/bifrost) | 各プロバイダーの認証、リクエスト/レスポンス変換、ストリーミング、使用量の正規化 | Apache-2.0 |
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | サブスクリプションチャネルの OAuth と実行アダプター | MIT |
| [Lobe Icons](https://github.com/lobehub/lobe-icons) | 管理画面のチャネルブランドアイコン | MIT |

認証情報の保存、アカウント選択、スケジューリング、リトライ、健全性、アフィニティ、ログ、使用量ポリシーは GPT-Load が担います。サードパーティ表記は [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)、ライセンス全文は [`LICENSES/`](LICENSES/) にあり、各リリースには Go 依存関係を対象とした CycloneDX SBOM が付属します。

チャネルアイコンは対応するアップストリームプロバイダーを識別するために使用しています。商標権は各所有者に帰属し、本プロジェクトはこれらのプロバイダーと提携関係や推奨関係にはありません。

## フィードバックと貢献

問題や機能の提案は [GitHub Issue](https://github.com/tbphp/gpt-load/issues) へお寄せください。セキュリティ脆弱性は [SECURITY.md](SECURITY.md) の手順に従って報告してください。

GPT-Load が役に立ったら、Star をいただけると嬉しいです。

## スポンサーと支援

<table>
<tbody>
<tr>
<td width="180" align="center"><a href="https://openai.com/"><img src="./screenshot/sponsor-openai.svg" alt="OpenAI" width="56"></a></td>
<td>本プロジェクトへのスポンサー支援に感謝します（OpenAI）。</td>
</tr>
<tr>
<td width="180"><a href="https://linux.do"><img src="./screenshot/l.png" alt="LINUX DO" width="150"></a></td>
<td>LINUX DO コミュニティの支援に感謝します。</td>
</tr>
<tr>
<td width="180"><a href="https://www.digitalocean.com/?refcode=3d52cff21342&utm_campaign=Referral_Invite&utm_medium=Referral_Program&utm_source=badge"><img src="https://web-platforms.sfo2.cdn.digitaloceanspaces.com/WWW/Badge%202.svg" alt="DigitalOcean" width="150"></a></td>
<td>本プロジェクトは DigitalOcean の支援を受けています。</td>
</tr>
</tbody>
</table>

## ライセンス

GPT-Load は [MIT License](LICENSE) で公開されています。
