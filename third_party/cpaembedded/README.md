# CPA Embedded Bridge

This nested Go module is GPT-Load's deliberately small bridge to
CLIProxyAPI (CPA). It exists because the CPA Codex, Claude, Antigravity, and xAI executors and
OAuth helpers needed by GPT-Load are implemented in CPA `internal` packages and
cannot be imported from the root `gpt-load` module directly.

The module path is a child of CPA's module path, so it can compile the pinned
CPA implementation without copying CPA source into this repository. GPT-Load
owns persistence, account selection, affinity, retry, health, logging, and
quota policy. This bridge only exposes:

- Codex browser OAuth challenge creation and one-shot code exchange;
- strict CPA Codex JSON parsing;
- one-shot, context-aware token refresh;
- the stateless Codex HTTP executor and its explicit local CountTokens estimator;
- an explicitly called Codex WebSocket Session facade for serial Responses turns;
- one-shot model and usage observation requests.
- Claude browser OAuth challenge creation and one-shot code exchange;
- strict CPA Claude JSON parsing and stable device identity normalization;
- one-shot, context-aware Claude token refresh;
- Claude Code account profile, model entitlement, and usage observation;
- the stateless Claude HTTP executor, upstream CountTokens request, and supported protocol translators.
- Antigravity browser OAuth, strict native-file enrichment, stable Google account identity,
  model discovery, plan/Google One AI credits observation, and upstream quota-window observation;
- the execution-only Antigravity HTTP executor, upstream CountTokens request, and supported
  protocol translators, with CPA refresh, fallback, cooldown, paid-credit fallback, and global
  signature cache disabled.
- xAI OIDC discovery, one-shot device-code begin/poll, strict native/canonical file enrichment,
  stable OIDC subject identity, and one-shot context-aware refresh;
- the execution-only Grok HTTP executor, live OAuth model discovery, proactive billing
  observation, explicit local CountTokens estimator, and supported protocol
  translators, without CPA manager, refresh, retry, fallback,
  WebSocket, image, or video execution paths.

It intentionally excludes CPA Manager, selector, account pool, file store, server,
watcher, and Auto executors. The Codex WS facade blocks HTTP fallback and business
request replay. The gateway explicitly wires this facade into its native WS route;
the existing HTTP executor remains separate.

## Codex request identity

Codex HTTP inference (including streaming and images) and WebSocket handshakes
use a pinned `codex-tui/0.159.2` User-Agent with CPA's existing platform signature.
`Version` is fixed to `CodexClientVersion`, currently `0.159.2`. Downstream and GPT-Load group
header rules cannot override, clear, or remove these two identity headers.
This restriction applies only to Codex; other providers retain their header rules.
HTTP continues to honor explicit `Originator` rules, including empty values and
removal. WebSocket retains the fixed `codex-tui` originator.

Model and account observation requests use the same version for their User-Agent,
Version header, and models `client_version` query parameter. The embedded model
JSON is copied from OpenAI Codex `rust-v0.159.2`,
`codex-rs/models-manager/models.json`, with its SHA-256 checked by tests.
CPA `v8.0.22` with the explicit interrupted-terminal fix, the Codex identity, and this model snapshot form one tested version set.
The bridge pins execution identity because CPA's built-in UA still uses an older version.
HTTP, image, WebSocket, and observation tests check these outgoing values.

Both `Session-Id` and `Session_id` are accepted, with `Session-Id` taking precedence
if both exist. HTTP sends one `Session-Id`, retaining the existing precedence over
CPA's prompt-cache fallback. WebSocket keeps CPA's wire spelling and connection
reuse behavior.

Both Codex executors explicitly enable CPA's `ModelLevelCooling`. This keeps
`usage_limit_reached` from acquiring CPA's new credential-wide scope, preserving
GPT-Load's credential-plus-model cooldown policy. GPT-Load still owns scheduling,
retries, and health state. Pre-generation capacity rejections and explicitly
retryable `server_error` responses are classified in the HTTP bridge; ordinary
server failures do not acquire safe-replay evidence. WebSocket model-capacity
error codes do not trigger quota cooldowns; without generation-stage proof, the
existing conservative replay policy remains in effect.

