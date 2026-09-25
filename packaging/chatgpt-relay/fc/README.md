# chatgpt.com 透明中继（阿里云函数计算 FC 3.0）

**云端取票定位：有限取票＋路由复用＋协议校验。** 不鉴定底层模型身份，不承诺模型能力或输出质量。

支持两种运行模式：默认 `mint` 保留按需打票；设置 `RELAY_MODE=transparent` 后，
函数完全关闭打票分支，所有已鉴权 HTTP/WS 请求都只做透明中继。透明模式下即使
客户端误带 `X-Relay-Mint`，也会按普通请求透传，该控制头不会发给上游。

把「出口 IP / 边缘选择」搬进云函数：客户端把原本发往 `https://chatgpt.com` 的请求改发到函数，函数以 chatgpt.com 的身份回源（Host 头、TLS SNI、证书校验都按上游域名），响应原样流式回传，包括 SSE 事件流和逐条的 `Set-Cookie`。

```
客户端 ──HTTPS──▶ FC HTTP 触发器 ──▶ index.js ──TLS(SNI/Host = chatgpt.com)──▶ Cloudflare 边缘 ──▶ chatgpt.com
                                       │
                                       ├─ 默认:正常 DNS 解析 chatgpt.com
                                       └─ X-Edge-IP: <ip>:TCP 直拨该 IP,SNI/Host/证书校验不变
```

- **出口 IP**：回源从 FC 实例出网，出口是函数所在地域的阿里云地址，与客户端本地网络无关。
- **边缘选择**：默认走正常 DNS。请求带 `X-Edge-IP` 时，钉的是 Cloudflare 边缘入口；gateway 节点（unified-N）仍由 `__cflb`/`__oailb` 决定，IP 选不了节点（见 [FINDINGS.md](../FINDINGS.md)「Direct gateway access」实测）。

它只是一个中继：不改写请求内容、不缓存、不重试、不跟随重定向、不解压；支持 WebSocket：HTTP 升级握手后双向透传帧，普通 HTTP 请求仍剥离逐跳头。实现是零依赖的单文件 [`index.js`](index.js)。

