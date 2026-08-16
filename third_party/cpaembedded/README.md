# CPA Embedded Bridge

This nested Go module is GPT-Load's deliberately small bridge to
CLIProxyAPI (CPA). It exists because the CPA Codex and Claude executors and
OAuth helpers needed by GPT-Load are implemented in CPA `internal` packages and
cannot be imported from the root `gpt-load` module directly.

The module path is a child of CPA's module path, so it can compile the pinned
CPA implementation without copying CPA source into this repository. GPT-Load
owns persistence, account selection, affinity, retry, health, logging, and
quota policy. This bridge only exposes:

- Codex browser OAuth challenge creation and one-shot code exchange;
- strict CPA Codex JSON parsing;
- one-shot, context-aware token refresh;
- the stateless Codex HTTP executor;
- one-shot model and usage observation requests.
- Claude browser OAuth challenge creation and one-shot code exchange;
- strict CPA Claude JSON parsing and stable device identity normalization;
- one-shot, context-aware Claude token refresh;
- the stateless Claude HTTP executor and supported protocol translators.

It intentionally excludes CPA Manager, selector, pool, file store, server,
watcher, WebSocket/Auto executors, fallback, and internal retry loops.

## Pinned upstream

- Module: `github.com/router-for-me/CLIProxyAPI/v7`
- Version: `v7.2.130`

The root module consumes this bridge through a local `replace`; releases still
resolve CPA itself at the exact version recorded in both `go.mod` files and
`go.sum` files.

## Updating CPA

CPA upgrades are deliberate compatibility work, not automatic dependency
bumps:

1. Review upstream changes to Codex and Claude OAuth, token, HTTP executor,
   translation, headers, device identity, model discovery, and usage observation
   code.
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

The Claude contract also requires an explicitly selected model because account
entitlements are not inferred from an API model catalog:

```bash
CPA_LIVE_CLAUDE_CREDENTIAL_FILE=/absolute/path/to/claude.json \
CPA_LIVE_CLAUDE_MODEL=claude-model-id \
  go test -count=1 -run '^TestLiveClaudeContract$' ./embedded
```
