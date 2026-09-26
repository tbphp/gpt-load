<div align="center">

<img src="./web/public/favicon.svg" alt="GPT-Load" width="96">

# GPT-Load

**A self-hosted AI gateway for multi-channel, multi-credential setups**

API keys, subscription accounts, traffic scheduling, failure handling, request logs, and usage accounting — behind a single entry point.

English · [中文](README_CN.md) · [日本語](README_JP.md) | [Official Website](https://www.gpt-load.com)

[![Release](https://img.shields.io/github/v/tag/tbphp/gpt-load?filter=v2.*)](https://github.com/tbphp/gpt-load/releases)
[![Docker](https://img.shields.io/badge/Docker-ghcr.io%2Ftbphp%2Fgpt--load%3A2-2496ED?logo=docker&logoColor=white)](https://github.com/tbphp/gpt-load/pkgs/container/gpt-load)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

<a href="https://trendshift.io/repositories/14880" target="_blank"><img src="https://trendshift.io/api/badge/repositories/14880" alt="tbphp/gpt-load | Trendshift" width="220" height="48"/></a>
<a href="https://hellogithub.com/repository/tbphp/gpt-load" target="_blank"><img src="https://api.hellogithub.com/v1/widgets/recommend.svg?rid=554dc4c46eb14092b9b0c56f1eb9021c&claim_uid=Qlh8vzrWJ0HCneG" alt="Featured｜HelloGitHub" width="220" height="47"/></a>

</div>

---

## Sponsors

<sub>[Become a sponsor](mailto:tangb7420@gmail.com)</sub>

<details open>
<summary>Sponsor details (collapsible)</summary>

<table>
<tbody>
<tr>
<td width="180"><a href="https://ofox.ai/?utm_source=github&amp;utm_medium=sponsorship&amp;utm_content=gpt_load" target="_blank" rel="noopener noreferrer"><img src="./screenshot/ofoxai.svg" alt="OfoxAI" width="150"></a></td>
<td><strong>OfoxAI: Text, image, and video AI in one platform</strong><br>OfoxAI is a unified AI API platform bringing together text, image, and video models from multiple providers. With OpenAI-compatible endpoints and native Anthropic and Gemini interfaces, developers can access models for AI applications, agents, and content creation through one platform. <a href="https://ofox.ai/?utm_source=github&amp;utm_medium=sponsorship&amp;utm_content=gpt_load" target="_blank" rel="noopener noreferrer">Explore OfoxAI models and APIs →</a></td>
</tr>
<tr>
<td width="180"><a href="https://www.packyapi.ai/register?aff=ahiS" target="_blank" rel="sponsored noopener noreferrer"><picture><source media="(prefers-color-scheme: dark)" srcset="./screenshot/packycode-dark.png"><source media="(prefers-color-scheme: light)" srcset="./screenshot/packycode-light.png"><img src="./screenshot/packycode-light.png" alt="PackyCode" width="150"></picture></a></td>
<td><strong>PackyCode</strong><br>Access leading AI models through PackyCode with one API endpoint and one API key. Enjoy fast, reliable access with automatic failover and dedicated high-speed routes for Codex and Claude Code. Get started with $1 in free credits, a discount on your first top-up, and savings of up to 80% on eligible routes. Pay in RMB with no currency conversion markups or extra top-up fees. <a href="https://www.packyapi.ai/register?aff=ahiS" target="_blank" rel="sponsored noopener noreferrer">Sign up through the link and start building today.</a></td>
</tr>
<tr>
<td width="180"><a href="https://fluxionai.space/register?source=github&amp;campaign=gptload&amp;promo=GPTLOAD" target="_blank" rel="sponsored noopener noreferrer"><img src="./screenshot/fluxionai-horizontal.png" alt="Fluxion AI" width="150"></a></td>
<td><strong>One entry point to connect and manage the world's leading AI models</strong><br>Fluxion AI serves individual developers, technical teams, and enterprises with a unified API for connecting to and managing leading AI models worldwide. Dynamic multi-route scheduling improves availability, while model performance, response times, and costs remain transparent and easy to review. Depending on the model and route, API calls can cost 40%–98% less than official or benchmark prices. Visit and sign up now to receive $7 in API credits. (<a href="https://fluxionai.space/register?source=github&amp;campaign=gptload&amp;promo=GPTLOAD" target="_blank" rel="sponsored noopener noreferrer">Dedicated link</a>)</td>
</tr>
<tr>
<td width="180"><a href="https://www.axisnow.io/zh"><img src="./screenshot/axisnow.jpg" alt="AxisNow" width="150"></a></td>
<td>Protect and accelerate websites and APIs, <strong>serving users in mainland China</strong> and around the world, and extend acceleration and security capabilities to native/mobile apps through a client SDK — <strong>self-built private-deployment CDN | subscription-based high-protection CDN | an independently controllable, flexibly composable CDN network.</strong></td>
</tr>
<tr>
<td width="180"><a href="https://go.apimart.ai/gh-gpt-load"><img src="./screenshot/apimart.png" alt="APIMart" width="150"></a></td>
<td>Thanks to APIMart for sponsoring this project! APIMart is a low-cost API platform for AI image &amp; video generation — GPT-Image-2 from $0.006/image, 160+ images per dollar. One async API covers both image and video: submit a task, get an ID, fetch results via polling or callback. Batch tens of thousands of images without timeouts, switch models without changing code. Pay-as-you-go with no monthly fee — <a href="https://go.apimart.ai/gh-gpt-load">sign up here</a> to get started.</td>
</tr>
</tbody>
</table>

</details>

## Why GPT-Load

Your application only needs one base URL and one AccessKey. Providers, accounts, credentials, models, and routing policy are all configured in the management UI.

<img src="./screenshot/architecture-overview.svg" alt="GPT-Load unified access and upstream routing architecture" width="860">

- **One gateway, native protocols** — Manage official APIs, cloud platforms, model services, and compatible relays together while clients keep their OpenAI, Anthropic, or Gemini native interfaces.
- **One mechanism for API keys and subscriptions** — Codex, Claude, Antigravity, Grok, and API-key channels share credential management, scheduling, and health handling.
- **Scheduling and failure isolation built in** — Multi-credential scheduling, configurable weights, retries, cooldown, blacklisting, and session affinity reduce the impact of overloaded or failing credentials.
- **Observable, self-hosted, and simple to deploy** — Inspect health, routes, logs, usage, and cost estimates in an embedded UI backed by SQLite, MySQL, or PostgreSQL with local credential encryption.

## Quick start

> [!WARNING]
> If you are using 1.x, read [Moving from 1.x](#moving-from-1x) first. 2.0 cannot open, import, or migrate 1.x data in place.

### 1. Start the service

Requires Docker and Docker Compose.

```bash
git clone --depth 1 https://github.com/tbphp/gpt-load.git
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

**Groups** — View channels, models, credential counts, traffic, and health in one place

<img src="./screenshot/groups-en-US.png" alt="GPT-Load modern groups overview" width="860">

**Usage statistics** — Review request trends, cache hit rate, token categories, and cost estimates

<img src="./screenshot/usage-en-US.png" alt="GPT-Load modern usage statistics" width="860">

## Scope

### Client protocols

| Protocol                | Main entry                                                         |
| ----------------------- | ------------------------------------------------------------------ |
| OpenAI Chat Completions | `POST /v1/chat/completions`                                        |
| OpenAI Responses        | `/v1/responses` and its resource paths                             |
| OpenAI Images           | `POST /v1/images/...`                                              |
| OpenAI Embeddings       | `POST /v1/embeddings`                                              |
| Rerank                  | `POST /v1/rerank`                                                  |
| Anthropic Messages      | `POST /v1/messages`                                                |
| Gemini                  | `/v1beta/models/...`                                               |
| Gemini Embeddings       | `POST /v1beta/models/{model}:embedContent` / `:batchEmbedContents` |

Each channel declares exactly which protocols and capabilities it can execute. GPT-Load converts between supported capabilities, but it is not a general-purpose any-protocol, any-JSON translator.

Embeddings has two independent native protocols: `openai-embeddings` (`POST /v1/embeddings`) on the OpenAI, OpenRouter, OpenAI Compatible, New API, and GPT-Load API-key channels, and `gemini-embeddings` (`POST /v1beta/models/{model}:embedContent` and `:batchEmbedContents`) on the Gemini, New API, and GPT-Load API-key channels. Requests and responses keep each provider's native format. The Gemini channel also accepts `openai-embeddings` by converting text input to `:batchEmbedContents`; `dimensions` and `float` / `base64` output are supported, token-ID input is not, and Gemini enforces its per-request item limit. Keep the groups behind one client model name on the same embedding model, otherwise vectors from different models get mixed. Subscription channels are not supported. An AccessKey without a protocol filter keeps its existing “all enabled protocols” behavior and therefore gains both Embeddings protocols after upgrade. Least-privilege deployments should configure an explicit protocol filter.

Rerank uses the independent `rerank` protocol through `POST /v1/rerank` on the OpenAI Compatible, New API, and GPT-Load API-key channels. Requests contain `model`, `query`, and a text-only `documents` array, with optional upstream parameters such as `top_n` and `return_documents`. Streaming, subscription channels, and protocol conversion are not supported. OpenAI Compatible takes a complete API prefix (for example, `https://host/v1`); New API / GPT-Load take the gateway root. The upstream must implement a compatible Rerank endpoint. AccessKeys without a protocol filter also gain Rerank access. Responses containing only non-token units such as `search_units` remain unpriced; these units are not treated as tokens or free requests.

### Built-in channels

- **Official and cloud** — OpenAI, Anthropic, Gemini, xAI, Azure OpenAI, AWS Bedrock, Google Vertex AI
- **Model services** — DeepSeek, Moonshot AI, SiliconFlow, Zhipu AI, Alibaba, Volcengine, OpenRouter, Groq, Cerebras, Mistral, Nebius, Parasail, Wafer, Hugging Face (Chat), Cohere (text reranking), OpenCode Go, OpenCode Zen
- **Subscription** — Codex, Claude, Antigravity, Grok
- **Custom** — OpenAI Compatible (any compatible relay)

OpenCode Go / Zen use API keys and the existing multi-protocol gateway adapter for native Chat Completions, stateless Responses creation, and Anthropic Messages, including streaming and model discovery. Choose the protocol supported by the upstream model; these presets do not convert protocols automatically. Custom Base URLs use the gateway root without `/v1`. Gemini-native requests and Responses resource operations are not enabled. Go clients should preserve their coding-client identity and conversation session headers.

### Codex live voice

Choose the default under **System settings → Connection and timeouts → Live voice**. Each Codex group's **Advanced configuration / Runtime settings** can inherit it or select **Off, Direct to upstream, Gateway relay**. Groups with different modes can coexist; each call stays bound to its selected group and account. Off disables voice only and ends affected active voice sessions; text requests remain available.

- **Direct to upstream (default)**: GPT-Load authenticates, selects the account, creates the call and proxies the control WebSocket. Audio and the WebRTC data channel connect directly from the client to upstream. Account tokens stay on the server. No server media UDP mapping is needed, but the client must reach upstream media endpoints; a server-side outbound proxy does not proxy this client traffic.
- **Gateway relay**: audio and the WebRTC data channel pass through GPT-Load and use its configured upstream proxy. All relay groups share the process media IP and UDP range.
- **Accounts and retries**: call creation uses existing global weighted scheduling and the system additional-retry budget (default 2). Explicit 403/429 rejections can try another candidate; 401 may refresh the credential, within the same budget. Timeouts, ambiguous 5xx results and failures after a call was created do not replay the call on another account. Voice admission failures do not blacklist otherwise working text credentials. Set known voice-ineligible groups to Off; plan names are not used to infer entitlement.

**Upgrade: previous versions always relayed media. The new unset default is Direct; deployments relying on server media transport must explicitly select Gateway relay.** No group voice-model setup is required; the client's model is forwarded unchanged. Both modes require a control WebSocket within 30 seconds of creation and reconnection within 30 seconds of disconnection. Append `/hangup` to the creation response Location to end a call explicitly. One session log is emitted after local closure and the first upstream hangup attempt. A failed first hangup returns HTTP 502 and immediately records an incomplete result, without waiting for background cleanup. Hangup retries run every 5 seconds, with at most 3 attempts including the initial and manual attempts. The capacity slot is retained and control reattachment is rejected during retries; success or exhaustion releases the local slot. Later retries neither rewrite nor duplicate the session log. Exhaustion does not confirm upstream termination; server warnings include the failure category and HTTP status when available. Usage/cost may remain unavailable.

For Docker relay, download [`docker-compose.voice.yml`](docker-compose.voice.yml) from the same version as the base Compose file, set a client-reachable public media IP in `.env`, then select Gateway relay in system or group settings:

```dotenv
CODEX_LIVE_PUBLIC_IP=YOUR_SERVER_PUBLIC_IP
```

```bash
docker compose -f docker-compose.yml -f docker-compose.voice.yml up -d
```

The overlay aligns listening and published ports, defaulting to **UDP 50000–50127**. Allow that range in the cloud security group and host firewall. To customize it, set both `CODEX_LIVE_UDP_PORT_MIN` and `CODEX_LIVE_UDP_PORT_MAX` in `.env` and recreate the container. Nginx must support WebSocket Upgrade and suitable long-lived connection timeouts; its HTTP proxy does not forward media UDP. Separate hosts, NAT and CDNs still require a directly reachable media address/port or appropriate STUN/TURN in `CODEX_LIVE_ICE_SERVERS`. GPT-Load cannot configure the public IP or external firewall for you.

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

The official Compose file uses `ghcr.io/tbphp/gpt-load:2`. Before GA, `2` tracks verified 2.0 Beta and RC releases; after GA, it tracks stable 2.x releases only. Exact image tags omit the Git tag's `v` prefix (for example, `2.0.0-beta.25`), while `2.0-beta` remains the 2.0 Beta channel. `latest` remains on 1.x.

<details>
<summary>Using a native binary</summary>

Download the build for your platform from [GitHub Releases](https://github.com/tbphp/gpt-load/releases), and verify it against the bundled `SHA256SUMS` first:

```bash
chmod +x ./gpt-load-linux-amd64

HOST=127.0.0.1 DATA_DIR=./data ./gpt-load-linux-amd64
```

Then open <http://127.0.0.1:3001>. Portable builds are provided for five targets across Linux, macOS (amd64 / arm64), and Windows; `gpt-load-windows-amd64.exe` keeps running in the foreground as before.

Windows desktop users can instead download `gpt-load-windows-setup.exe`. After one administrator approval, Setup installs and starts a low-privilege Windows service, enables automatic startup, and creates desktop and Start Menu shortcuts to the GPT-Load management page. Setup displays the generated management key before it finishes; save it before closing the page. The protected copy remains at `%ProgramData%\GPT-Load\data\auth.key`. Service configuration and its `.env` live in `%ProgramData%\GPT-Load`, with persistent data in `%ProgramData%\GPT-Load\data`.

Installing a newer Setup stops the service gracefully before updating it. Windows uninstall removes the program and service but preserves data. Advanced users can still manage an installed service with `gpt-load-windows-amd64.exe service start|stop|restart|status`.

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
| `DATA_DIR` | `./data` | Directory for the managed database, `auth.key`, `encryption.key`, and runtime state; official Compose uses `/app/data`, while the Windows Setup service uses `%ProgramData%\GPT-Load\data`. |
| `DATABASE_DSN` | Empty, uses `${DATA_DIR}/gpt-load.db` | Empty uses application-managed SQLite; non-empty values support SQLite paths or URLs, MySQL URLs, and PostgreSQL URLs, and are treated as operator-managed external databases. Container file paths must be inside a mounted directory. |
| `DATABASE_MAX_OPEN_CONNECTIONS` | `10` | Maximum open connections for MySQL and PostgreSQL, positive integer. SQLite always uses one connection. |
| `DATABASE_MAX_IDLE_CONNECTIONS` | `5` | Maximum idle connections for MySQL and PostgreSQL, positive integer and no greater than `DATABASE_MAX_OPEN_CONNECTIONS`. SQLite always uses one connection. |
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
- HTTP Responses continuation with `previous_response_id` automatically uses native Responses routes that declare upstream-managed storage: currently `openai`, `gpt_load`, `xai`, `newapi`, `cliproxyapi`, and `sub2api`. Ownership is isolated by AccessKey and pins the original credential when current routing permits, independently of soft affinity; actual state availability depends on the upstream. Stateless and converted responses are not registered as persistent state. Unknown IDs, including IDs created before upgrading or outside this gateway, are rejected. Group parameter overrides cannot change this field.
- Native Responses WebSocket uses `GET /v1/responses` on the same port. Admission follows declared upstream capabilities for OpenAI, xAI, Codex, and compatible native CPA/sub2api and GPT-Load endpoints. Clients may include the boolean `stream:true/false`; both values still use the WS event stream. Each turn checks current permissions, rate and cost limits, and routing, with separate usage and cost records. One connection keeps one upstream identity; there is no HTTP fallback or conversation-history replay.
- `responses_websocket_enabled` defaults to enabled. An explicit group setting overrides the global value; otherwise the group inherits it. Disabling immediately closes affected WS connections and interrupts generation without affecting HTTP/SSE. Re-enabling does not restore the old connection's temporary state.
- `empty_response_retry` defaults to disabled. An explicit group setting overrides the global value. When enabled, a streaming chat request is checked for produced content before it is committed downstream: if upstream finishes naturally without producing anything, the attempt counts as a failure and the next candidate is tried, without cooling down or blacklisting the credential. Once retries are exhausted the upstream empty response is still delivered as-is. Empty results explained by their stop reason, such as an exhausted output budget, a content filter or a refusal, are delivered without retrying. Prewarm requests (`generate:false`), requests carrying `previous_response_id` or `conversation`, non-chat endpoints and WebSocket are all exempt; when upstream keeps emitting events without content, the response is committed at a built-in limit rather than held indefinitely. Retried empty attempts are still billed upstream, but usage and cost estimates only record the attempt that was finally delivered.
- Full `stream_id` multiplexing and forks are enabled for OpenAI and GPT-Load cascades that support them end to end. The other channels above run serially and reject named streams. Prewarming sends `generate:false` upstream. Codex continuation requires the original live connection: `store:true` and restoration by an old ID on a new connection are unsupported. Persistent continuation on other channels depends on storage capabilities and valid ownership. Existing [Codex SDK proxy, reading, and shutdown limits](third_party/cpaembedded/README.md#codex-websocket-session) still apply.
- Response bindings stay in memory for up to 30 days, with limits of 100,000 entries and 16 MiB of ID text; older entries are evicted when capacity is reached. A successful checkpoint during normal shutdown allows restoration from the same data directory. Crash recovery and continued upstream state availability are not guaranteed.
- `conversation` and other existing resource IDs are outside this ownership routing scope and still depend on a single credential or upstream resource sharing across credentials.

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

## Project support

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
<br><sub>Platform support</sub>
</td>
<td align="center" width="33%">
<a href="https://linux.do"><img src="./screenshot/l.png" alt="LINUX DO" width="120"></a>
<br><sub>Community support</sub>
</td>
<td align="center" width="33%">
<a href="https://www.digitalocean.com/?refcode=3d52cff21342&utm_campaign=Referral_Invite&utm_medium=Referral_Program&utm_source=badge"><img src="https://web-platforms.sfo2.cdn.digitaloceanspaces.com/WWW/Badge%202.svg" alt="DigitalOcean" width="120"></a>
<br><sub>Infrastructure support</sub>
</td>
</tr>
</tbody>
</table>

---

[MIT License](LICENSE) · [Third-party notices](THIRD_PARTY_NOTICES.md) · [Security policy](SECURITY.md)