## Codex WebSocket Session

`internal/subscription/providers/codex.NewWSSession` exposes this independent
capability to GPT-Load callers. The existing `NewExecutor` remains HTTP-only.

- Supply an already selected credential, optional HTTPS API proxy root (with the
  same native-path mapping as HTTP), and the proxy URL selected by the existing
  proxy policy (HTTP or SOCKS5), or `direct`. There is no additional WS-specific
  proxy scheme restriction; dialing uses the pinned SDK's proxy implementation.
  There is no environment proxy lookup, credential selection, or token refresh.
  HTTP proxies can tunnel TLS/WSS upstreams with CONNECT. The existing business
  proxy configuration and HTTP execution paths are unchanged.
- `NewWSSession` creates a handle; the first `ExecuteTurn` opens the connection.
  Pass a Responses create body, without the WS event `type`. The first turn must
  not reference a prior response. Retain its response ID and use the same Session
  for subsequent `previous_response_id` turns. IDs are not automatically added,
  removed, looked up globally, or persisted. CPA enforces `store:false` upstream.
- `ExecuteTurn(ctx, body, emit)` is synchronous. The callback receives native JSON
  events in order and must return promptly and honor `ctx`; nil discards events.
  It may return an error or call `Close` to stop. The result contains response ID,
  terminal status, raw usage (nil if absent), handshake headers when available,
  and `not_sent` / `maybe_sent` business-dispatch evidence. Reused connections do
  not provide new handshake headers; old quota headers are not carried forward.
  `HeaderObservedAt` records when headers were received, so generation time does
  not shift relative quota reset times. Prepared `Headers` use the SDK header contract.
  A generic `response.done` preserves `response.status`; only `completed` succeeds.
  `response.incomplete` with the exact reason `interrupted` also ends a turn
  normally while preserving the original status, usage, and connection. Other
  incomplete reasons keep their failure behavior.
- `Interrupt(ctx, responseID)` sends `response.interrupt` with
  `mode: "discard_partial_items"` to the current response on the existing
  connection, without creating a turn, retrying, or synthesizing completion.
  The active response ID is checked before dispatch. An upstream
  `response.interrupt.failed` is a control result, not a model terminal event.
