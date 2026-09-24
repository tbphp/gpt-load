# chatgpt-relay

Transparent chatgpt.com reverse proxy for oracle-sg / home / FC.

- `Host` / TLS SNI stay `chatgpt.com`
- `X-Edge-IP` dials that address
- `X-Relay-Key` required
- Mint / hunt-88 / ticket cache live in gpt-load, not here

```
RELAY_KEY=...
FC_SERVER_PORT=19000
node index.js
```
