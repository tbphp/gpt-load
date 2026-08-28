# Third-Party Notices

GPT-Load includes third-party open-source software. This file covers the
components that require specific attribution, carry obligations beyond
attribution, or are modified by GPT-Load. Each release also ships a CycloneDX
SBOM (`bom.cdx.json`) inventorying the resolved Go module graph.

## Bifrost Core

- Module: `github.com/maximhq/bifrost/core`
- Version: `v1.8.4`
- Copyright: 2025 H3 Labs Inc.
- License: Apache License 2.0

GPT-Load uses Bifrost Core as an infrastructure adapter for provider execution
and protocol conversion. GPT-Load's domain models, persisted channel IDs,
scheduling, retry policy, health state, usage accounting, and pricing remain
owned by GPT-Load.

The complete Apache License 2.0 text is distributed in
`LICENSES/Apache-2.0.txt`.

## CLIProxyAPI

- Module: `github.com/router-for-me/CLIProxyAPI/v7`
- Version: `v7.2.143`
- Copyright: 2025-2005.9 Luis Pater; 2025.9-present Router-For.ME
- License: MIT License

GPT-Load uses a pinned, execution-only embedded adapter around CLIProxyAPI's
Codex, Claude, Antigravity, and xAI OAuth and HTTP executor code. GPT-Load retains ownership of
credential storage, account selection, retry, health, affinity, logging, and
usage policy; the embedded adapter does not use CLIProxyAPI's manager, pool,
file store, WebSocket executor, fallback, or automatic retry.

The complete MIT License text is distributed in `LICENSES/MIT.txt`.

## fasthttp

- Module: `github.com/valyala/fasthttp`
- Replaced by: `github.com/tbphp/fasthttp v1.73.1-0.20260828150536-1c6c09a6f6bc`
- Copyright: 2015-present Aliaksandr Valialkin, VertaMedia, Kirill Danshin, Erik
  Dubbelboer, FastHTTP Authors
- License: MIT License

GPT-Load builds against a pinned fork carrying an unreleased upstream stream
lifecycle fix (<https://github.com/valyala/fasthttp/pull/2353>). The fork keeps
the original copyright and MIT license unchanged.

The complete MIT License text is distributed in `LICENSES/MIT.txt`.

## Go MySQL Driver

- Module: `github.com/go-sql-driver/mysql`
- Version: `v1.8.1`
- Copyright: 2012 The Go-MySQL-Driver Authors
- License: Mozilla Public License 2.0

Linked unmodified, through `gorm.io/driver/mysql`, for MySQL support. As required
by MPL-2.0 Section 3.2, the Source Code Form for this version is available under
the terms of the MPL at
<https://github.com/go-sql-driver/mysql/tree/v1.8.1>.

The complete Mozilla Public License 2.0 text is distributed in
`LICENSES/MPL-2.0.txt`.

## Lobe Icons

- Source: `@lobehub/icons-static-svg` `1.94.0` (vendored subset, not an npm
  dependency of the management UI)
- Copyright: 2023 LobeHub
- License: MIT License

GPT-Load vendors a subset of Lobe Icons' SVG marks (`web/src/assets/channels/`)
to identify built-in channel presets by their upstream provider's brand in the
management UI. The vendored icons and this notice do not grant any trademark
rights in the marks they depict.

The complete MIT License text is distributed in `LICENSES/MIT.txt`.
