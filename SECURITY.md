# Security Policy

## Supported versions

| Version line | Status |
|---|---|
| `2.0.x` | Pre-release security-supported candidate |
| `1.4.x` | Maintained security support line |

The repository's 2.0 candidate receives security fixes while its first public release is being prepared. This support status does not assert release readiness or public availability. No `v2.0.0` tag, GitHub Release, public binary, or public container image is confirmed here; verify availability from actual public assets.

## Reporting a vulnerability

Report suspected vulnerabilities through [GitHub Private Vulnerability Reporting](https://github.com/tbphp/gpt-load/security/advisories/new).

Please do not open a public issue for an undisclosed vulnerability. Use the private report so the maintainers can investigate and coordinate a fix before public disclosure.

## Channel credential exposure boundary

Channel credentials and any derived secret fragments must not appear in data-plane response headers or bodies, request logs, or the request-log management API. Request-log attempts identify the selected credential only by its internal `credential_id`, together with the non-secret `channel_id`.

The authenticated Group credential list is the sole approved mask exception: it may return a safe identifier mask so an operator can identify a credential in the provider console. Plaintext reveal is a separate authenticated, explicitly invoked endpoint and must never be logged or returned by collection, health, usage, or request-log APIs.
