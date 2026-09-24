# Codex cookie-routing sidecar

This process is a local fork of GPT-Load for cookie-region experiments.

- Listen on `127.0.0.1:13001`. Production `:3001` is untouched.
- Probe traffic uses `CODEX_ROUTING_PROBE_PROXY` (zooproxy, `{session}` rotates the residential sticky ID).
- Business Codex requests stay on the group outbound proxy (direct in the current CPA Codex group).
- The observation page is `/monitor/codex-routing`.
- Verdicts do not use turn-state length 780. Success is: injected `__oailb` host stays on the same `unified-N`.

Copy `gpt-load` (linux amd64 binary) next to this compose file, copy `.env.example` to `.env`, then:

```bash
# from repo root
GOTOOLCHAIN=go1.27.0 GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o packaging/cookie-lab/gpt-load .
cp packaging/cookie-lab/.env.example packaging/cookie-lab/.env
docker compose -f packaging/cookie-lab/docker-compose.yml up -d --build
```

On CPA the sidecar listens on `127.0.0.1:13001`. Production `:3001` is not used.
