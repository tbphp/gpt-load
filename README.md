<div align="center">

<img src="./web/public/favicon.svg" alt="GPT-Load" width="96">

# GPT-Load

**A self-hosted AI gateway for multi-channel, multi-credential setups**

API keys, subscription accounts, traffic scheduling, failure handling, request logs, and usage accounting — behind a single entry point.

English · [中文](README_CN.md) · [日本語](README_JP.md)

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
[![Docker](https://img.shields.io/badge/Docker-ghcr.io%2Ftbphp%2Fgpt--load%3A2-2496ED?logo=docker&logoColor=white)](https://github.com/tbphp/gpt-load/pkgs/container/gpt-load)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

</div>

---

Your application only needs one base URL and one AccessKey. Providers, accounts, credentials, models, and routing policy are all configured in the management UI.

<img src="./screenshot/architecture-overview.png" alt="GPT-Load unified access and upstream routing architecture" width="860">

## Sponsors and support

<table>
<tbody>
<tr>
<td width="180"><a href="https://go.apimart.ai/gh-gpt-load"><img src="./screenshot/apimart.png" alt="APIMart" width="150"></a></td>
<td>Thanks to APIMart for sponsoring this project! APIMart is a low-cost API platform for AI image &amp; video generation — GPT-Image-2 from $0.006/image, 160+ images per dollar. One async API covers both image and video: submit a task, get an ID, fetch results via polling or callback. Batch tens of thousands of images without timeouts, switch models without changing code. Pay-as-you-go with no monthly fee — <a href="https://go.apimart.ai/gh-gpt-load">sign up here</a> to get started.</td>
</tr>
<tr>
<td width="180">
<a href="https://openai.com/">
<picture>
<source media="(prefers-color-scheme: dark)" srcset="./screenshot/sponsor-openai-lockup-white.svg">
<source media="(prefers-color-scheme: light)" srcset="./screenshot/sponsor-openai-lockup-black.svg">
<img src="./screenshot/sponsor-openai-lockup-black.svg" alt="OpenAI" width="150">
</picture>
</a>
</td>
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

## Why GPT-Load

- **One gateway, native protocols** — Manage official APIs, cloud platforms, model services, and compatible relays together while clients keep their OpenAI, Anthropic, or Gemini native interfaces.
- **One mechanism for API keys and subscriptions** — Codex, Claude, Antigravity, Grok, and API-key channels share credential management, scheduling, and health handling.
- **Scheduling and failure isolation built in** — Multi-credential scheduling, automatic weighting, retries, cooldown, blacklisting, and session affinity reduce the impact of overloaded or failing credentials.
- **Observable, self-hosted, and simple to deploy** — Inspect health, routes, logs, usage, and cost estimates in an embedded UI backed by SQLite, MySQL, or PostgreSQL with local credential encryption.

## Quick start

> [!WARNING]
> If you are using 1.x, read [Moving from 1.x](#moving-from-1x) first. 2.0 cannot open, import, or migrate 1.x data in place.

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

Initial setup takes three steps:

1. **Add a channel** — Choose an upstream service and add one or more API keys. For subscription channels, complete the OAuth flow or import credentials as prompted.
2. **Create a group** — Pick a channel, then configure available models and runtime policy.
3. **Create an AccessKey** — Set the groups and client protocols it may use, then give the generated AccessKey to your application.

<details>
<summary>OAuth callback ports for subscription channels</summary>

The Codex, Claude, and Antigravity OAuth clients use fixed callback ports. Compose publishes them on the address configured by `HOST`, which defaults to `127.0.0.1`; setting `HOST=0.0.0.0` also publishes these callback ports on all host interfaces. Because the ports are fixed by the upstream clients, only one default Compose instance can run on a host at a time.

When working over SSH or from a remote browser, the browser's `localhost` may not reach GPT-Load — paste the full callback URL into the authorization dialog to finish the flow.

</details>

## Screenshots

**Groups** — View channels, models, credential counts, and health in one place

<img src="./screenshot/groups-overview.png" alt="GPT-Load groups overview" width="860">

**Subscription accounts** — Track account availability, quota windows, reset times, and runtime diagnostics

<img src="./screenshot/subscription-accounts.png" alt="GPT-Load subscription accounts and quota status" width="860">

**AccessKey read-only home** — Sign in with an AccessKey to view only its own groups, models, requests, usage, and cost allowance

<img src="./screenshot/access-key-home.png" alt="GPT-Load AccessKey read-only home" width="860">

**Usage and cost** — Review request trends, cache hit rate, token categories, and cost estimates

<img src="./screenshot/usage-cost.png" alt="GPT-Load usage and cost monitoring" width="860">

## Scope

### Client protocols

| Protocol                | Main entry                             |
| ----------------------- | -------------------------------------- |
| OpenAI Chat Completions | `POST /v1/chat/completions`            |
| OpenAI Responses        | `/v1/responses` and its resource paths |
| Anthropic Messages      | `POST /v1/messages`                    |
| Gemini                  | `/v1beta/models/...`                   |

Each channel declares exactly which protocols and capabilities it can execute. GPT-Load converts between supported capabilities, but it is not a general-purpose any-protocol, any-JSON translator.

### Built-in channels

- **Official and cloud** — OpenAI, Anthropic, Gemini, xAI, Azure OpenAI, AWS Bedrock, Google Vertex AI
- **Model services** — DeepSeek, Moonshot AI, SiliconFlow, Zhipu AI, Alibaba, Volcengine, OpenRouter, Groq
- **Subscription** — Codex, Claude, Antigravity, Grok
- **Custom** — OpenAI Compatible (any compatible relay)

## Deployment and data

Docker Compose uses application-managed SQLite by default. Data lives in the `gpt-load-data` named volume and includes the database, `auth.key`, and `encryption.key`.

> [!IMPORTANT]
> `encryption.key` decrypts channel credentials. When backing up or migrating, the database and the key **must be kept together**. Once the key is lost or replaced, existing encrypted credentials cannot be recovered, and this version does not support master key rotation.

<details>
<summary>Using an external database</summary>

Use the unified `DATABASE_DSN` to connect SQLite, MySQL, or PostgreSQL:

```text
mysql://user:password@db.example:3306/gpt_load?charset=utf8mb4&collation=utf8mb4_bin
postgres://user:password@db.example:5432/gpt_load?sslmode=require
```

</details>

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

### Environment configuration

At startup, the application reads `.env` in the current directory; existing process environment variables take precedence. Unless noted otherwise, changes require restarting the process or container; see [`.env.example`](.env.example) for the common configuration template.

<details>
<summary>Show all environment variables</summary>

| Variable | Default | Description |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | Native listening address, and the default host address for Compose's main port and OAuth callback ports; Compose always listens on `0.0.0.0` inside the container. |
| `PORT` | `3001` | HTTP service port, must be `1–65535`; Compose also uses it for the container port, host publishing, and health check. |
| `BIND_ADDRESS` | Empty, inherits `HOST` | Compose only; overrides the host publishing address for the main service port without changing OAuth callback ports. |
| `OAUTH_CALLBACK_BIND_ADDRESS` | Empty, inherits `HOST` | Compose only; overrides the host publishing address for the fixed OAuth callback ports `1455`, `54545`, and `51121`. |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | Maximum time to wait for requests after a stop signal, positive integer in seconds. |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Docker duration to wait before Compose force-stops the container; should be longer than `GRACEFUL_SHUTDOWN_TIMEOUT`. |
| `READ_TIMEOUT` | `60` | HTTP request read timeout, positive integer in seconds. |
| `IDLE_TIMEOUT` | `120` | HTTP keep-alive idle connection timeout, positive integer in seconds. |
| `DATA_DIR` | `./data` | Directory for the managed database, `auth.key`, `encryption.key`, and runtime state; official Compose uses `/app/data`. |
| `DATABASE_DSN` | Empty, uses `${DATA_DIR}/gpt-load.db` | Empty uses application-managed SQLite; non-empty values support SQLite paths or URLs, MySQL URLs, and PostgreSQL URLs, and are treated as operator-managed external databases. Container file paths must be inside a mounted directory. |
| `AUTH_KEY` | Empty, reads or generates `${DATA_DIR}/auth.key` | Bearer key for the management UI and `/api` management API, not a data-plane AccessKey. |
| `ENCRYPTION_KEY` | Empty, reads or generates `${DATA_DIR}/encryption.key` | Encrypts channel credentials; changing or losing it makes existing credentials undecryptable, so back it up with the database. |
| `HTTP_PROXY` | Empty | Environment proxy for HTTP upstream requests. |
| `HTTPS_PROXY` | Empty | Environment proxy for HTTPS upstream requests. |
| `NO_PROXY` | Empty | Comma-separated hosts, domains, or IPs that bypass the environment proxy. |
| `LOG_LEVEL` | `info` | Supports `panic`, `fatal`, `error`, `warn`, `warning`, `info`, `debug`, and `trace`; invalid values warn and fall back to `info`. |
| `LOG_FORMAT` | `text` | Supports `text` and `json`; any other value fails startup. |
| `MODELS_DEV_AUTO_SYNC_ENABLED` | Unset, initial default `true` | When unset, uses the persisted management UI setting; when set, forces Models.dev auto-sync on or off and makes the same UI option read-only. |

Environment proxies apply only when no proxy is specified on the credential, group, or global settings.

</details>

## Production considerations

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

| Project                                                     | Role                                                                                 | License    |
| ----------------------------------------------------------- | ------------------------------------------------------------------------------------ | ---------- |
| [Bifrost Core](https://github.com/maximhq/bifrost)          | Provider authentication, request/response conversion, streaming, usage normalization | Apache-2.0 |
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | OAuth and execution adapter for subscription channels                                | MIT        |
| [Lobe Icons](https://github.com/lobehub/lobe-icons)         | Channel brand icons in the management UI                                             | MIT        |

GPT-Load owns credential storage, account selection, scheduling, retry, health, affinity, logging, and usage policy. Third-party notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), full license texts in [`LICENSES/`](LICENSES/), and each release ships a CycloneDX SBOM covering the Go dependency graph.

Channel icons identify their respective upstream providers. All trademarks belong to their owners; this project is not affiliated with or endorsed by them.

## Feedback and contributing

For problems or feature ideas, open a [GitHub Issue](https://github.com/tbphp/gpt-load/issues). Report security vulnerabilities through the process in [SECURITY.md](SECURITY.md).

For community chat and usage discussion, join the [Telegram group](https://t.me/+GHpy5SwEllg3MTUx).

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp/gpt-load | Trendshift" width="220" height="48"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" width="220" height="47"/></a>

If GPT-Load is useful to you, a star is appreciated.

## License

GPT-Load is released under the [MIT License](LICENSE).
