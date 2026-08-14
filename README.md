# GPT-Load

English | [中文](README_CN.md) | [日本語](README_JP.md)

[![Release](https://img.shields.io/github/v/release/tbphp/gpt-load)](https://github.com/tbphp/gpt-load/releases)
![Go Version](https://img.shields.io/badge/Go-1.26-blue.svg)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

GPT-Load is a self-hosted multi-provider AI gateway written in Go. A single binary with an embedded admin UI manages channel presets and encrypted credentials while exposing OpenAI, Anthropic, and Gemini-compatible client endpoints.

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
> 2.0 is a **pre-release local candidate**. M3/M4 candidate code and retained local verification evidence exist, but release exit and publication are not complete. No `v2.0.0` tag, GitHub Release, public binary, or public container image has been verified as available. A checkout or branch is not release evidence.

2.0 is a greenfield rewrite whose data is incompatible with 1.x and earlier pre-release 2.x builds. During pre-release development, use an empty dedicated database with a matching new encryption key. `main` remains the 1.4.x maintenance line. The release contract reserves explicit `2`, `2.0`, and `v2.0.0` container tags and does not move `latest` automatically; these names do not imply that images have been published.

## 2.0 capabilities

- **Two planes:** provider-native paths on the data plane; management APIs under `/api`, with the admin UI embedded in the same Go binary.
- **Four client protocols:** OpenAI Completions, OpenAI Responses, Anthropic Messages, and Gemini requests keep their public endpoints. Each channel declares its supported native and converted operations; unsupported feature combinations fail before dispatch.
- **Channel and traffic management:** Users select a searchable code-defined channel, then enter models and encrypted credentials. GPT-Load owns AccessKey filtering, cross-Group credential scheduling, retry decisions, health state, cooldown, blacklist, and automatic weights.
- **Provider execution:** The official Bifrost Core Go SDK maintains provider-specific authentication, request/response conversion, streaming, normalized usage, and provider errors. GPT-Load pins one selected credential per logical attempt and disables Bifrost's configured retry and fallback policy.
- **Codex subscriptions:** OpenAI Groups can use API keys or Codex browser OAuth/CPA JSON accounts. Subscription accounts share GPT-Load's existing scheduling, retry, health, weight, and affinity behavior, while credentials stay encrypted and account usage is synchronized only on explicit request.
- **Control and observability:** runtime settings, route inspection, health views, RequestLog, and a Chinese, English, and Japanese admin UI.
- **Usage and estimated cost:** usage extraction for the four protocols where the endpoint returns generation usage, 24-hour/30-day reports, per-request quality states, exact four-slot model prices synchronized from Models.dev where available, and user-managed prices.

The M3 control-plane UI and M4 usage/pricing scope are present in the local candidate, but their formal exit and public release are unfinished. Prices and costs are best-effort **estimates** derived from upstream usage and the active pricing rules. They are not a billing ledger, invoice, or provider bill, and historical requests are not repriced.

## 2.0.0 support boundaries

- Correctness is guaranteed for a **single application instance** only; multi-instance coordination is not supported.
- Supports SQLite, MySQL, and PostgreSQL through one unified `DATABASE_DSN` configuration. The current release targets the latest stable versions of these three engines; other database products are not supported.
- The AccessKey and runtime configuration select the Group. A Group never appears in the data-plane URL.
- Client protocol filters use `openai-completions`, `openai-responses`, `anthropic`, or `gemini`. A Group stores one `channel_id`; the code-owned channel preset determines provider behavior, accepted client protocols, credential schema, discovery, and Models.dev mapping.
- OpenAI Responses resource routing has no Key affinity. Stateful turns using `previous_response_id` or `conversation`, and later retrieve/delete/cancel/input-item calls, are reliable only with one upstream Key or an upstream that shares resource storage across Keys. Otherwise the selected upstream may return a resource-not-found error.
- Channel credentials must be encrypted at rest with no plaintext fallback. 2.0.0 has no master-key rotation; `migrate-keys` remains an explicitly failing deferred command.
- There is no automatic migration or reverse synchronization from 1.x or earlier pre-release 2.x schemas. The current baseline initializes an empty database; future schema changes will append migrations from this baseline.
- There is no online billing reconciliation, online backup API, or backup CLI. Models.dev synchronization supplies estimation metadata only; it is not a provider bill or invoice.

## Quick start

### Docker Compose

The candidate 2.x Compose contract references `ghcr.io/tbphp/gpt-load:2`, container path `/app/data`, and the `gpt-load-data` named volume. This is a local contract, not proof that the image is publicly available. It never uses `latest`. Check the current checkout first:

```console
cp .env.example .env
docker compose config
```

Continue only if the resolved configuration uses image `ghcr.io/tbphp/gpt-load:2`, sets the **container** environment to `HOST=0.0.0.0` and `DATA_DIR=/app/data`, leaves `DATABASE_DSN` empty/absent so the process selects managed `/app/data/gpt-load.db`, publishes the **host** side on `${BIND_ADDRESS:-127.0.0.1}`, and mounts a named volume at `/app/data`. The service has no fixed `container_name`, so Compose project names provide instance isolation. If the public image is unavailable, use the commented local build override instead of assuming it was published.

After those preconditions and image/build availability are met:

```console
docker compose up -d
curl --fail http://localhost:3001/health
# If AUTH_KEY was generated on first boot, read it once in a secure terminal
# and immediately store it in a secret manager.
docker compose exec gpt-load sh -c 'cat /app/data/auth.key'
```

The named volume preserves the default SQLite database, `auth.key`, and `encryption.key`. Production deployments should inject explicit `AUTH_KEY` and `ENCRYPTION_KEY` values through protected secret handling. Never commit real secrets to `.env`, logs, or issues. A MySQL or PostgreSQL `DATABASE_DSN` is operator-managed and should be supplied through the deployment secret/configuration system.

Compose listens on all interfaces only inside the container while publishing to host loopback by default. It also publishes the provider-fixed Codex OAuth callback on `127.0.0.1:1455`; keep it on loopback unless the network boundary is explicitly controlled. Because this callback port is fixed by the OAuth client, only one container on a host can perform browser authorization at a time. Setting `BIND_ADDRESS=0.0.0.0`, `OAUTH_CALLBACK_BIND_ADDRESS=0.0.0.0`, or running a native binary with `HOST=0.0.0.0`, is an explicit opt-in. In production, expose either only behind a controlled network boundary with a TLS reverse proxy and ACL/firewall controls.

### Native binary

After publication, download the platform-matching artifact from the GitHub Release and verify it against `SHA256SUMS`. Until release assets actually exist, build from the current checkout as shown under “Build and verification”; do not assume that an artifact has been published.

Linux amd64 example:

```console
chmod +x ./gpt-load-linux-amd64
mkdir -p ./data
HOST=127.0.0.1 DATA_DIR=./data ./gpt-load-linux-amd64
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
| OpenAI | `POST /v1/chat/completions` | Native OpenAI Completions request |
| OpenAI | `/v1/responses` and `/v1/responses/...` | Native OpenAI Responses namespace; ordinary HTTP methods are forwarded |
| OpenAI / Anthropic | `GET /v1/models` | OpenAI shape by default; Anthropic shape when `anthropic-version` is present |
| Anthropic | `POST /v1/messages` | Native Anthropic Messages request |
| Gemini | `GET /v1beta/models` | Native Gemini model list |
| Gemini | `POST /v1beta/models/{model}:generateContent` | Gemini non-streaming generation |
| Gemini | `POST /v1beta/models/{model}:streamGenerateContent` | Gemini streaming generation |

The AccessKey and runtime configuration select the Group; it is not passed as a URL path segment. The selected channel determines whether an operation is native or converted. Conversion is capability-gated and never treated as arbitrary lossless JSON transformation.

The canonical client protocol filter values and display names are:

| Configuration value | Display name |
|---|---|
| `openai-completions` | OpenAI Completions |
| `openai-responses` | OpenAI Responses |
| `anthropic` | Anthropic |
| `gemini` | Gemini |

The built-in `openai` channel supports both OpenAI client protocols. Other official, curated, compatible, and cloud channels expose only the operations and features declared by their code-owned presets; users do not configure protocol checkboxes per Group.

Responses routing uses the namespace boundary, not a per-resource allowlist. After AccessKey authentication, `/v1/responses` and its ordinary subpaths are sent through the same scheduler and execution pipeline. Decoded `.` or `..` path segments are rejected locally so normalization or redirects cannot escape the authorized namespace. `OPTIONS`, `CONNECT`, and `TRACE` are also rejected locally; other methods, including `GET`, `POST`, `DELETE`, and `HEAD`, are forwarded. Paths and queries are preserved within Go URL normalization: decoded `URL.Path` is re-encoded and `RawPath` is not retained. GPT-Load does not search other Credentials for a resource ID; the selected upstream's response, including a resource-not-found error, is returned through the normal response-safety boundary.

A Group whose channel supports Responses may keep an empty model list and still serve model-free Responses resource endpoints. Requests that include a model, including ordinary create requests, still require a configured model route.

> [!WARNING]
> 2.0.0 does not implement Responses affinity. Stateful multi-turn requests using `previous_response_id` or `conversation`, and resource operations on an earlier response ID, may reach a different Group/Credential and receive an upstream 404. Use a single Credential, stateless item replay with `store: false`, or an upstream with shared resource storage until affinity is implemented.

Responses create and compact requests participate in usage extraction. Retrieve, delete, cancel, input-items, input-token-count, and unknown extension subpaths are recorded with usage `not_applicable`. Discovery and validation are selected by the channel preset rather than by user-configured protocol checkboxes.

OpenAI Completions example:

```console
curl http://127.0.0.1:3001/v1/chat/completions \
  -H "Authorization: Bearer $GPT_LOAD_ACCESS_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"<MODEL_ID>","messages":[{"role":"user","content":"Hello"}]}'
```

Responses example:

```console
curl http://127.0.0.1:3001/v1/responses \
  -H "Authorization: Bearer $GPT_LOAD_ACCESS_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"<MODEL_ID>","input":"Hello","store":false}'
```

The official OpenAI SDK can use the same native endpoint:

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

## Management, usage, and cost

The admin UI is served at `/`, and management APIs are under `/api`; both use `AUTH_KEY`. The UI covers the searchable channel directory, Groups, encrypted credentials, AccessKeys, runtime settings, health, logs, route inspection, Usage, and model-price management. Current code and UI are the management API reference; this README intentionally avoids copying a route list that can drift.

Automatic catalog synchronization is enabled by default and uses the control plane to fetch the fixed endpoint `https://models.dev/api.json`; startup remains asynchronous and can use the durable last-known-good catalog. Manual synchronization remains available. Data-plane requests never contact Models.dev.

Usage/Cost quality boundaries:

- `complete` and `partial` usage contribute their known token dimensions; `missing` usage contributes only request and quality counts.
- `priced` requests contribute their known estimated cost. `pricing_partial` retains the calculable portion while reporting incomplete price coverage; `unpriced` requests are never assigned guessed prices.
- A clean EOF on a stream does not guarantee complete usage, and compatible relays may omit the provider's official terminal usage.
- Prices match the exact `(channel_id, upstream model)` identity. Models.dev is the sole automatic source; explicit user overrides and the existing fallback rules remain available. The four flat slots are input, output, cache read, and cache write; an explicit zero means free, while an unset slot remains unavailable.
- Price changes affect future writes only. Historical RequestLog and UsageStat rows are not recalculated.
- Current-process dropped/write-failure counters and durable database-window aggregates have different scopes.

## Core configuration

| Variable | Default | Purpose |
|---|---|---|
| `HOST` | `127.0.0.1` | Native HTTP listen address; `0.0.0.0` is an explicit opt-in. The release container overrides this to `0.0.0.0` internally |
| `BIND_ADDRESS` | `127.0.0.1` | Compose host-side publish address; not a process setting |
| `OAUTH_CALLBACK_BIND_ADDRESS` | `127.0.0.1` | Compose host-side address for the fixed Codex OAuth callback on port `1455` |
| `PORT` | `3001` | HTTP listen port |
| `DATA_DIR` | `./data` | Native persistent directory; the container overrides it to `/app/data` |
| `DATABASE_DSN` | empty → `${DATA_DIR}/gpt-load.db` | Empty selects managed SQLite. Non-empty values use one of `sqlite`, `mysql`, or `postgres` URL forms and are operator-managed |
| `AUTH_KEY` | generated keyfile | Management bearer credential; an explicit value cannot contain whitespace; otherwise reads or creates `${DATA_DIR}/auth.key` |
| `ENCRYPTION_KEY` | generated keyfile | Master key for encrypted channel credentials; otherwise reads or creates `${DATA_DIR}/encryption.key` |
| `MODELS_DEV_AUTO_SYNC_ENABLED` | unset | Optional strict boolean override for Models.dev automatic synchronization; unset uses the runtime setting, which defaults to enabled |
| `GRACEFUL_SHUTDOWN_TIMEOUT` | `10` | Graceful shutdown timeout in seconds |
| `READ_TIMEOUT` | `60` | Maximum time to read a complete request, in seconds |
| `IDLE_TIMEOUT` | `120` | Keep-alive idle timeout in seconds |
| `CONTAINER_STOP_GRACE_PERIOD` | `15s` | Compose stop budget; must exceed the application shutdown timeout |
| `LOG_LEVEL` | `info` | Application log level |
| `LOG_FORMAT` | `text` | Log format: `text` or `json` |

See [`.env.example`](.env.example) for the complete process configuration. Native-response/stream-first-event, total-request, and stream-idle timeouts plus RequestLog retention are runtime settings managed through the admin UI/API, not additional environment variables. Buffered converted unary calls use the total-request timeout because the SDK does not expose a response-start signal.

`DATABASE_DSN` uses one common URL contract; do not add a separate database type variable or engine-specific application settings. Examples:

```text
sqlite:///var/lib/gpt-load.db
mysql://user:password@db.example:3306/gpt_load?charset=utf8mb4&collation=utf8mb4_bin
postgres://user:password@db.example:5432/gpt_load?sslmode=require
```

The empty value is the only application-managed database mode. A non-empty SQLite path/URL and all MySQL/PostgreSQL URLs are external, operator-managed values.

## Persistence and security

- Database ownership follows only the raw `DATABASE_DSN`: empty means the managed SQLite DB/WAL/SHM under `${DATA_DIR}`; every non-empty value means an external, operator-owned database that GPT-Load does not mkdir, chmod, or create database users for and that must be backed up separately.
- Secret ownership is independent of database ownership. For each secret, `/api/system/info` reports `key_file` or `environment`: archive a reported `key_file` (`auth.key` or `encryption.key`) from `DATA_DIR` regardless of the database source, or restore an `environment` secret separately from the protected external secret system.
- On POSIX, managed `${DATA_DIR}` is restricted to `0700` and managed DB/WAL/SHM plus application-created key files to `0600`. Windows uses current-user-only ACLs, but the Windows runtime stop/ACL gate has not been executed for this candidate.
- Losing the matching `encryption.key`, from either source, makes encrypted channel credentials unrecoverable. 2.0.0 has no automatic repair or master-key rotation.
- Managed SQLite uses WAL. Before backing it up, stop incoming traffic and wait for a clean exit: use `SIGTERM` on POSIX, or Ctrl+C, Ctrl+Break, or the service manager's stop action on Windows. External MySQL/PostgreSQL backups must follow the operator's engine-native backup procedure.
- Never paste AUTH_KEY, ENCRYPTION_KEY, AccessKeys, or channel credentials into logs, public issues, screenshots, or ordinary backup manifests.

### Public operations baseline

This checklist is self-contained and does not require access to the project's private Notion workspace:

1. Determine the database source and location from the actual environment, service, or container configuration, then call authenticated `GET /api/system/info` to record the selected database driver and each secret's safe source/path metadata without recording secret values. The endpoint deliberately omits the database DSN, credentials, and location.
2. Stop incoming traffic and wait for a clean process exit using the POSIX or Windows mechanism above. With Compose, run `docker compose stop` and confirm the service container is stopped.
3. Build the complete recovery set across both independent axes: archive managed DB/WAL/SHM when `DATABASE_DSN` is empty, or back up the external DB with its operator procedure when non-empty; for both database cases, archive every `key_file` reported for auth/encryption and recover every `environment` secret from its protected external secret system. Use unique archive names, refuse overwrite, restrict access, and record SHA-256.
4. Restore both the database and secret sides with the exact same binary or image into an empty target. Verify checksums first and restore the exact matching encryption key; never combine restore with an upgrade.
5. Start the restored instance and verify `/health`, `/api/system/info`, Groups, AccessKeys, model prices, Usage, RequestLog, and a real data-plane canary. For managed SQLite, stop the instance and require `PRAGMA quick_check` to return `ok`; for MySQL/PostgreSQL, use the operator's native consistency check.

2.0.0 has no backup CLI or encryption-key rotation. Never replace the encryption key for an existing database.

## Moving from 1.x

2.0 cannot open, import, or upgrade a 1.x database in place, and it must not reuse a 1.x `DATA_DIR`. The recommended flow is:

1. Keep 1.x running and verify that its backup can be restored.
2. Give 2.0 a separate port, `DATA_DIR`, database, and Compose project/named volume. Do not share any of these with 1.x.
3. Manually rebuild the minimum Groups, channel credentials, AccessKeys, and rules; validate the required client protocols, logs, and usage/cost in isolation.
4. Move entry traffic during a maintenance window or small rollout. On failure, stop 2.0 and switch back to the original 1.x deployment; do not reverse-import new 2.0 data.

`latest` is not a safe 1.x-to-2.0 upgrade channel. Use the public operations baseline above for backup and restore, and keep the original 1.x deployment and data intact until the rollback window closes.

## Build and verification

Baseline tools: Go `1.26.5`, Node.js `>=24.11.0`, and pnpm `11.17.0`.

Build the single binary with its embedded admin UI:

```console
make build
```

Full local quality gates:

```console
make check
```

Frontend unit tests and browser E2E tests are not part of the project workflow. Frontend verification consists of dependency installation, linting, formatting, type-checking, and building.

2.0.0 is expected to provide five native raw binaries plus `SHA256SUMS`, a CycloneDX SBOM, and project/third-party license notices:

- `gpt-load-linux-amd64`
- `gpt-load-linux-arm64`
- `gpt-load-macos-amd64`
- `gpt-load-macos-arm64`
- `gpt-load-windows-amd64.exe`

These are the expected names in the release contract, not a claim that a downloadable GitHub Release already exists.

## License and security

GPT-Load is released under the [MIT License](LICENSE). Distributed third-party notices are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), with license texts under [`LICENSES/`](LICENSES/). Report vulnerabilities through the process in [SECURITY.md](SECURITY.md).
