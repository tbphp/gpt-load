# GPT-Load

English | [中文](README_CN.md) | [日本語](README_JP.md)

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
![Go Version](https://img.shields.io/badge/Go-1.25-blue.svg)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

GPT-Load is a self-hosted Go gateway for managing upstream AI API keys and exposing native OpenAI, Anthropic, and Gemini endpoints through one service.

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

## Development status

> [!WARNING]
> 2.0 is not released. The `v2` branch is an active greenfield rewrite; use the `main` branch for the maintained 1.4.x release line.

M1 is complete as a backend-only milestone. It does not bundle or provide an admin frontend; M3 will rebuild that interface.

## Current M1 scope

- Native OpenAI, Anthropic, and Gemini data-plane routes with AccessKey authentication.
- SQLite-backed Groups, encrypted upstream keys, AccessKeys, and a reloadable runtime snapshot.
- Group list/create, key import into an existing Group, both model-discovery operations, and AccessKey CRUD through the current management API.
- Automatic generation of a local encryption keyfile when no explicit master key is supplied.

Deferred work is explicit: M2 completes scheduling and health behavior, M3 expands the control plane and rebuilds the admin UI, and M4 adds usage and cost accounting. Those capabilities are not part of M1.

## Architecture and runtime limits

- M1 ships only the Go backend and separates data-plane traffic from the `/api` management plane.
- 2.0.0 supports SQLite only and guarantees correctness for a single application instance only.
- `DATA_DIR` owns the default SQLite database, the management credential `auth.key`, and the separate encryption master-key file `encryption.key`. `DATABASE_DSN`, `AUTH_KEY`, and `ENCRYPTION_KEY` explicitly override their respective defaults.
- Upstream secrets are encrypted at rest; there is no plaintext fallback.

## Build and run

Go 1.25 is required.

```bash
cp .env.example .env
# AUTH_KEY is optional; leave it empty to read or create DATA_DIR/auth.key.
go build -o gpt-load .
./gpt-load
```

For development with the race detector:

```bash
make dev
```

## Environment

| Variable | Default | Purpose |
|---|---|---|
| `HOST` | `0.0.0.0` | HTTP listen address |
| `PORT` | `3001` | HTTP listen port |
| `AUTH_KEY` | optional | Management API bearer credential; a non-empty value wins and cannot contain whitespace; when empty, reads or creates `${DATA_DIR}/auth.key` |
| `DATA_DIR` | `./data` | Owns the default database and generated `auth.key` and `encryption.key` |
| `DATABASE_DSN` | `${DATA_DIR}/gpt-load.db` | Explicit SQLite path/DSN override when set |
| `ENCRYPTION_KEY` | generated keyfile | Explicit master-key override when set |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | Graceful shutdown timeout in seconds |
| `READ_TIMEOUT` | `60` | Maximum time to read a complete request, in seconds |
| `IDLE_TIMEOUT` | `120` | Keep-alive idle timeout in seconds |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Docker Compose stop budget |
| `LOG_LEVEL` | `info` | Application log level |
| `LOG_FORMAT` | `text` | Log format: `text` or `json` |

`auth.key` is the management API bearer credential. Back it up securely and restrict access to it; generation logs only its path, never the full value. It is separate from `encryption.key`, which encrypts stored upstream secrets.

## Data-plane routes

Data-plane requests use an AccessKey. Provider-compatible credentials are accepted through `Authorization: Bearer`, `x-api-key`, `x-goog-api-key`, or Gemini's `key` query parameter as appropriate.

| Method | Path | Protocol / behavior |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI chat completions |
| `GET` | `/v1/models` | OpenAI model list; an `anthropic-version` header selects the Anthropic model-list shape |
| `POST` | `/v1/messages` | Anthropic messages |
| `GET` | `/v1beta/models` | Gemini model list |
| `POST` | `/v1beta/models/{model}:generateContent` | Gemini content generation |
| `POST` | `/v1beta/models/{model}:streamGenerateContent` | Gemini streaming content generation |

Group selection is driven by the AccessKey and runtime configuration, not by a Group segment in the URL.

## Management API

Every management route requires `Authorization: Bearer <AUTH_KEY>`.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/auth/session` | Verify the current management credential |
| `GET` | `/api/groups` | List Groups |
| `POST` | `/api/groups` | Create a Group |
| `POST` | `/api/groups/{group_id}/keys/import` | Import keys into an existing Group |
| `POST` | `/api/groups/{group_id}/models/discover` | Discover models through an existing Group |
| `POST` | `/api/models/discover` | Discover models from an explicit upstream configuration |
| `POST` | `/api/access-keys` | Create an AccessKey |
| `GET` | `/api/access-keys` | List AccessKeys |
| `PUT` | `/api/access-keys/{id}` | Update an AccessKey |
| `DELETE` | `/api/access-keys/{id}` | Delete an AccessKey |

Management failures use `{ "code": string, "message": string, "data"?: any }`. The optional `data` field is present only when a client needs structured information to decide its next action.

## Docker Compose

The Compose file defaults to the published image. To run the unreleased `v2` checkout, uncomment its local `build` block first, then:

```bash
cp .env.example .env
# AUTH_KEY is optional; leave it empty to read or create /app/data/auth.key.
docker compose up -d --build
docker compose exec gpt-load sh -c 'cat /app/data/auth.key'
docker compose logs -f gpt-load
```

Back up `${DATA_DIR}/auth.key` securely and restrict access to it; it is the management bearer credential, not the encryption key. Before upgrades or any encryption-key change, back up the SQLite database and `${DATA_DIR}/encryption.key` together. If you set `DATABASE_DSN` or `ENCRYPTION_KEY`, back up those explicit values instead.

## Testing

```bash
go test -race . ./internal/...
go test ./internal/somepkg -run '^TestName$' -v
```

## License and security

GPT-Load is released under the [MIT License](LICENSE). Report vulnerabilities through the process in [SECURITY.md](SECURITY.md).
