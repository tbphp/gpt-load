# Third-Party Notices

GPT-Load includes third-party open-source software. The release SBOM contains
the complete resolved dependency inventory.

## Bifrost Core

- Module: `github.com/maximhq/bifrost/core`
- Version: `v1.7.11`
- Copyright: 2025 H3 Labs Inc.
- License: Apache License 2.0

GPT-Load uses Bifrost Core as an infrastructure adapter for provider execution
and protocol conversion. GPT-Load's domain models, persisted channel IDs,
scheduling, retry policy, health state, usage accounting, and pricing remain
owned by GPT-Load.

The complete Apache License 2.0 text is distributed in
`LICENSES/Apache-2.0.txt`.

## Lobe Icons

- Module: `@lobehub/icons-static-svg`
- Version: `1.94.0`
- Copyright: 2023 LobeHub
- License: MIT License

GPT-Load vendors a subset of Lobe Icons' SVG marks (`web/src/assets/channels/`)
to identify built-in channel presets by their upstream provider's brand in the
management UI. The vendored icons and this notice do not grant any trademark
rights in the marks they depict.

The complete MIT License text is distributed in `LICENSES/MIT.txt`.
