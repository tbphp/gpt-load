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

## Provider credential exposure boundary

Provider keys and any derived fragments, including prefix/suffix masks, must not appear in data-plane response headers or bodies, request logs, or the request-log management API. Request-log attempts identify an upstream key only by its internal `key_id`.

The authenticated Group upstream-key management list is the sole approved exception: it may return the existing mask so an operator can identify a key in the provider console. It remains protected by management authentication and never exposes plaintext credentials.
