<div align="center">

<img src="./web/public/favicon.svg" alt="GPT-Load" width="96">

# GPT-Load

**A self-hosted AI gateway for multi-channel, multi-credential setups**

API keys, subscription accounts, traffic scheduling, failure handling, request logs, and usage accounting — behind a single entry point.

English · [中文](README_CN.md) · [日本語](README_JP.md)

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
[![Docker](https://img.shields.io/badge/Docker-ghcr.io%2Ftbphp%2Fgpt--load%3A2-2496ED?logo=docker&logoColor=white)](https://github.com/tbphp/gpt-load/pkgs/container/gpt-load)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp/gpt-load | Trendshift" width="220" height="48"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" width="220" height="47"/></a>

</div>

---

Your application only needs one base URL and one AccessKey. Providers, accounts, credentials, models, and routing policy are all configured in the management UI.

```mermaid
flowchart LR
    C["Application / AI client"]
    G["GPT-Load<br/>————————<br/>Native protocol entry<br/>Scheduling · Retry · Health isolation<br/>Logs · Usage · Cost estimates"]
    U1["Official APIs"]
    U2["Cloud platforms"]
    U3["Compatible relays"]
    U4["Subscription accounts"]

    C -->|"One base URL<br/>One AccessKey"| G
    G --> U1
    G --> U2
    G --> U3
    G --> U4
```

## Why GPT-Load

- **One gateway for many upstreams** — Official APIs, cloud platforms, popular model services, and OpenAI-compatible relays are all managed in one place.
- **API keys and subscription accounts share one mechanism** — Subscription channels like Codex, Claude, Antigravity, and Grok use the same credential management, scheduling, and health system as API key channels.
- **Get the most out of a credential pool** — Multi-credential scheduling, automatic weighting, retries, cooldown, blacklisting, and session affinity reduce the impact of a single overloaded or failing credential.
- **Clients keep their native protocol** — Applications keep using OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, or the Gemini native API without code changes.
- **Every call is visible** — Health state, route inspection, request logs, usage rollups, and per-model cost estimates make problems and consumption easy to trace.
- **Simple to deploy, your data stays yours** — The management UI is embedded in a single Go binary; SQLite by default, MySQL or PostgreSQL optional; channel credentials are encrypted at rest locally.

## Screenshots

**Home** — Groups and credentials at a glance, one-click client setup, 30-day cost estimate

<img src="./screenshot/screenshot1.png" alt="GPT-Load home">

**Monitoring** — Request volume, cache rate, token breakdown, cost estimates, usage quality

<img src="./screenshot/screenshot2.png" alt="GPT-Load monitoring">

<!-- 【TODO: subscription channel screenshot】
     screenshot3.png shows subscription quota windows and diagnostics, a core
     selling point, but the account card contains a real email address in plain
     text. Replace it with a sample address and re-shoot, or redact it.
     Insert here once handled; keep all three languages in sync. -->

## Scope

### Client protocols

| Protocol | Main entry |
|---|---|
| OpenAI Chat Completions | `POST /v1/chat/completions` |
| OpenAI Responses | `/v1/responses` and its resource paths |
| Anthropic Messages | `POST /v1/messages` |
| Gemini | `/v1beta/models/...` |

Each channel declares exactly which protocols and capabilities it can execute. GPT-Load converts between supported capabilities, but it is not a general-purpose any-protocol, any-JSON translator.

### Built-in channels

<details>
<summary>Show all 20 built-in channels</summary>

- **Official and cloud** — OpenAI, Anthropic, Gemini, xAI, Azure OpenAI, AWS Bedrock, Google Vertex AI
- **Model services** — DeepSeek, Moonshot AI, SiliconFlow, Zhipu AI, Alibaba, Volcengine, OpenRouter, Groq
- **Subscription** — Codex, Claude, Antigravity, Grok
- **Custom** — OpenAI Compatible (any compatible relay)

</details>

## Quick start

### 1. Start the service

Requires Docker and Docker Compose.

```bash
git clone --depth 1 --branch v2 https://github.com/tbphp/gpt-load.git
cd gpt-load

cp .env.example .env
docker compose up -d
```

Confirm the service is up:

```bash
curl --fail http://127.0.0.1:3001/health
```

The first start generates a management key. Read it and store it safely:

```bash
docker compose exec gpt-load sh -c 'cat /app/data/auth.key'
```

Open <http://127.0.0.1:3001> and sign in to the console with that key.

> You can also set `AUTH_KEY` explicitly in `.env` before starting. By default the service listens on the loopback address only and is not exposed to the internet.

### 2. Initial configuration

The console has three configuration layers:

```mermaid
flowchart LR
    A["① Channel<br/>Add API keys<br/>or complete OAuth"] --> B["② Group<br/>Pick a channel<br/>Set models and policy"] --> C["③ AccessKey<br/>Pick groups and protocols<br/>Hand it to your app"]
```

1. **Add a channel** — Choose an upstream service and add one or more API keys. For subscription channels, complete the OAuth flow or import credentials as prompted.
2. **Create a group** — Pick a channel, then configure available models and runtime policy.
3. **Create an AccessKey** — Set the groups and client protocols it may use, then give the generated AccessKey to your application.

<details>
<summary>OAuth callback ports for subscription channels</summary>

The Codex, Claude, and Antigravity OAuth clients use fixed callback ports. Compose publishes them on the address configured by `HOST`, which defaults to `127.0.0.1`; setting `HOST=0.0.0.0` also publishes these callback ports on all host interfaces. Because the ports are fixed by the upstream clients, only one default Compose instance can run on a host at a time.

When working over SSH or from a remote browser, the browser's `localhost` may not reach GPT-Load — paste the full callback URL into the authorization dialog to finish the flow.

</details>

### 3. Send your first request

Replace the AccessKey and model ID with the real values from your console:

```bash
export GPT_LOAD_ACCESS_KEY="your-access-key"

curl http://127.0.0.1:3001/v1/chat/completions \
  -H "Authorization: Bearer ${GPT_LOAD_ACCESS_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "your-model-id",
    "messages": [{ "role": "user", "content": "Hello, introduce yourself." }]
  }'
```

## Using existing clients

For the OpenAI SDK or any OpenAI-compatible client, two settings usually change:

```text
Base URL: http://127.0.0.1:3001/v1
API Key:  the AccessKey created in GPT-Load
```

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:3001/v1",
    api_key="your-access-key",
)

response = client.responses.create(
    model="your-model-id",
    input="Hello",
    store=False,
)

print(response.output_text)
```

Anthropic clients use `/v1/messages` and Gemini clients use `/v1beta/models/...`. Authenticate the way each client normally does: `Authorization: Bearer`, `x-api-key`, `x-goog-api-key`, or Gemini's `key` query parameter.

## Deployment and data

Docker Compose uses application-managed SQLite by default. Data lives in the `gpt-load-data` named volume and includes the database, `auth.key`, and `encryption.key`.

> [!IMPORTANT]
> `encryption.key` decrypts channel credentials. When backing up or migrating, the database and the key **must be kept together**. Once the key is lost or replaced, existing encrypted credentials cannot be recovered, and this version does not support master key rotation.

For an external database, use the unified `DATABASE_DSN` to connect SQLite, MySQL, or PostgreSQL:

```text
mysql://user:password@db.example:3306/gpt_load?charset=utf8mb4&collation=utf8mb4_bin
postgres://user:password@db.example:5432/gpt_load?sslmode=require
```

See [`.env.example`](.env.example) for the full configuration reference.

Common operations:

```bash
docker compose logs -f      # view logs
docker compose pull && docker compose up -d   # update to the latest 2.x image
docker compose stop         # stop the service
```

The official 2.x Compose file uses `ghcr.io/tbphp/gpt-load:v2beta` and does not rely on the `latest` tag.

<details>
<summary>Using a native binary</summary>

Download the build for your platform from [GitHub Releases](https://github.com/tbphp/gpt-load/releases), and verify it against the bundled `SHA256SUMS` first:

```bash
chmod +x ./gpt-load-linux-amd64

HOST=127.0.0.1 DATA_DIR=./data ./gpt-load-linux-amd64
```

Then open <http://127.0.0.1:3001>. Builds are provided for five targets across Linux, macOS (amd64 / arm64), and Windows.

</details>

## Before you go to production

- The service listens on `127.0.0.1` only by default. For remote access, expose it through a controlled network or a TLS reverse proxy, and configure ACLs and firewall rules.
- Manage `AUTH_KEY` and `ENCRYPTION_KEY` carefully. Never commit real keys to a repository, log, screenshot, or public issue.
- 2.0 is designed for a **single application instance**. Instances do not share state, so horizontal scaling is not supported.
- Usage and cost are **estimates** derived from upstream responses. They support operational analysis and capacity planning, and do not equal a provider invoice or a financial reconciliation.
- Subscription channels depend on upstream OAuth and compatibility protocols and may change as upstreams change. Only connect accounts you are entitled to use, and follow each provider's terms.
- In OpenAI Responses, stateful requests relying on `previous_response_id`, `conversation`, or an existing resource ID are only reliable with a single credential, or with an upstream that shares resources across credentials.

## Moving from 1.x

> [!WARNING]
> GPT-Load 2.0 is a complete rewrite. It **cannot** open, import, or migrate 1.x data in place.

Deploy 2.0 with its own database, `DATA_DIR`, port, and Docker volume. Cut traffic over only after verification, and keep the original 1.x deployment until the rollback window closes. Documentation for the 1.4.x maintenance line is at the [official docs](https://www.gpt-load.com/docs).

## Open-source dependencies

Some of GPT-Load's capabilities build on these projects, with thanks:

| Project | Role | License |
|---|---|---|
| [Bifrost Core](https://github.com/maximhq/bifrost) | Provider authentication, request/response conversion, streaming, usage normalization | Apache-2.0 |
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | OAuth and execution adapter for subscription channels | MIT |
| [Lobe Icons](https://github.com/lobehub/lobe-icons) | Channel brand icons in the management UI | MIT |

GPT-Load owns credential storage, account selection, scheduling, retry, health, affinity, logging, and usage policy. Third-party notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), full license texts in [`LICENSES/`](LICENSES/), and each release ships a CycloneDX SBOM covering the Go dependency graph.

Channel icons identify their respective upstream providers. All trademarks belong to their owners; this project is not affiliated with or endorsed by them.

## Feedback and contributing

For problems or feature ideas, open a [GitHub Issue](https://github.com/tbphp/gpt-load/issues). Report security vulnerabilities through the process in [SECURITY.md](SECURITY.md).

For community chat and usage discussion, join the [Telegram group](https://t.me/+GHpy5SwEllg3MTUx).

If GPT-Load is useful to you, a star is appreciated.

## Sponsors and support

<table>
<tbody>
<tr>
<td width="180"><a href="https://go.apimart.ai/gh-gpt-load"><img src="./screenshot/apimart.png" alt="APIMart" width="150"></a></td>
<td>Thanks to APIMart for sponsoring this project! APIMart is a low-cost API platform for AI image &amp; video generation — GPT-Image-2 from $0.006/image, 160+ images per dollar. One async API covers both image and video: submit a task, get an ID, fetch results via polling or callback. Batch tens of thousands of images without timeouts, switch models without changing code. Pay-as-you-go with no monthly fee — <a href="https://go.apimart.ai/gh-gpt-load">sign up here</a> to get started.</td>
</tr>
<tr>
<td width="180" align="center"><a href="https://openai.com/"><img src="./screenshot/sponsor-openai.svg" alt="OpenAI" width="56"></a></td>
<td>Thanks to OpenAI for sponsoring this project.</td>
</tr>
<tr>
<td width="180"><a href="https://linux.do"><img src="./screenshot/l.png" alt="LINUX DO" width="150"></a></td>
<td>Thanks to the LINUX DO community for their support.</td>
</tr>
<tr>
<td width="180"><a href="https://www.digitalocean.com/?refcode=3d52cff21342&utm_campaign=Referral_Invite&utm_medium=Referral_Program&utm_source=badge"><img src="https://web-platforms.sfo2.cdn.digitaloceanspaces.com/WWW/Badge%202.svg" alt="DigitalOcean" width="150"></a></td>
<td>This project is supported by DigitalOcean.</td>
</tr>
</tbody>
</table>

## License

GPT-Load is released under the [MIT License](LICENSE).
