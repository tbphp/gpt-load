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

It intentionally excludes CPA Manager, selector, pool, file store, server,
watcher, WebSocket/Auto executors, fallback, and internal retry loops.

## Pinned upstream

- Module: `github.com/router-for-me/CLIProxyAPI/v7`
- Version: `v7.2.143`

The root module consumes this bridge through a local `replace`; releases still
resolve CPA itself at the exact version recorded in both `go.mod` files and
`go.sum` files.

## Updating CPA

CPA upgrades are deliberate compatibility work, not automatic dependency
bumps:

1. Review upstream changes to Codex, Claude, Antigravity, and xAI OAuth, token, HTTP
   executor, translation, headers, identity, model discovery, and usage observation code.
2. Update the CPA version in this module and run `go mod tidy` here.
3. Fix only bridge compatibility issues; keep the execution-only boundary and
   do not adopt CPA Manager, retry, WebSocket, fallback, or file persistence.
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