- One turn runs at a time; overlapping calls fail with `session_busy`. Local
  validation errors leave the Session usable. Cancellation, timeout, transport
  loss and failed/protocol-invalid responses close it. A closed Session cannot
  reconnect. `Close` is idempotent and affects only that Session; active execution
  then unwinds. Callers own the eventual `Close`, including after successful use.
  Cancellation also closes an in-progress TLS/WS upgrade via the SDK's HTTP trace
  connection hook. Before an HTTP proxy CONNECT tunnel is established that hook
  is not yet available: cancellation may wait for proxy negotiation to end or its
  deadline (the smaller of the turn deadline and SDK's 30-second handshake limit).
  SOCKS5 is accepted with a known SDK limitation: its initial dial/negotiation
  ignores the request context. `TurnTimeout`, cancellation and `Close` cannot
  guarantee prompt termination or resource release while that operation is stuck;
  the goroutine and connection can remain until the proxy/network returns. Once
  the SOCKS5 tunnel is established, the existing TLS/WS cancellation and Session
  cleanup apply. The facade does not modify or fork the SDK to change this behavior.
- A lifetime-bound CPA `ExecutionLifecycle` blocks HTTP fallback and rejects
  replacement connections. CPA can still perform an extra handshake after a send
  failure, but cannot send the business request again. This is not a guarantee of
  exactly one network connection attempt.
- Without a caller deadline, the default per-turn timeout is five minutes.
  An explicit caller deadline is authoritative for that turn, so later turns can
  use updated timeout settings. `Done` closes when the Session is invalidated.
  Request and forwarded-event limits default to 10 MiB each. All three are configurable when creating the Session.
  The facade buffers no conversation history or output queue. Event checks occur
  **after SDK reading**: CPA v8.0.8 has no exposed raw-frame size limit and has
  its own internal buffers. These checks do not bound all SDK memory. CPA also
  retains its upstream read-idle timeout; idle connection loss invalidates the
  Session and is not transparently recovered.
- A narrowly scoped logrus hook is registered during package initialization,
  before the application's runtime redaction and log-sink hooks. It removes raw
  errors from the pinned SDK's disconnect logs for facade-owned Sessions,
  retaining safe error classes and WS close codes. It does not change log levels,
  outputs, or other Sessions' logs. Revalidate this log-format contract when
  upgrading CPA.
- No multi-lane `stream_id`, background mode, other protocols, Responses resource
  operations, global session routing, application shutdown integration, or
  business health/quota/logging policy is introduced here.

Deterministic tests use local fake WS upstreams. They prove connection reuse and
wire contracts, not real Codex account availability or recovery compatibility.
Real-account checks require a separately authorized credential and are opt-in.

```bash
CPA_LIVE_CODEX_WS_CREDENTIAL_FILE=/absolute/path/to/codex.json \
CPA_LIVE_CODEX_WS_MODEL=authorized-model-id \
  go test -count=1 -run '^TestLiveCodexWSSessionContract$' ./embedded
```

This makes two real model requests and checks that the second can recall a marker
sent only in the first. `CPA_LIVE_CODEX_WS_PROXY_URL` defaults to `direct`;
`CPA_LIVE_CODEX_WS_BASE_URL` optionally selects an authorized HTTPS API proxy root.

## Grok request identity

`grokClientVersion` is the single identity pin for execution, model discovery, and
billing, currently `1.0.44` with CPA `v8.0.8`. Each path retains its own User-Agent
format. CPA's version constant is not exported, and its default CLI identity only
applies to the official chat-proxy endpoint; the bridge therefore keeps its
explicit headers for custom upstreams too. Upgrading CPA does not replace this pin.
When changing CPA or the Grok identity, review upstream defaults and header
precedence, then verify the outgoing version and User-Agent for unary and streaming
execution on official and custom upstreams, model discovery, and billing. A locally
verified identity may differ from CPA's default. Record the tested pair after
validation; Grok has no CLI-version-bound model template snapshot to update.

## Pinned upstream

- Module: `github.com/router-for-me/CLIProxyAPI/v8`
- Version: `v8.0.8`

The bridge keeps Codex's fixed Version, execution and observation identity, and
model snapshot aligned at `0.159.2`. CPA includes Antigravity reasoning tokens in unary
OpenAI Chat and OpenAI Responses output totals; the bridge only adds them for OpenAI
Chat streaming, and retains Anthropic's unary cache-input normalization.
Antigravity Responses web search is not enabled by this dependency update.

CPA supplies the native Codex `X-Codex-Routing-Hint` to HTTP Responses
requests and WebSocket handshakes, derived from the resolved model and final
`service_tier`. Bridge tests verify that the hint matches the normalized request.
The official Codex snapshot includes GPT-6.1 Sol's native client capabilities.

The root module consumes this bridge through a local `replace`; releases still
resolve CPA itself at the exact version recorded in both `go.mod` files and
`go.sum` files.

## Updating CPA

CPA upgrades are deliberate compatibility work, not automatic dependency
bumps:

1. Review upstream changes to Codex, Claude, Antigravity, and xAI OAuth, token, HTTP
   executor, translation, headers, identity, model discovery, and usage observation code.
2. Update both modules' CPA pins and any changed major-version import paths, then
   run `go mod tidy` in both modules. Update the Codex identity, matching official
   model snapshot, snapshot digest, and version-alignment tests together.
3. Fix only bridge compatibility issues; keep the execution-only boundary and
   do not adopt CPA Manager, business-request retry, Auto, fallback, or file persistence.
   Revalidate the explicit WS facade's lifecycle, continuation, proxy and cancellation
   contracts when changing the pinned SDK.
4. Run `go test -count=1 ./...` in this module, then GPT-Load's full
   `make check` from the repository root.
5. With authorized disposable CPA credentials, run the applicable opt-in live
   contracts. Verify Codex discovery/observation and both providers'
   non-streaming and streaming execution.
6. Pin the reviewed CPA version in the root module and record the result in the
   implementation document and third-party notice.

The opt-in live test is:

```bash
CPA_LIVE_CREDENTIAL_FILE=/absolute/path/to/codex.json \
  go test -count=1 -run '^TestLiveCodexContract$' ./embedded
```

The file contents are never logged. Do not use a credential whose refresh token
is concurrently managed by another service when explicitly testing refresh;
the live contract test intentionally does not refresh it.

The Claude contract requires all account observation sources, discovers account
entitlements, then exercises unary and streaming Anthropic Messages, OpenAI Chat
Completions, OpenAI Responses, and Gemini conversions. It also calls the real
Anthropic CountTokens endpoint for the three CountTokens routes exposed by
GPT-Load. A model override is optional:

```bash
CPA_LIVE_CLAUDE_CREDENTIAL_FILE=/absolute/path/to/claude.json \
CPA_LIVE_CLAUDE_MODEL=optional-claude-model-id \
  go test -count=1 -run '^TestLiveClaudeContract$' ./embedded
```

This live test deliberately does not complete interactive browser OAuth, rotate
a refresh token, or force real 401/429 responses. Those gates require a disposable
account and an explicitly supervised run; deterministic bridge tests cover their
local classification contracts, but do not constitute real-provider evidence.

The Antigravity contract requires a disposable credential whose Google account is
authorized for the service. It verifies dynamic models, account/credits observation,
all declared unary/streaming protocol routes, and the three upstream CountTokens
routes. It must also confirm that no paid Google One AI credit type is injected.

```bash
CPA_LIVE_ANTIGRAVITY_CREDENTIAL_FILE=/absolute/path/to/antigravity.json \
CPA_LIVE_ANTIGRAVITY_MODEL=optional-antigravity-model-id \
  go test -count=1 -run '^TestLiveAntigravityContract$' ./embedded
```

Browser OAuth, refresh-token rotation, deliberate 401/429 responses, and provider
policy changes remain supervised live gates rather than default test behavior.

The Grok contract requires a prepared canonical xAI OAuth credential. It verifies live OAuth
models, weekly/monthly billing observation, all four unary/streaming protocol routes, and the
three explicit local CountTokens representations.

```bash
CPA_LIVE_GROK_CREDENTIAL_FILE=/absolute/path/to/grok.json \
CPA_LIVE_GROK_MODEL=optional-grok-model-id \
  go test -count=1 -run '^TestLiveGrokContract$' ./embedded
```

## Shared Codex capabilities

`modelcatalog/codex.json` contains the union of Codex records from
`router-for-me/models` commit `690c37fdbe62dc05f609f3a3e609d07ea4d16bf1`.
Records are selected in pro, plus, team, free order, keeping the first record
for each ID, as execution previously used the pro catalog. This describes
capabilities, not account model availability. Actual model discovery still
controls availability. The snapshot digest is checked in `modelcatalog` tests.

The client model list and CPA registry consume the same update capability.
The unchanged official client template snapshot also lives in `modelcatalog`.
For matching models, CPA's reasoning-level validation uses the exact levels from
that template, including `ultra`, instead of the narrower CPA snapshot list.
Models absent from the official template keep their CPA level metadata. The raw
CPA snapshot stays unchanged and retains its original provenance and digest.
Unknown models and virtual fallback models do not advertise effort updates.
Client templates remain pinned to Codex 0.159.2, with CPA v8.0.8; verify all
version pins and both model snapshots together when updating this set.

HTTP and WebSocket preserve supported `configuration_update` items. WebSocket
logs inherit effort only from the immediately known completed parent response
on the same model/session, and only when that history contains an effort update.
Ordinary request-level changes remain per-request. A fresh history uses its own baseline; failed turns
do not update the remembered effort. Upstream `response.reasoning.effort`
reports the baseline and must not overwrite the selected update in logs.
Clients must enable their effort-update feature (Codex CLI:
`--enable reasoning_effort_override`); GPT-Load has no server experiment switch.

Service-tier normalization uses CPA's native converters; bridge contract tests
cover Responses HTTP, WebSocket and Chat. Credential import/OAuth/refresh retain
explicit plan metadata or extract missing metadata from JWT claims. These are
presentation hints only. Live account observations take precedence; missing
claims never imply a free plan or successful quota observation.