例外是**打票**：带 `X-Relay-Mint` 的已鉴权请求不透传，函数代客户端向上游发送 codex ping 铸票（`X-Codex-Turn-State` + `__cflb`/`__oailb`），仅在总次数和总时限内尝试；符合票据格式、目标路由、模型声明与有效期条件后，才返回票和 pair JSON。没有有效 pair 时裸发，有符合目标的有效 pair 时定向复用——见[打票端点](#打票端点x-relay-mint)。

## 请求约定

| 请求头 | 必填 | 说明 |
|---|---|---|
| `X-Relay-Key` | 是 | 与函数环境变量 `RELAY_KEY` 相同；缺失或不符返回 403 |
| `X-Edge-IP` | 否 | 公网 IP 字面量（IPv4/IPv6）。TCP 直拨该 IP，SNI、Host、证书校验仍按上游域名 |

普通 HTTP 请求除下列请求头外，其余请求头原样转发，方法、路径、query、请求体也一样（WebSocket 的区别见下文）：

- 逐跳头：`Connection`、`Keep-Alive`、`TE`、`Trailer`、`Transfer-Encoding`、`Upgrade`、`Expect`、`Proxy-*`
- 中继控制头：`X-Relay-Key`、`X-Edge-IP`、`X-Relay-Mint`、`X-Mint-*`
- `Host`：固定改写为上游
- 身份头：`Forwarded`、`X-Forwarded-*`、`X-Real-IP`、`True-Client-IP`、`Client-IP`、`Via`
- 平台注入的 `x-fc-*`

响应的状态码、响应头（多条 `Set-Cookie` 逐条保留）和响应体原样回传，另外附加：

- `X-Relay-Edge-IP`：实际连上的边缘 IP。默认 DNS 时是解析结果，钉 IP 时就是所钉的 IP。
- `X-Accel-Buffering: no`：仅 SSE 响应。

**流式与断流**：上游不带 `Content-Length` 时（SSE 就是这样），中继按 chunked 转发，事件逐块到达客户端。响应开始后如果上游中途断开，中继会直接掐断客户端连接、不发 chunked 结束块，客户端据此能识别流不完整，不会把截断的流当成完整响应。客户端中途断开时，中继同步拆掉上游连接。

**中继自身的错误**：响应体为 `{"error":{"message":"relay: …","type":"relay_error","code":"…"}}`，同时带 `X-Relay-Error` 头：

| 状态 | `X-Relay-Error` | 含义 |
|---|---|---|
| 400 | `bad_edge_ip` | `X-Edge-IP` 不是 IP 字面量，或不是公网地址 |
| 400 | `bad_upgrade` | 升级请求不是 GET WebSocket |
| 400 | `bad_target` | 请求行不是 origin-form（`/path?query`） |
| 400 | `mint_no_auth` | 打票请求缺 `Authorization`（打票靠客户端凭据发 ping） |
| 400 | `mint_bad_params` | 打票模型列表为空等非法参数 |
| 400 | `mint_bad_cookie` | 显式路由 pair 缺失一半、重复、过期或不匹配目标；不会裸打回退 |
| 403 | `bad_relay_key` | `X-Relay-Key` 缺失或不符；`RELAY_KEY` 未配置时一律 403 |
| 500 | `relay_misconfigured` | `RELAY_UPSTREAM` 非法（不是 http(s)，或带路径、query、凭据） |
| 500 | `internal` | 中继内部异常 |
| 502 | `upstream_bad_handshake` | WebSocket 上游响应头非法或超过 16 KiB |
| 502 | `upstream_unreachable` | DNS/TCP/TLS 失败，含证书与上游域名不符 |
| 502 | `mint_rejected` | 上游返回 401/403，补票或补 pair 均立即整单停止；`error.upstream_status`/`upstream_body` 带细节 |
| 502 | `mint_exhausted` | 没有有效票，或未补到有效目标 pair；`error.last`/`gateways_seen` 带诊断 |
| 504 | `upstream_timeout` | 建连加 TLS 握手超时；WebSocket 还包括等待完整升级响应头 |

不带 `X-Relay-Error` 的 4xx/5xx 来自上游（原样透传）或 FC 平台本身（响应体是 FC 的 `ErrorCode` JSON，见下文[平台约束](#fc-平台约束)）。

## 打票端点（`X-Relay-Mint`）

已鉴权请求只要带 `X-Relay-Mint` 头就不走透传：函数以请求里的 `Authorization`/`Chatgpt-Account-Id` 为凭据向上游发 codex ping（默认 SSE POST；也可选择 WebSocket 升级到同一路径），为**每个目标模型各打一张票**。单票验收三条，全中才收：

- **票长**：`X-Codex-Turn-State` 长度 == `ticketLen`（默认 780，仅是预期格式长度，不是健康或能力信号）；
- **节点**：票所属 `__cflb`/`__oailb` 指向目标节点（默认 `unified-88`；节点名内嵌在 `__oailb` JWT 载荷里，cookie 值明文也查）；
- **模型声明**：只接受完整 JSON 事件 `type: response.created` 的 `response.model`，同时要求非空字符串 `response.id` 和 `response.model`，并精确匹配请求模型。其他事件或其他对象里的 `model` 均不参与判断；不匹配即拒收。这是上游声明，不是底层执行模型身份的独立证明。

这些是协议一致性检查，不是质量过滤器。模型名一致不能排除同名模型的内部策略变化；网关编号只用于路由验收，不能推出某个节点必然提供更强能力。声明缺失、不匹配或内容类型不符是可观测的协议失败，不自动解释为“降智”。

路由复用只处理 `__cflb` / `__oailb`，不等于所有 Cookie 都能任意混用；仍按当前凭据隔离缓存，不扩大跨账号共享范围。插件现在会从当前业务请求的 Cookie 中提取完整、未过期且匹配目标的 pair，作为本次取票的路由种子传入 FC；不会从其他账号池借用，也不转发登录会话 Cookie。没有种子时，FC 仍使用自己的有效缓存或裸打流程。没有新 `Set-Cookie` 时可依据既有有效定向 pair 验收；单独收到 `__cf_bm` 不构成新的路由 pair。

### 显式携带路由 Cookie

已鉴权打票请求可以携带标准 `Cookie` 头，但只接受 `__cflb` 和 `__oailb`。其他 Cookie 被忽略，不向上游转发。`__oailb` 必须能读到有效 `exp`，且节点符合目标；完整性/重复值/有效期/目标不符合时返回 `400 mint_bad_cookie`，一发也不打。读取 JWT 声明不是对签名的本地密码学验证，最终仍由上游验证 Cookie。

首发 SSE 或 WS 握手会带上该 pair；响应未重新下发路由 Cookie 时仍沿用它验收。不延长输入 pair 原有效期。输入 pair 变化隔离缓存；TTL=0 时也可在本次调用使用 pair，但不读写跨请求缓存。上游撤销/部分轮换/切换到非目标节点或输入 pair 到期后，显式带 Cookie 的调用立即失败，**不静默切回裸打**。总次数、总时限和严格模型声明检查仍生效。

### WebSocket 对话续链与取票的区别

`X-Relay-Mint` 是短时取票控制面：得到合法 `response.created` 后可能主动关闭上游流，返回的票不是会话 ID，也不返回可供另一条业务连接续用的 `previous_response_id`。

对话续链应使用普通 WS 隧道 `/backend-api/codex/responses`（不带 `X-Relay-Mint`）：同一个 WS 上首轮完成后，下一轮 `response.create` 设置 `previous_response_id=上一轮 response.id`，只发送新增 input。FC 保持双向透传，不改写或跨连接缓存这个 ID。`store=false` 下连接关闭后不能保证旧 ID 可恢复；不能把“取票探针创建的 ID”分发给另一个业务连接使用。[公开 Responses WS 文档](https://developers.openai.com/api/docs/guides/websocket-mode)解释了连接内续链语义，Codex 私有端点仍以实测为准。

2026-09-24 的有界实测：先从一轮真实会话抓取 `unified-125` pair，再带该 pair 建立 WS；两轮均 completed，第二轮的 previous ID 回显匹配，并准确复述首轮随机标记。实际正文在 `response.output_text.delta`，完成事件可能不携带完整 output，诊断工具同时采集两者。此结果证明该账号、路由和当次连接的续链可用，不证明模型能力或其他节点可用。

本地复现（假凭据、回环上游）运行 `python3 scripts/test_ws_chain_probe.py`；线上工具 `scripts/ws_capture_route.py` 要求 `--confirm-live`，最多 2 条连接、3 轮对话，不重试。已有 Cookie 可用 `scripts/ws_chain_probe.py` 验证单条 WS 两轮；原始 Cookie 保存在私有文件，报告只记录指纹。

### SSE / WebSocket 上游打票

打票入口仍是普通 HTTP 请求，返回 JSON；通过 `X-Mint-Transport: sse|websocket` 选择**函数到上游**的传输方式，默认读取 `MINT_TRANSPORT`，未配置时为 `sse`。值非法立即返回 400，不做自动协议降级。

```text
客户端 HTTP + X-Relay-Mint
  → FC 打票入口
    ├─ sse: POST → 完整 SSE response.created 事件
    └─ websocket: GET/101 → 发送 response.create → 完整 WS JSON 消息
  → 共同验收模型声明、票、目标网关与有效期
  → 返回票与 pair JSON
```

- **SSE**：只处理 `text/event-stream` 响应；按空行分隔完整事件，兼容跨网络包、CRLF、多行 `data:`、JSON 空白和转义。若有 SSE `event:` 字段，必须与 JSON `type` 一致。扫描上限 16 KiB，未拿到有效模型声明则拒收，不退回模糊搜索。
- **WebSocket**：校验 101 握手和 `Sec-WebSocket-Accept`，发送带客户端掩码的 `response.create`（不带 SSE 的 `stream` 字段）。支持文本分片与 ping/pong，拒绝非法帧/UTF-8，不协商压缩或子协议；单帧/单消息最多 16 KiB，握手后累计接收最多 64 KiB。每次尝试独立连接，只发一个打票请求。
- **票与 Cookie 来源**：SSE 从 HTTP 响应头取得，WebSocket 从 101 握手响应头取得。模型匹配但没有 `X-Codex-Turn-State` 时仍然失败；不从任意 JSON 字段猜票。SSE 与 WebSocket 的缓存保守隔离，不跨协议复用。
- **停止条件**：读到合法模型声明即可结束本次上游连接，再交给共同验收流程；模型不符、没票或票过期时继续重试。两种协议都使用现有单次超时、按轮冷却、pair 续期及客户端断连取消。不会等待 `response.completed`，也不会宣称已核对完整生成过程。
- **兼容边界**：普通客户端 WebSocket 请求仍走透明隧道，不会因 `X-Relay-Mint` 在 WS 握手中出现而变成打票入口；本次未新增客户端 WS 打票 RPC。

示例（SSE 只需把 `websocket` 改成 `sse` 或省略该头）：

```bash
curl -sS -X POST "https://HOST/" \
  -H "X-Relay-Key: $RELAY_KEY" -H 'X-Relay-Mint: unified-88' \
  -H 'X-Mint-Transport: websocket' -H 'X-Mint-Model: gpt-6-sol' \
  -H "Authorization: Bearer $AT" -H "Chatgpt-Account-Id: $AID"
```

返回 JSON 的 `transport` 标明本次选择。WS 请求格式参考[官方 WebSocket 模式](https://developers.openai.com/api/docs/guides/websocket-mode)，`OpenAI-Beta` 沿用项目所依赖 CPA 的 `responses_websockets=2026-02-06`。本项目使用的 `chatgpt.com/backend-api/codex/responses` 并非公开 Responses API 的稳定契约：已做本地模拟测试，未验证线上 WS 是否持续返回票头，未部署云端。

**裸打与定向**：没有可用 pair 时 ping 裸发（不带 LB cookie，边缘才会分配新节点并铸新 pair）；一旦拿到目标节点的 pair，后续模型的 ping 携带该 pair 定向打（请求钉在该节点铸票，边缘不再重铸 pair）——一对 unified-88 pair 即可服务所有模型的票。上游若在定向打时轮换了 pair，按其新节点判责，不对目标就回裸打。

**缓存与 TTL**：打出的票与 pair 按凭据摘要缓存。票的有效期 = Fernet 内嵌签发时刻 + `MINT_TICKET_TTL_S`（默认 240s，实测窗口）；pair 的死线取 `__oailb` JWT 自己的 `exp`（实测 3900s）。TTL 内复用不打，过期才重打——pair 通常比票活得久，所以常见情形是续打只需定向一发。

- 缓存命中也重新检查本次票长要求；本次有效期取「缓存原死线」与「签发时刻 + 本次 TTL」中较早者。缩短 TTL 会立即生效，增大 TTL 不会延长已缓存票的原死线；收紧只作用于本次返回，不修改共享记录。
- 票仍有效而 pair 缺失或过期时，单独裸打补 pair，保留已有票。每轮补 pair 共享最多 `maxAttempts` 次预算，同时计入整次调用总预算，不按每个模型重复计算；400/404/422 时换其他已有票对应的模型，401/403 整单停止。返回的 `attempts` 包含补票和补 pair 的全部请求。
- 成功返回前再次验期：启用 TTL 时，已过期的票不会出现在 `tickets` 中，对应模型在 `errors` 中标记 `expired_ticket`；没有有效票或没有有效目标 pair 就返回 `502 mint_exhausted`。上游新发的票若已过期，也计为失败尝试，不写缓存。
- TTL 为 `0` 表示禁用跨请求缓存：票与 pair 均不读、不写缓存，只返回本次取得的票（不应用 TTL 有效期筛选）；同一次调用仍复用有效 pair。缓存只在当前实例内，实例重启丢失，不跨实例共享。


**有限预算内冷却续打**：同一次 HTTP 请求内按轮执行，默认整次最多 24 次上游尝试、75 秒总时限（含冷却与读取响应）。所有模型与补 pair 共用总次数，`X-Mint-Attempts` 不能提高总预算。每轮先处理各缺失模型，再按需补 pair；仍有可重试失败时，等待 `MINT_RETRY_COOLDOWN_MS`（默认 30 秒），然后开启下一轮。等待期间不发上游请求，已成功且仍有效的票不会重复打；冷却后重新检查票与 pair 的有效期。`attempts` 跨轮累计，日志中的 `mint.cooldowns` 记录冷却次数。

- 401/403 仍立即整单停止；400/404/422 对应模型不参加后续轮次。无可重试目标时不会无意义地一直等待。
- 客户端断开被运行时观察到时立即取消冷却计时器与在途请求。FC 网关可能保持内部连接，因此不能只依赖 `close`：服务端总时限会独立取消 SSE/WS 在途请求和冷却。
- 总次数或总时限耗尽后结束调用；没有有效票/pair 时返回 `502 mint_exhausted`，消息分别包含 `total_attempt_limit` 或 `total_timeout`。已有仍有效票和目标 pair 时保留部分成功，未完成模型列入 `errors`。不自动修改目标网关，不跳过模型验收。
- 总限制仅由服务端环境变量配置，不新增客户端请求头或改动 JSON 响应结构。建议将总时限设为小于插件 `timeout_ms` 的值，给网络回传留余量；客户端取消不再是唯一终止保障。
- 打票载荷显式携带 `instructions: ""`。HTTP 200 中的 SSE `error` / `response.failed` 不再当作正常结束：明确的无效请求/模型错误终止对应模型，认证/权限拒绝立即整单停止（即使事件没有数值状态），429/服务端错误只在有限预算内重试；WS 沿用相同规则。未知错误正文不写日志。
- 这是当前请求内的等待，不是后台任务，也不是跨请求、跨实例的全局限流。客户端及中间代理应给足请求超时；FC 执行超时仍是外层限制，现有部署配置未修改。

返回 `200` JSON（`Cache-Control: no-store`）：

```json
{
  "transport": "sse",
  "gateway": "unified-88",
  "cookies": { "__cflb": "…", "__oailb": "…" },
  "cookie_header": "__cflb=…; __oailb=…",
  "expires_at": "<pair 死线，__oailb JWT exp，ISO>",
  "edge_ip": "<pair 铸出时连上的边缘>",
  "attempts": 4,
  "tickets": {
    "gpt-6-sol": {
      "turn_state": "<780 字符票>", "ticket_len": 780,
      "served_model": "gpt-6-sol",
      "issued_at": "<签发时刻>", "expires_at": "<票死线>", "age_s": 3,
      "cached": false
    },
    "gpt-6-luna": { "…": "…" },
    "gpt-6-astra": { "…": "…" }
  }
}
```

`cookie_header` 可直接作为 `Cookie` 请求头，与各票的 `turn_state`（`X-Codex-Turn-State` 头）一起回放。单模型调用额外平铺 `model`/`turn_state`/`ticket_len`/`served_model` 四个字段。模型被永久拒绝时，在有效 pair 存在的前提下，其余有效票照给，失败明细在 `errors` 字段。可重试失败达到本轮上限后，仅在总预算和总时限仍允许时冷却续打；总限制耗尽或无可重试模型时进入最终验期，无有效票/pair 就返回 `mint_exhausted`。

打票参数按「请求头 > 环境变量 > 默认」取值：

| 请求头 | 环境变量 | 默认 | 说明 |
|---|---|---|---|
| `X-Relay-Mint`（值） | — | — | 触发头；值写成 `unified-N` 样文本时同时指定目标网关 |
| `X-Mint-Transport` | `MINT_TRANSPORT` | `sse` | 上游传输：`sse` 或 `websocket`，不自动降级 |
| `X-Mint-Gateway` | `MINT_GATEWAY` | `unified-88` | 目标网关；`88`/`unified_88` 都规范化为 `unified-88`，`any` 不查 |
| `X-Mint-Models` | `MINT_MODELS` | `gpt-6-sol,gpt-6-luna,gpt-6-astra` | 要打票的模型，逗号分隔 |
| `X-Mint-Model` | `MINT_MODEL` | — | 单模型写法，等价于 `X-Mint-Models` 只填一个 |
| `X-Mint-Len` | `MINT_TICKET_LEN` | `780` | 票长验收；`0` 不查 |
| `X-Mint-TTL` | `MINT_TICKET_TTL_S` | `240` | 票缓存秒数；`0` 不缓存，每次都打 |
| `X-Mint-Attempts` | `MINT_MAX_ATTEMPTS` | `24` | 每轮每模型尝试上限（含首次），钳到 1–128 |
| 不支持请求头覆盖 | `MINT_MAX_TOTAL_ATTEMPTS` | `24` | 整次调用总尝试上限，所有模型/补 pair 共用，钳到 1–128 |
| 不支持请求头覆盖 | `MINT_TOTAL_TIMEOUT_MS` | `75000` | 整次调用总时限，含读取和冷却，钳到 1–180000 ms |

`X-Edge-IP` 对打票同样生效：钉哪个边缘 IP 就从哪里打。运行时观察到客户端断开即停打并拆掉在途上游请求，未观察到断开仍由总时限封顶（已入缓存的票/pair 不丢）。上游回 401/403（凭据被拒）整单停，400/404/422 只弃当前模型，其余失败计入该模型的尝试次数。

```bash
curl -sS "https://<函数地址>/" \
  -H "X-Relay-Key: $RELAY_KEY" -H "X-Relay-Mint: unified-88" \
  -H "Authorization: Bearer $AT" -H "Chatgpt-Account-Id: $AID"
```

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `RELAY_KEY` | 无（必填） | 客户端 `X-Relay-Key` 须与之相同；未设置时拒绝一切请求 |
| `RELAY_MODE` | `mint` | `mint`：带 `X-Relay-Mint` 时打票；`transparent`：完全关闭打票，只做透明中继 |
| `RELAY_UPSTREAM` | `https://chatgpt.com` | 上游源站，只能写 `scheme://host[:port]`：请求路径原样取自客户端，写了路径会报 `relay_misconfigured` |
| `RELAY_CONNECT_TIMEOUT_MS` | `10000` | HTTP 只管建连加 TLS 握手；WebSocket 还管完整升级响应头。建立隧道后不设空闲超时。HTTP 响应阶段不设限：非流式调用要等模型算完才回头部，SSE 也允许长时间静默 |
| `ALLOW_PRIVATE_EDGE_IPS` | 未设 | 仅供测试：设为 `1` 时 `X-Edge-IP` 允许私网地址 |
| `MINT_TRANSPORT` | `sse` | 默认上游打票传输，可被 `X-Mint-Transport` 覆盖 |
| `MINT_GATEWAY` | `unified-88` | 打票目标网关；`any`/`*` 表示不查网关 |
| `MINT_MODELS` | `gpt-6-sol,gpt-6-luna,gpt-6-astra` | 打票的模型集，逗号分隔（`MINT_MODEL` 可写单个） |
| `MINT_TICKET_LEN` | `780` | 票长验收条件；`0` 表示不查票长 |
| `MINT_TICKET_TTL_S` | `240` | 票缓存秒数（Fernet 签发时刻起算）；`0` 不缓存 |
| `MINT_MAX_ATTEMPTS` | `24` | 每轮每模型尝试上限（1–128，含首次） |
| `MINT_MAX_TOTAL_ATTEMPTS` | `24` | 整次调用所有模型和补 pair 的总尝试上限（1–128），不能通过请求头扩大 |
| `MINT_TOTAL_TIMEOUT_MS` | `75000` | 整次调用总时限（1–180000 ms），到期取消冷却和在途 SSE/WS；建议小于插件 timeout_ms |
| `MINT_RETRY_COOLDOWN_MS` | `30000` | 每轮耗尽后冷却毫秒数，仅环境变量配置，钳到 1–300000；不支持设为 0 禁用等待 |
| `MINT_ATTEMPT_TIMEOUT_MS` | `60000` | 单发打票的整体超时 |
| `FC_SERVER_PORT` | `9000` | 监听端口，须与 `customRuntimeConfig.port` 一致 |

## 部署

### 透明模式（不打票）

部署前将函数切换为透明模式：

```bash
cd relay
export RELAY_KEY="$(openssl rand -hex 32)"
export RELAY_MODE=transparent
s deploy -y
```

该模式仍要求客户端发送 `X-Relay-Key`，但不会读取 `X-Relay-Mint`，也不会向上游
发起 Codex ping。请求路径、请求体、Authorization、Cookie 和 SSE/WS 流按透明中继
规则处理。

### 通过前置代理访问云函数

前置代理位于客户端与函数之间，函数本身不需要知道代理地址：

```text
CPA/客户端 ── SOCKS5H/HTTP 前置代理 ──▶ FC 透明网关 ──▶ chatgpt.com
```

命令行验证可使用：

```bash
curl --proxy "$FRONT_PROXY" "https://HOST/backend-api/codex/responses" \
  -H "X-Relay-Key: $RELAY_KEY" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Chatgpt-Account-Id: $ACCOUNT_ID"
```

在 CPA 中应把前置代理配置在实际发起业务请求的出口上；不要把代理密码写入
`relay/s.yaml`、URL 查询参数或日志。若只想让云端打票走代理，才使用插件的
`cloud_mint.proxy_env`/`proxy_url`；透明模式不需要云端打票配置。若 CPA 仍安装
云端打票插件，应同时把 `cloud_mint.enabled` 设为 `false`，避免插件把打票请求
发到一个只提供透明透传的函数。

### Serverless Devs（推荐）

```bash
npm i -g @serverless-devs/s          # Serverless Devs 3.x
s config add                          # 配置阿里云 AccessKey,别名 default
cd relay
export RELAY_KEY="$(openssl rand -hex 32)"
s deploy -y                           # 删除:s remove
```

[`s.yaml`](s.yaml) 的要点：

- **运行时**：`custom.debian10`，自带 Node.js 20，启动命令为 `/var/fc/lang/nodejs20/bin/node index.js`，端口 9000。
- **超时**：`timeout: 3600`。平台默认只有 3 秒，不设会截断流。
- **并发**：`instanceConcurrency: 100`。
- **触发器**：HTTP 触发器，平台层匿名（`anonymous`），鉴权由函数自己做。
- **代码包**：[`.fcignore`](.fcignore) 排除测试和文档，只上传 `index.js`。

部署输出里的 HTTP 触发器地址（形如 `https://chatgpt-relay-xxxx.ap-southeast-1.fcapp.run`）就是中继入口。

### 控制台

1. 创建 **Web 函数**，运行环境选「自定义运行时 Debian 10」。启动命令填 `/var/fc/lang/nodejs20/bin/node index.js`，监听端口填 `9000`。
2. 代码上传 `index.js` 这一个文件即可。
3. 环境变量加 `RELAY_KEY`。执行超时 3600 秒，单实例并发 100，规格 0.5 vCPU / 512 MB。
4. 触发器选 HTTP 触发器，认证方式「无需认证」，请求方法全选。

### 地域

函数地域就是回源出口所在地。OpenAI 不向中国内地、香港等地区提供服务，函数要部署在它支持的国家/地区，例如新加坡 `ap-southeast-1`、东京 `ap-northeast-1`、硅谷 `us-west-1`、法兰克福 `eu-central-1`。改 `s.yaml` 的 `vars.region` 即可；同时部署到多个地域，就得到多个出口。

### 固定出口 IP（可选）

FC 实例默认经平台的动态地址出网。需要固定出口 IP 时，给函数配置 VPC，并在该 VPC 里用 NAT 网关加 EIP 做 SNAT。具体步骤见 FC 文档「配置固定公网 IP 地址」。

## 接入

### curl

```bash
curl -N "https://<函数地址>/backend-api/codex/responses" \
  -H "X-Relay-Key: $RELAY_KEY" \
  -H "X-Edge-IP: <cloudflare-ip>" \
  -H "Authorization: Bearer $AT" -H "Chatgpt-Account-Id: $AID" \
  -H "Content-Type: application/json" -H "Accept: text/event-stream" \
  --data-binary @ping.json
```

`X-Edge-IP` 那一行可以省略，省略时走 DNS。`ping.json` 同 [HANDOFF.md](../HANDOFF.md)「复现方法」。

### CPA：原生插件 + 云端打票

推荐接入方式及完整配置见 [CPA 原生插件接入](CPA.md)。插件只读当前选中账号，向 FC 取票后注入业务请求；不修改 CPA 核心、OAuth 刷新和业务回源地址。

注意：给 Codex OAuth JSON 增加 `base_url` 不等于执行器一定使用该地址。当前所核对版本的执行器读取 Auth.Attributes，而文件加载器不为 Codex 自动映射该字段。此前的直接改 JSON 示例不能作为已验证部署方式。

## WebSocket 接入

同一 HTTP 监听端口同时处理 HTTP/SSE 与 WebSocket，不新增依赖，也不新增触发器。
客户端把函数 URL 的 `https://` 改为 `wss://`（本地 `http://` 改为 `ws://`），保留路径与 query，例如：

```text
wss://HOST/backend-api/codex/responses
```

- 握手带 `X-Relay-Key`；上游需要的 `Authorization`、`Cookie`、`Chatgpt-Account-Id` 等照常携带。`X-Edge-IP` 仍可选，公网 IP 校验与 HTTP 路径一致。
- 上游地址仍使用 `RELAY_UPSTREAM=http(s)://...`：`https` 对应 TLS 回源；钉 IP 不改变 Host、SNI 与证书校验目标。
- 保留 `Sec-WebSocket-*` 协商头；重建 `Connection: Upgrade` / `Upgrade: websocket`，剥离其他逐跳头（包括 `Connection` 指名的头）、中继控制头与平台身份头。
- 完整响应头到达后，101、握手头（含多条 `Set-Cookie`）与随包首帧原样返回；上游拒绝升级的 HTTP 响应也透传。WS 路径不额外注入 `X-Relay-Edge-IP` 响应头。
- 文本、二进制、分片、压缩协商、ping/pong 与 close 帧均按字节透传，中继不生成业务消息、不自动重连，也不执行打票；升级请求中的 `X-Relay-Mint` 只会被剥离。
- TCP/TLS 与等待完整握手响应头共用 `RELAY_CONNECT_TIMEOUT_MS`；握手后不设空闲超时，任一端断开会释放对端连接。

可用支持自定义握手头的 WebSocket 客户端连接。浏览器原生 `WebSocket` 无法设置 `X-Relay-Key` 等任意请求头，因此不能直接使用当前鉴权方式；不要把 key 放到 URL 查询参数中。

仅检查握手时可用以下命令（不会发送业务帧；连接成功后到达 10 秒时 curl 会主动退出）：

```bash
curl --http1.1 -i -N --max-time 10 "https://HOST/backend-api/codex/responses" \
  -H "X-Relay-Key: $RELAY_KEY" \
  -H "Authorization: Bearer $AT" -H "Chatgpt-Account-Id: $AID" \
  -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Version: 13' \
  -H "Sec-WebSocket-Key: $(openssl rand -base64 16)"
```

FC 的 HTTP 触发器需允许 `GET`，现有 `s.yaml` 已包含；连接时长受函数 `timeout` 限制（当前配置 3600 秒），客户端应实现 ping/pong 与断线重连。平台配置与限制见[阿里云 WebSocket 官方文档](https://help.aliyun.com/zh/functioncompute/configure-an-http-trigger-for-a-function-that-is-triggered-by-websocket-requests-1)。本地测试不等同于云端部署验证。

## FC 平台约束

以下约束来自 FC 3.0 文档（Web 函数、自定义运行时），中继已按此实现：

- **流式判定**：Web 函数按响应头是否带 `Transfer-Encoding: chunked` 判定流式。中继对 SSE 按 chunked 转发（有测试钉住）。
- **HTTP Server 要求**：监听 `0.0.0.0`，连接保持 keep-alive，服务端超时不小于函数最大运行时长（24h），120 秒内完成启动。`createServer()` 把 keep-alive、请求和 socket 三项超时全部关掉。
- **大小上限**：请求头总大小 ≤ 8 KB，路径加 query ≤ 4 KB，同步调用请求体 ≤ 32 MB，超限时平台直接回 400 `InvalidArgument`。响应头总大小 ≤ 8 KB，超限时平台回 502 `BadResponse`。
- **保留头**：客户端不能自定义 `x-fc-*` 请求头（平台保留）。响应头里的 `date`、`server`、`content-length` 等由平台接管。
- **默认域名**：使用平台默认域名时，平台会强制加上 `content-disposition: attachment`。API 客户端不受影响；需要去掉时绑定自定义域名。
- **超时**：函数 `timeout` 就是单条流的最长时长，平台上限 86400 秒。

## 本地开发与测试

```bash
cd relay
node --test                                    # 全部只连本机
docker run --rm --network none -v "$PWD":/app:ro -w /app \
  node:20.10.0-bookworm-slim node --test       # 与 custom.debian10 内置 Node 同版本
RELAY_KEY=dev node index.js                    # 本地起服务,监听 0.0.0.0:9000(会真实回源)
```

TLS 用例以子进程运行真实入口 `node index.js`，证书校验保持开启，测试证书经 `NODE_EXTRA_CA_CERTS` 信任。中继本身没有、也不该有「跳过证书校验」的开关。

## 安全

- **鉴权 fail-closed**：`RELAY_KEY` 未配置时拒绝一切请求。key 用定长摘要比较（`timingSafeEqual`）。公网触发器上这是唯一防线：用足够长的随机值，泄露即轮换。本函数会转发 `Authorization`，等同于凭据代理。
- **防 SSRF**：`X-Edge-IP` 只接受公网 IP 字面量。私网、回环、链路本地、CGNAT（`100.64.0.0/10`，含阿里云元数据地址 `100.100.100.200`）以及文档和保留网段一律返回 400，IPv4-mapped IPv6 按 IPv4 规则判定。
- **不外泄平台身份**：剥掉 `x-fc-*`（函数配置了角色时，平台会在请求头里注入临时 STS 凭据）和 `X-Forwarded-*` 等身份头，不把平台凭据和调用方 IP 带给上游。
- **日志**：请求结束输出汇总 JSON；打票另输出 `mint_attempt`（脱敏指纹、票长、票龄、模型声明、网关变化，以及 request_id / wanted_gateway / requested_model / reject_reason）及 `mint_cooldown`。`candidate_ok` 仅表示单发初筛通过，最终仍验期；汇总日志 `mint.stop_reason` 标识总预算/总时限/客户端取消。最终 JSON/错误响应中的 `attempt_log` 最多保留最近 40 次记录，供原生插件展示在标准日志中。不输出原始票、Cookie 或凭据。
