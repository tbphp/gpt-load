# GPT-Load

English | [中文](README_CN.md) | [日本語](README_JP.md)

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
![Go Version](https://img.shields.io/badge/Go-1.25-blue.svg)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

GPT-Load is a self-hosted AI API key aggregator and native-protocol gateway written in Go. A single binary with an embedded admin UI manages keys for OpenAI, Anthropic, Gemini, and compatible upstreams while exposing each provider's native data-plane endpoints.

For the maintained 1.4.x release documentation, visit the [official documentation](https://www.gpt-load.com/docs?lang=en).

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp%2Fgpt-load | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" style="width: 250px; height: 54px;" width="250" height="54" /></a>

## Sponsors

<table>
<tbody>
<tr>
<td width="180"><a href="https://teamorouter.com/?utm_source=gpt_load&utm_medium=referral&utm_campaign=ai_directory"><img src="./screenshot/teamorouter.png" alt="TeamoRouter" width="150"></a></td>
<td>Thanks to TeamoRouter for sponsoring this project! TeamoRouter is an enterprise-grade Agentic LLM gateway that lets developers, AI teams, and businesses access Claude Code, Codex, Gemini CLI, and other AI agents through one unified API without separate subscriptions, with discounts of up to 90%. It connects to official providers and trusted partners like OpenAI, Anthropic, Vertex, Azure, and AWS Bedrock, offering verified Agent protocol compatibility, request traceability, near-official TTFT, 99.6% SLA, and up to 5,000 QPM. It also includes centralized billing, team management, BYOK, smart routing, analytics, provider optimization, and dedicated support. Teamo Desktop enables one-click setup with no API key management or manual configuration, and new users can register via <a href="https://teamorouter.com/?utm_source=gpt_load&utm_medium=referral&utm_campaign=ai_directory">this link</a> for 10% off their first top-up.</td>
</tr>
<tr>
<td width="180"><a href="https://unity2.ai/register?source=gptload"><img src="./screenshot/unity2ai.jpg" alt="Unity2.ai" width="150"></a></td>
<td>Thanks to Unity2.ai for sponsoring this project! Unity2.ai is a high-performance AI model API relay platform for individual developers, teams, and enterprises. It has long served leading enterprises in China, handles over 30 billion token calls per day, and supports 5000 RPM high concurrency. It supports balance billing, first top-up bonuses, bundled subscriptions, enterprise invoicing, and dedicated integration support. Register via <a href="https://unity2.ai/register?source=gptload">this link</a> to receive a $2 balance; join the official group for another $10 balance, up to $12 in free credits.</td>
</tr>
<tr>
<td width="180"><a href="https://linux.do"><img src="./screenshot/l.png" alt="LINUX DO" width="150"></a></td>
<td>Thank you very much for the support from the LINUX DO community!</td>
</tr>
<tr>
<td width="180"><a href="https://www.digitalocean.com/?refcode=3d52cff21342&utm_campaign=Referral_Invite&utm_medium=Referral_Program&utm_source=badge"><img src="https://web-platforms.sfo2.cdn.digitaloceanspaces.com/WWW/Badge%202.svg" alt="DigitalOcean Referral Badge" width="150"></a></td>
<td>This project is supported by DigitalOcean.</td>
</tr>
</tbody>
</table>

## 2.0 release status

> [!WARNING]
> 2.0 is currently in release-ready closeout. This does not mean that a `v2.0.0` tag, GitHub Release, binary, or container image has been published. Check the actual release assets before deploying; a repository branch is not evidence of a successful release.

2.0 is a greenfield rewrite whose data is incompatible with 1.x. `main` remains the 1.4.x maintenance line. 2.0 does not automatically move `latest`; stable container channels use explicit `2`, `2.0`, and `v2.0.0` tags.

## 2.0 capabilities

- **Two planes:** provider-native paths on the data plane; management APIs under `/api`, with the admin UI embedded in the same Go binary.
- **Three native dialects:** OpenAI, Anthropic, and Gemini requests are forwarded in their respective protocols. GPT-Load does not translate between protocols.
- **Key and traffic management:** Groups, encrypted upstream keys, AccessKeys, model discovery, filtering and rate limits, scheduling, health state, cooldown, blacklist, and automatic weights.
- **Control and observability:** runtime settings, route inspection, health views, RequestLog, and a Chinese, English, and Japanese admin UI.
- **Usage and estimated cost:** usage extraction for the three dialects, 24-hour/30-day reports, per-request quality states, built-in prices, and user price overrides.

Prices and costs are best-effort **estimates** derived from upstream usage and the active pricing rules. They are not a billing ledger, invoice, or provider bill, and historical requests are not repriced.

## 2.0.0 support boundaries

- Correctness is guaranteed for a **single application instance** only; multi-instance coordination is not supported.
- **SQLite only**; PostgreSQL, MySQL, and other databases are not supported.
- The AccessKey and runtime configuration select the Group. A Group never appears in the data-plane URL.
- Upstream keys must be encrypted at rest with no plaintext fallback. 2.0.0 has no master-key rotation; `migrate-keys` remains an explicitly failing deferred command.
- There is no automatic 1.x migration, in-place upgrade, or reverse synchronization.
- There is no protocol conversion, online billing reconciliation, automatic price fetcher, online backup API, or backup CLI.

## Quick start

### Docker Compose

The 2.x Compose release contract uses `ghcr.io/tbphp/gpt-load:2`, container path `/app/data`, and the `gpt-load-data` named volume. It never uses `latest`. Check the current checkout first:

```console
cp .env.example .env
docker compose config
```

Continue only if the resolved configuration uses image `ghcr.io/tbphp/gpt-load:2`, sets `DATA_DIR=/app/data` and `DATABASE_DSN=/app/data/gpt-load.db`, and mounts a named volume at `/app/data`. If the checkout still resolves to `latest` or a host bind mount, the later T18 container closeout has not landed. Do not use that Compose file for a 2.0 production deployment, and do not substitute `latest`.

After those preconditions are met:

```console
docker compose up -d
curl --fail http://localhost:3001/health
# If AUTH_KEY was generated on first boot, read it once in a secure terminal
# and immediately store it in a secret manager.
docker compose exec gpt-load sh -c 'cat /app/data/auth.key'
```

The named volume preserves SQLite, `auth.key`, and `encryption.key`. Production deployments should inject explicit `AUTH_KEY` and `ENCRYPTION_KEY` values through protected secret handling. Never commit real secrets to `.env`, logs, or issues. A custom container `DATABASE_DSN` requires a Compose override with both a **container** path and a matching volume mount.

### Native binary

After publication, download the platform-matching artifact from the GitHub Release and verify it against `SHA256SUMS`. Until release assets actually exist, build from the current checkout as shown under “Build and verification”; do not assume that an artifact has been published.

Linux amd64 example:

```console
chmod +x ./gpt-load-linux-amd64
mkdir -p ./data
DATA_DIR=./data ./gpt-load-linux-amd64
```

In another terminal:

```console
curl --fail http://localhost:3001/health
```

Then open <http://localhost:3001> in a browser.

`AUTH_KEY` and `ENCRYPTION_KEY` may both be supplied explicitly. When left empty, first boot creates and reuses `${DATA_DIR}/auth.key` and `${DATA_DIR}/encryption.key`, respectively. The application logs generated file paths, never their secret contents.

## Native data plane

Data-plane requests use an AccessKey. Provider-compatible credentials are accepted through `Authorization: Bearer`, `x-api-key`, `x-goog-api-key`, or Gemini's `key` query parameter as appropriate.

| Provider | Method and path | Behavior |
|---|---|---|
| OpenAI | `POST /v1/chat/completions` | Native OpenAI Chat Completions request |
| OpenAI / Anthropic | `GET /v1/models` | OpenAI shape by default; Anthropic shape when `anthropic-version` is present |
| Anthropic | `POST /v1/messages` | Native Anthropic Messages request |
| Gemini | `GET /v1beta/models` | Native Gemini model list |
| Gemini | `POST /v1beta/models/{model}:generateContent` | Gemini non-streaming generation |
| Gemini | `POST /v1beta/models/{model}:streamGenerateContent` | Gemini streaming generation |

GPT-Load does not translate one dialect into another. The AccessKey and runtime configuration select the Group; it is not passed as a URL path segment.

## Management, usage, and cost

The admin UI is served at `/`, and management APIs are under `/api`; both use `AUTH_KEY`. The UI covers Groups, upstream keys, AccessKeys, runtime settings, health, logs, route inspection, Usage, and model-price management. Current code and UI are the management API reference; this README intentionally avoids copying a route list that can drift.

Usage/Cost quality boundaries:

- `complete + priced` requests contribute to default token and estimated-cost totals.
- `missing`, `partial`, and `unpriced` requests still contribute to request and quality counts but not to default token/cost totals. `complete + unpriced` requests are never assigned guessed prices.
- A clean EOF on a stream does not guarantee complete usage, and compatible relays may omit the provider's official terminal usage.
- Price changes affect future writes only. Historical RequestLog and UsageStat rows are not recalculated.
- Current-process dropped/write-failure counters and durable database-window aggregates have different scopes.

## Core configuration

| Variable | Default | Purpose |
|---|---|---|
| `HOST` | `0.0.0.0` | HTTP listen address |
| `PORT` | `3001` | HTTP listen port |
| `DATA_DIR` | `./data` | Persistent directory for a native process; fixed to `/app/data` by the container release contract |
| `DATABASE_DSN` | `${DATA_DIR}/gpt-load.db` | SQLite path/DSN; a container path must exist in the container namespace and have a matching volume |
| `AUTH_KEY` | generated keyfile | Management bearer credential; an explicit value cannot contain whitespace; otherwise reads or creates `${DATA_DIR}/auth.key` |
| `ENCRYPTION_KEY` | generated keyfile | Master key for encrypted upstream keys; otherwise reads or creates `${DATA_DIR}/encryption.key` |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | Graceful shutdown timeout in seconds |
| `READ_TIMEOUT` | `60` | Maximum time to read a complete request, in seconds |
| `IDLE_TIMEOUT` | `120` | Keep-alive idle timeout in seconds |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Compose stop budget; must exceed the application shutdown timeout |
| `LOG_LEVEL` | `info` | Application log level |
| `LOG_FORMAT` | `text` | Log format: `text` or `json` |

See [`.env.example`](.env.example) for the complete process configuration. Connect, first-byte, request, and stream-idle timeouts plus RequestLog retention are runtime settings managed through the admin UI/API, not additional environment variables.

## Persistence and security

- By default, `${DATA_DIR}` contains SQLite, `auth.key`, and `encryption.key`. Protect and back up these assets as one recovery set.
- Losing or replacing `encryption.key` makes encrypted upstream keys unreadable. 2.0.0 has no automatic repair or master-key rotation.
- An external `DATABASE_DSN` or explicitly managed secrets must be backed up separately; backing up DATA_DIR no longer covers those external assets.
- SQLite uses WAL. Before backup, stop incoming traffic, send `SIGTERM`, wait for a clean process exit, and then copy the full persistent asset set. Never copy only `gpt-load.db` while the service is running.
- Never paste AUTH_KEY, ENCRYPTION_KEY, AccessKeys, or upstream keys into logs, public issues, screenshots, or ordinary backup manifests.

The canonical operations source is **“GPT-Load 2.0 Deployment, Backup/Restore, and 1.x Cutover Runbook”** (`GPT-Load 2.0 部署、备份恢复与 1.x 切换 Runbook`) under the “🚀 Operations & Deployment” (`🚀 运维部署`) category in the “GPT-Load 2.0” Notion teamspace. Task 11 creates or updates that page and resolves its link; this README does not invent a URL in advance.

## Moving from 1.x

2.0 cannot open, import, or upgrade a 1.x database in place, and it must not reuse a 1.x `DATA_DIR`. The recommended flow is:

1. Keep 1.x running and verify that its backup can be restored.
2. Give 2.0 a separate port, `DATA_DIR` / named volume, and database.
3. Manually rebuild the minimum Groups, upstream keys, AccessKeys, and rules; validate all three dialects, logs, and usage/cost in isolation.
4. Move entry traffic during a maintenance window or small rollout. On failure, stop 2.0 and switch back to the original 1.x deployment; do not reverse-import new 2.0 data.

`latest` is not a safe 1.x-to-2.0 upgrade channel. Follow the canonical Runbook for cutover, backup, restore, and rollback details.

## Build and verification

Baseline tools: Go `1.25.12`, Node.js `>=24.11.0`, and pnpm `11.15.1`.

Build the single binary with its embedded admin UI:

```console
make build
```

Full local quality gates:

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

2.0.0 is expected to provide five native raw binaries plus `SHA256SUMS`:

- `gpt-load-linux-amd64`
- `gpt-load-linux-arm64`
- `gpt-load-macos-amd64`
- `gpt-load-macos-arm64`
- `gpt-load-windows-amd64.exe`

These are the expected names in the release contract, not a claim that a downloadable GitHub Release already exists.

## License and security

GPT-Load is released under the [MIT License](LICENSE). Report vulnerabilities through the process in [SECURITY.md](SECURITY.md).
