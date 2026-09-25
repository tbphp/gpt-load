'use strict';
// 阿里云函数计算(FC 3.0 Web 函数)— chatgpt.com 透明中继
//
// 客户端把原本发往 https://chatgpt.com 的请求改发到本函数;函数以 chatgpt.com
// 的身份回源(Host 头、TLS SNI、证书校验都按上游域名),响应——含 SSE 流与逐条
// Set-Cookie——原样流式回传。等于把两件事搬进云函数:
//
//   出口 IP  —— 回源从 FC 实例出网,出口是函数所在地域的阿里云地址;
//   边缘选择 —— 默认按正常 DNS 解析上游;请求带 X-Edge-IP: <ip> 时 TCP 直拨该
//               IP,SNI/Host/证书仍是上游域名(「边缘 IP + chatgpt.com SNI」通路)。
//               钉的是 Cloudflare 边缘;gateway 节点仍由 __cflb/__oailb 决定
//               (见 FINDINGS.md「Direct gateway access」)。
//
// 形态:零依赖 Node HTTP server。FC 自定义运行时以 `node index.js` 启动,监听
// $FC_SERVER_PORT(默认 9000)。FC 以响应是否 chunked 判定流式;上游响应不带
// Content-Length 时(SSE 即是),本服务按 chunked 转发,事件因此逐块到达客户端。
// 部署(s.yaml)与接入方式见同目录 README.md。
//
// 打票:已鉴权请求带 X-Relay-Mint 时不走透传 —— 函数以客户端的
// Authorization/Chatgpt-Account-Id 向上游发 codex ping 铸票:每个目标模型
// 各一张,验收票长(默认 780)、所属 pair 的目标网关(默认 unified-88,
// __oailb JWT 内嵌节点名)与上游模型声明；这些检查不能证明实际能力或排除隐性降级。
// 打出的票与 pair 按凭据缓存,TTL 内复用、过期重打,最后把票和 pair 以
// JSON 返回。打票不依赖任何客户端会话状态,等同一个按需铸票端点;其余
// 请求照常透明中继。RELAY_MODE=transparent 时完全关闭这条分支,所有请求
// 都只做透明中继,即使客户端误带 X-Relay-Mint 也不会打票。
//
// 鉴权:必须设置 RELAY_KEY,请求带同值 X-Relay-Key 才放行;未设置则全拒。本函数
// 转发 Authorization,等于凭据代理,公网触发器上不能不设防。

const crypto = require('node:crypto');
const http = require('node:http');
const https = require('node:https');
const net = require('node:net');
const tls = require('node:tls');
const { pipeline } = require('node:stream');

const UPSTREAM_DEFAULT = 'https://chatgpt.com';
const CONNECT_TIMEOUT_DEFAULT_MS = 10_000;
const WS_RESPONSE_HEADER_LIMIT = 16 * 1024;
const RELAY_MODE_DEFAULT = 'mint';

// 打票默认值:模型/验收参数可被 X-Mint-* 覆盖;冷却和整次调用上限只能由服务端配置。
const MINT_PATH = '/backend-api/codex/responses';
const MINT_UA = 'codex-tui/0.154.0 (Ubuntu 24.04; x86_64) OVH (codex-tui; 0.154.0)';
const MINT_GATEWAY_DEFAULT = 'unified-88';
const MINT_MODELS_DEFAULT = 'gpt-6-sol,gpt-6-luna,gpt-6-astra';
const MINT_TICKET_LEN_DEFAULT = 780;
const MINT_TICKET_TTL_DEFAULT_S = 240; // 票实测可用窗口 ~240s(FINDINGS.md)
const MINT_MAX_ATTEMPTS_DEFAULT = 24;
// 整次调用独立封顶；FC 网关可能不会把调用方断连传给运行时。
const MINT_MAX_TOTAL_ATTEMPTS_DEFAULT = 24;
const MINT_TOTAL_TIMEOUT_DEFAULT_MS = 75_000;
const MINT_TOTAL_TIMEOUT_MAX_MS = 180_000;
const MINT_RETRY_COOLDOWN_DEFAULT_MS = 30_000;
const MINT_RETRY_COOLDOWN_MAX_MS = 300_000;
const MINT_ATTEMPT_TIMEOUT_DEFAULT_MS = 60_000;
const MINT_CACHE_MAX = 256;
// 只认完整 response.created 的模型声明;SSE 扫描/WS 单消息上限 16 KiB。
// 没有合法事件就拒收,不退回模糊字段搜索。
const MINT_BODY_SCAN_BYTES = 16 * 1024;
const MINT_SNIPPET_BYTES = 512; // 非 200 响应留这么长一段诊断体
// 凭据/载荷被上游正面拒绝:重试不会变出别的结果,循环直接停。
const MINT_FATAL_STATUS = new Set([400, 401, 403, 404, 422]);
// 节点名内嵌在 __oailb JWT 载荷里(chat.gateway.unified-N.api.openai.com),也可能
// 出现在 cookie 值明文里;与 Go 侧 routeCookieGatewayRe 同款宽松写法。
const MINT_GATEWAY_RE = /unified[-_.]?(\d+)|gateway[-_.][a-z0-9-]+/i;

// 不逐跳转发的请求头:连接级字段;Host 固定改写为上游;中继自己的控制头;
// x-forwarded-* 等会把真实调用方暴露给上游;x-fc-* 是平台注入的(函数配了
// 角色时含临时 AK/STS 凭据),绝不能外发。
const DROP_REQUEST_HEADERS = new Set([
  'host', 'connection', 'keep-alive', 'proxy-authorization', 'proxy-connection',
  'te', 'trailer', 'transfer-encoding', 'upgrade', 'expect',
  'x-relay-key', 'x-edge-ip', 'x-relay-mint',
  'forwarded', 'x-forwarded-for', 'x-forwarded-host', 'x-forwarded-port',
  'x-forwarded-proto', 'x-forwarded-scheme', 'x-forwarded-server',
  'x-real-ip', 'true-client-ip', 'client-ip', 'via',
]);
const DROP_REQUEST_PREFIXES = ['x-fc-', 'x-mint-'];
const DROP_RESPONSE_HEADERS = new Set([
  'connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization',
  'te', 'trailer', 'transfer-encoding', 'upgrade',
]);
// WS 只重建 Connection/Upgrade/Host,保留 sec-websocket-* 协商字段;
// 剥离其余逐跳头、中继控制头、身份头与平台注入头。
const WS_DROP_REQUEST_HEADERS = new Set([
  'host', 'connection', 'upgrade', 'keep-alive', 'te', 'trailer',
  'transfer-encoding', 'content-length', 'proxy-authorization', 'proxy-connection', 'expect',
  'x-relay-key', 'x-edge-ip', 'x-relay-mint',
  'forwarded', 'x-forwarded-for', 'x-forwarded-host', 'x-forwarded-port',
  'x-forwarded-proto', 'x-forwarded-scheme', 'x-forwarded-server',
  'x-real-ip', 'true-client-ip', 'client-ip', 'via',
]);

// X-Edge-IP 只接受公网 IP 字面量,防止把中继当内网探针(SSRF)。
// IPv4-mapped IPv6(::ffff:a.b.c.d)按 IPv4 规则判定 —— BlockList 自带这层映射。
const NON_PUBLIC = new net.BlockList();
for (const [prefix, bits] of [
  ['0.0.0.0', 8], ['10.0.0.0', 8], ['100.64.0.0', 10], ['127.0.0.0', 8],
  ['169.254.0.0', 16], ['172.16.0.0', 12], ['192.0.0.0', 24], ['192.0.2.0', 24],
  ['192.168.0.0', 16], ['198.18.0.0', 15], ['198.51.100.0', 24],
  ['203.0.113.0', 24], ['224.0.0.0', 3], // 组播 + 保留 + 广播
]) NON_PUBLIC.addSubnet(prefix, bits, 'ipv4');
for (const [prefix, bits] of [
  ['::', 128], ['::1', 128], ['64:ff9b::', 96], ['100::', 64],
  ['2001:db8::', 32], ['fc00::', 7], ['fe80::', 10], ['ff00::', 8],
]) NON_PUBLIC.addSubnet(prefix, bits, 'ipv6');

function isPublicIP(ip) {
  const family = net.isIP(ip);
  return family !== 0 && !NON_PUBLIC.check(ip, family === 4 ? 'ipv4' : 'ipv6');
}

// 每请求读取:部署后环境变量不变,读取成本可忽略,测试可逐用例切换。
function config() {
  const mode = String(process.env.RELAY_MODE || RELAY_MODE_DEFAULT).trim().toLowerCase();
  if (mode !== 'mint' && mode !== 'transparent') {
    throw new Error('RELAY_MODE must be mint or transparent');
  }
  const upstream = new URL(process.env.RELAY_UPSTREAM || UPSTREAM_DEFAULT);
  if (upstream.protocol !== 'https:' && upstream.protocol !== 'http:') {
    throw new Error(`RELAY_UPSTREAM must be an http(s) URL, got ${upstream.protocol}`);
  }
  // 请求路径原样取自客户端,这里写的路径不会被拼接 —— 与其静默忽略,不如直接报错。
  if (upstream.pathname !== '/' || upstream.search || upstream.hash || upstream.username || upstream.password) {
    throw new Error('RELAY_UPSTREAM must be an origin (scheme://host[:port]) without path, query or credentials');
  }
  const connectTimeoutMs = Number(process.env.RELAY_CONNECT_TIMEOUT_MS);
  const attemptTimeoutMs = Number(process.env.MINT_ATTEMPT_TIMEOUT_MS);
  return {
    mode,
    relayKey: process.env.RELAY_KEY || '',
    upstream,
    connectTimeoutMs: connectTimeoutMs > 0 ? connectTimeoutMs : CONNECT_TIMEOUT_DEFAULT_MS,
    allowPrivateEdge: process.env.ALLOW_PRIVATE_EDGE_IPS === '1',
    mint: {
      transport: process.env.MINT_TRANSPORT || 'sse',
      gateway: process.env.MINT_GATEWAY || MINT_GATEWAY_DEFAULT,
      models: parseModels(process.env.MINT_MODELS || process.env.MINT_MODEL || MINT_MODELS_DEFAULT),
      // 0 表示不查票长 —— 上游改过签名格式(292→780),留个不更新代码的逃生口。
      ticketLen: nonNegInt(process.env.MINT_TICKET_LEN, MINT_TICKET_LEN_DEFAULT),
      // 票的有效窗口:命中缓存直接复用,过期才重打。0 = 不缓存,每次都打。
      ticketTtlS: nonNegInt(process.env.MINT_TICKET_TTL_S, MINT_TICKET_TTL_DEFAULT_S),
      maxAttempts: clampInt(process.env.MINT_MAX_ATTEMPTS, MINT_MAX_ATTEMPTS_DEFAULT, 1, 128),
      maxTotalAttempts: clampInt(process.env.MINT_MAX_TOTAL_ATTEMPTS, MINT_MAX_TOTAL_ATTEMPTS_DEFAULT, 1, 128),
      totalTimeoutMs: clampInt(process.env.MINT_TOTAL_TIMEOUT_MS,
        MINT_TOTAL_TIMEOUT_DEFAULT_MS, 1, MINT_TOTAL_TIMEOUT_MAX_MS),
      retryCooldownMs: clampInt(process.env.MINT_RETRY_COOLDOWN_MS,
        MINT_RETRY_COOLDOWN_DEFAULT_MS, 1, MINT_RETRY_COOLDOWN_MAX_MS),
      attemptTimeoutMs: attemptTimeoutMs > 0 ? attemptTimeoutMs : MINT_ATTEMPT_TIMEOUT_DEFAULT_MS,
    },
  };
}

function nonNegInt(v, dflt) {
  if (v === undefined || v === '') return dflt;
  const n = Number(v);
  return Number.isInteger(n) && n >= 0 ? n : dflt;
}

function clampInt(v, dflt, lo, hi) {
  const n = Number(v);
  if (!Number.isInteger(n)) return dflt;
  return Math.min(hi, Math.max(lo, n));
}

// 比较摘要而非原串:定长比较,不因长度不同提前返回(timingSafeEqual 要求等长)。
function keyMatches(expected, given) {
  if (!expected || typeof given !== 'string') return false;
  const digest = (s) => crypto.createHash('sha256').update(s).digest();
  return crypto.timingSafeEqual(digest(expected), digest(given));
}

function first(v) {
  return Array.isArray(v) ? v[0] : v;
}

function filterRequestHeaders(inHeaders) {
  const out = {};
  for (const [name, value] of Object.entries(inHeaders || {})) {
    if (DROP_REQUEST_HEADERS.has(name)) continue;
    if (DROP_REQUEST_PREFIXES.some((p) => name.startsWith(p))) continue;
    out[name] = value;
  }
  return out;
}

function filterResponseHeaders(inHeaders) {
  const out = {};
  for (const [name, value] of Object.entries(inHeaders || {})) {
    if (DROP_RESPONSE_HEADERS.has(name)) continue;
    out[name] = value; // set-cookie 在 Node 里本就是数组,writeHead 逐条写出
  }
  return out;
}

// 中继自身产生的错误:OpenAI 风格错误体(客户端能直接显示 message),外加
// X-Relay-Error 头 —— 与上游的 401/403/5xx 区分开,免得被当成账号或上游故障。
// details 是打票失败时的诊断(尝试次数、上次观测值等),合进 error 对象。
function sendError(res, status, code, message, details) {
  if (res.headersSent) {
    res.destroy(); // 响应已经开始:只能掐断,让客户端知道流不完整
    return;
  }
  const body = JSON.stringify({
    error: { message: `relay: ${message}`, type: 'relay_error', code, ...details },
  });
  res.writeHead(status, {
    'content-type': 'application/json',
    'content-length': Buffer.byteLength(body),
    'x-relay-error': code,
  });
  res.end(body);
}

// 只管「TCP 建连 + TLS 握手」。ClientRequest.setTimeout 在 socket 连上之前不计时,
// 建连卡住它管不到,所以自己布表。响应头与响应体阶段不设限:responses/compact
// 这类非流式调用要等模型算完才回头部,SSE 也允许长时间静默 —— 兜底是客户端
// 断开(联动拆上游)和 FC 函数超时。
function armConnectTimeout(upReq, ms) {
  upReq.once('socket', (socket) => {
    const timer = setTimeout(() => {
      const err = new Error(`connect timeout after ${ms}ms`);
      err.code = 'ETIMEDOUT';
      upReq.destroy(err);
    }, ms);
    const disarm = () => clearTimeout(timer);
    socket.once(socket.encrypted ? 'secureConnect' : 'connect', disarm);
    socket.once('close', disarm);
  });
}

// --- 打票(X-Relay-Mint)---------------------------------------------------
//
// 一发打票 = 一次 codex ping:裸打(不带 __cflb/__oailb)时边缘分配新节点并铸
// 新 pair;定向打(携带已验收的 pair)时请求钉在该节点上铸票,边缘不再发
// pair —— 实测见 FINDINGS.md。每个模型各打一张票,单票的验收条件:
//   票长     —— X-Codex-Turn-State 长度 == ticketLen(780 仅是预期格式长度)
//   节点     —— 票所属 pair 内嵌节点名 == 目标(__oailb JWT 解码后与值明文两边都查)
//   模型声明 —— 完整 response.created.response.model == 请求模型,禁止模糊搜索
// 打出的票与 pair 按凭据缓存:TTL 内复用,过期才重打。每轮每模型上限 maxAttempts
// 发,总预算/总时限内才允许冷却续打;401/403 整单停,400/404/422 只弃当前模型。

function jwtPayloadText(token) {
  const parts = String(token || '').split('.');
  if (parts.length < 2) return '';
  try {
    return Buffer.from(parts[1], 'base64url').toString('utf8');
  } catch {
    return '';
  }
}

// pair 的节点名:先查 __oailb 的 JWT 载荷(节点名在里面),再退到两个值的明文。
// 规范化成 unified-N;查不到返回 '' —— pair 仍能路由,只是没有可读名。
function mintGatewayLabel(cflb, oailb) {
  for (const src of [jwtPayloadText(oailb), oailb, cflb]) {
    const m = MINT_GATEWAY_RE.exec(src || '');
    if (!m) continue;
    const u = /unified[-_.]?(\d+)/i.exec(m[0]);
    return u ? `unified-${u[1]}` : m[0].toLowerCase();
  }
  return '';
}

// X-Relay-Mint 的值只在明确写着网关样内容(unified-N / gateway-x)时才当目标;
// 空值、1、true 这类布尔写法不算 —— 否则会把它误读成 unified-1。
function mintGatewayHint(v) {
  const s = String(v || '').trim();
  return /unified[-_.]?\d+|gateway[-_.][a-z0-9-]+/i.test(s) ? s : undefined;
}

// 目标网关写法规范化:'any'/'*'/'' → null(不查);'88'、'unified_88' → 'unified-88'。
function mintGatewayTarget(v) {
  const s = String(v || '').trim().toLowerCase();
  if (!s || s === 'any' || s === '*') return null;
  const u = /^unified[-_.]?(\d+)$/.exec(s);
  if (u) return `unified-${u[1]}`;
  if (/^\d+$/.test(s)) return `unified-${s}`;
  return s;
}

// __oailb JWT 自带的 exp 是网关真正执行的死线(实测签名 3900s);Max-Age/Expires
// 只是声明。读声明不验签 —— 跟 Go 侧 jwtExpiresAt 一个口径。
function mintExpiry(oailb, setCookie) {
  try {
    const claims = JSON.parse(jwtPayloadText(oailb) || '{}');
    if (Number.isFinite(claims.exp) && claims.exp > 0) {
      return new Date(claims.exp * 1000).toISOString();
    }
  } catch { /* 非 JSON 载荷,落属性兜底 */ }
  for (const line of setCookie || []) {
    if (!/^__oailb=/i.test(line)) continue;
    const ma = /(?:^|;)\s*max-age=(\d+)/i.exec(line);
    if (ma) return new Date(Date.now() + Number(ma[1]) * 1000).toISOString();
    const ex = /(?:^|;)\s*expires=([^;]+)/i.exec(line);
    if (ex) {
      const t = Date.parse(ex[1]);
      if (!Number.isNaN(t)) return new Date(t).toISOString();
    }
  }
  return undefined;
}

function mintPairs(setCookie) {
  const pairs = {};
  for (const line of setCookie || []) {
    const m = /^(__cflb|__oailb)=([^;]*)/i.exec(line);
    if (m) pairs[m[1].toLowerCase()] = m[2];
  }
  return pairs.__cflb && pairs.__oailb ? pairs : null;
}

// 显式输入的 pair 是路由种子，不是账号身份；只准两个 LB Cookie，不转发会话 Cookie。
function mintSeedPair(cookie, target) {
  const pairs = {};
  for (const part of String(cookie || '').split(';')) {
    const match = /^\s*(__cflb|__oailb)=(.*)$/.exec(part);
    if (!match) continue;
    const [, name, value] = match;
    if (Object.hasOwn(pairs, name) || !value || value.length > 4096 || /[^\x21-\x7e]|[,;]/.test(value)) {
      throw new Error('invalid route cookie pair');
    }
    pairs[name] = value;
  }
  if (!Object.keys(pairs).length) return null;
  if (!pairs.__cflb || !pairs.__oailb) throw new Error('incomplete route cookie pair');
  const gateway = mintGatewayLabel(pairs.__cflb, pairs.__oailb);
  let claims;
  try { claims = JSON.parse(jwtPayloadText(pairs.__oailb)); } catch { /* fail closed */ }
  const expiresAt = claims?.exp * 1000;
  if (!gateway || (target && target !== gateway) || !Number.isSafeInteger(claims?.exp) || expiresAt <= Date.now()) {
    throw new Error('expired, unknown or off-target route cookie pair');
  }
  return { pairs, gateway, expiresAt };
}

// SSE/WS 共用同一个严格判据:完整 JSON + created 类型 + 响应 ID + 模型字符串。
// 这是上游的模型声明,不把其他事件或其他对象里的 model 当作执行模型证明。
function createdModelFromJson(text, eventName = '') {
  try {
    const event = JSON.parse(text);
    if (event?.type !== 'response.created' || (eventName && eventName !== event.type)) return undefined;
    const response = event.response;
    if (typeof response?.id !== 'string' || !response.id.trim()) return undefined;
    if (typeof response.model !== 'string' || !response.model.trim()) return undefined;
    return response.model;
  } catch { return undefined; }
}

// 扫描缓冲最多 16 KiB,只分发空行结束的完整 SSE 事件;网络分包由调用方累积。
function readMintSse(buf, readEvent) {
  const lines = buf.toString('utf8').replace(/^\uFEFF/, '').split(/\r\n|\r|\n/);
  lines.pop(); // 未终止的一行不能参与事件判定
  let eventName = '';
  let data = [];
  for (const line of lines) {
    if (line === '') {
      const result = readEvent(data.join('\n'), eventName);
      if (result !== undefined) return result;
      eventName = '';
      data = [];
      continue;
    }
    if (line.startsWith(':')) continue;
    const colon = line.indexOf(':');
    const field = colon < 0 ? line : line.slice(0, colon);
    const value = colon < 0 ? '' : line.slice(colon + 1).replace(/^ /, '');
    if (field === 'event') eventName = value;
    if (field === 'data') data.push(value);
  }
  return undefined;
}

function createdModelFromSse(buf) {
  return readMintSse(buf, createdModelFromJson);
}

// HTTP 200 仍可能携带 error/response.failed。只保留受控错误类别，绝不记录正文。
function mintEventError(text, eventName = '') {
  let event;
  try { event = JSON.parse(text); } catch { return undefined; }
  if (!['error', 'response.failed'].includes(event?.type)
      || (eventName && eventName !== event.type)) return undefined;
  const error = event.error || event.response?.error || event;
  const knownCodes = new Set(['invalid_request_error', 'invalid_request', 'invalid_argument',
    'model_not_found', 'unsupported_model', 'invalid_model', 'authentication_error',
    'invalid_api_key', 'permission_denied', 'insufficient_quota', 'rate_limit_exceeded',
    'rate_limit_error', 'server_error', 'internal_error', 'overloaded_error']);
  const code = knownCodes.has(error.code) ? error.code
    : (knownCodes.has(error.type) ? error.type : 'unknown');
  const authStatus = ['authentication_error', 'invalid_api_key'].includes(code) ? 401
    : (code === 'permission_denied' ? 403 : undefined);
  const status = authStatus ?? event.status ?? error.status;
  return { code, status: Number.isInteger(status) && status >= 400 && status <= 599 ? status : undefined,
    terminal: ['invalid_request_error', 'invalid_request', 'invalid_argument', 'model_not_found',
      'unsupported_model', 'invalid_model', 'authentication_error', 'invalid_api_key',
      'permission_denied', 'insufficient_quota'].includes(code) };
}

function mintSseDecision(buf) {
  return readMintSse(buf, (text, eventName) => {
    const error = mintEventError(text, eventName);
    if (error) return { error };
    const model = createdModelFromJson(text, eventName);
    return model === undefined ? undefined : { model };
  });
}

function parseModels(v) {
  const list = String(v || '').split(',').map((s) => s.trim()).filter(Boolean);
  return [...new Set(list)];
}

// 票是 Fernet(0x80 + 8B 大端秒 = 签发时刻 + IV + 密文 + HMAC)—— 签发时刻
// 不验签就能读,与 Go 侧 fernetIssuedAt 同口径;读不出来退到采集时刻。
function fernetIssuedAt(ticket, fallbackMs) {
  try {
    const raw = Buffer.from(String(ticket), 'base64url');
    if (raw.length >= 9 && raw[0] === 0x80) {
      const sec = Number(raw.readBigUInt64BE(1));
      if (sec > 0) return sec * 1000;
    }
  } catch { /* 非 Fernet,落兜底 */ }
  return fallbackMs;
}

// 打出的票与 pair 按凭据缓存:TTL 内直接复用,过期才重打 —— 打票烧的是真配额。
// key 只存凭据摘要,内存不落 token。票与 pair 各自记期:
//   票   —— Fernet 内嵌签发时刻 + ticketTtlS(实测窗口 ~240s)
//   pair —— __oailb JWT 自己的死线(实测 3900s),由 mintExpiry 读出
// off-target 的 pair 永不入缓存:留它只会把之后的调用钉到错误节点上。
const mintCache = new Map();

function mintCacheKey(creds, kind, ...rest) {
  const h = crypto.createHash('sha256')
    .update(`${creds.authorization}|${creds.accountId || ''}`)
    .digest('hex').slice(0, 16);
  return [kind, h, ...rest].join('|');
}

function mintCacheGet(key) {
  const e = mintCache.get(key);
  if (!e) return null;
  if (e.expiresAt <= Date.now()) {
    mintCache.delete(key);
    return null;
  }
  return e;
}

// 每次调用重新验收缓存票:更严格的票长/TTL 不能被旧缓存绕过。
// 只收紧本次返回的死线,既不延长原始死线,也不修改其他调用共享的缓存记录。
function mintCachedTicket(key, want) {
  if (want.ticketTtlS === 0) return null;
  const hit = mintCacheGet(key);
  if (!hit || (want.ticketLen > 0 && hit.len !== want.ticketLen)) return null;
  const expiresAt = Math.min(hit.expiresAt, hit.issuedAt + want.ticketTtlS * 1000);
  return expiresAt > Date.now() ? { ...hit, expiresAt } : null;
}

function mintCacheSet(key, e) {
  if (mintCache.size >= MINT_CACHE_MAX) {
    mintCache.delete(mintCache.keys().next().value); // Map 保插入序:逐最旧
  }
  mintCache.set(key, e);
}

// 打票请求的头部:客户端凭据照传,其余按真实 codex-tui 的形态补齐 —— 这发是
// 代客户端铸票,不是透传,头部不继承调用方的杂项。session-id 每发新铸。
// cookieHeader 为空即裸打(边缘才会分配新节点);带上已验收的 pair 即定向打
// (请求被钉在该 pair 的节点上铸票,边缘不再发新 pair)。
function mintHeaders(creds, sid, cookieHeader) {
  const h = {
    authorization: creds.authorization,
    'content-type': 'application/json',
    accept: 'text/event-stream',
    'accept-encoding': 'identity',
    originator: 'codex-tui',
    'session-id': sid,
    'user-agent': MINT_UA,
  };
  if (creds.accountId) h['chatgpt-account-id'] = creds.accountId;
  if (cookieHeader) h.cookie = cookieHeader;
  return h;
}

function mintPayload(model) {
  return JSON.stringify({
    model,
    instructions: '',
    stream: true,
    store: false,
    input: [{ type: 'message', role: 'user', content: [{ type: 'input_text', text: 'ping' }] }],
    reasoning: { effort: 'low' },
    tool_choice: 'auto',
    parallel_tool_calls: false,
  });
}

// 一发 SSE 打票。只读响应头和有限事件数据:票/pair 在头上,模型声明取 created,
// 拿到判决就拆流 —— 把补全跑完只会白烧配额。返回 {req, done}:req 给调用方在
// 客户端断开时拆连接;done 永不 reject,一切结果(含传输错)都归结成一次
// attempt 记录交给循环判。
function fireSseMintAttempt(cfg, edgeIp, creds, model, cookieHeader) {
  const { upstream } = cfg;
  const isHttps = upstream.protocol === 'https:';
  let upReq = null;
  const done = new Promise((resolve) => {
    const out = { status: 0, len: 0, gateway: '', served: undefined, steered: !!cookieHeader, model };
    let finished = false;
    const finish = (extra) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      resolve({ ...out, ...extra });
    };

    upReq = (isHttps ? https : http).request({
      host: edgeIp || upstream.hostname,
      port: upstream.port || (isHttps ? 443 : 80),
      method: 'POST',
      path: MINT_PATH,
      headers: mintHeaders(creds, crypto.randomUUID(), cookieHeader),
      agent: false,
      servername: isHttps ? upstream.hostname : undefined,
    });
    armConnectTimeout(upReq, cfg.connectTimeoutMs);
    const timer = setTimeout(() => {
      upReq.destroy(Object.assign(new Error('mint attempt timeout'), { code: 'ETIMEDOUT' }));
    }, cfg.mint.attemptTimeoutMs);

    upReq.on('response', (upRes) => {
      readMintHeaders(out, upRes);
      // 上游已把 SSE 响应声明为 application/octet-stream(正文仍是 SSE 文本);
      // 模型声明由 mintSseDecision 严格解析,content-type 只做早退优化,放宽白名单。
      const mintCt = upRes.headers['content-type'] || '';
      if (upRes.statusCode === 200
          && !/^text\/event-stream(?:;|$)/i.test(mintCt)
          && !/^application\/octet-stream(?:;|$)/i.test(mintCt)) {
        finish({ reason: 'bad_sse_content_type' });
        upRes.destroy();
        return;
      }

      const cap = upRes.statusCode === 200 ? MINT_BODY_SCAN_BYTES : MINT_SNIPPET_BYTES;
      let buf = Buffer.alloc(0);
      upRes.on('data', (chunk) => {
        if (finished) return;
        if (buf.length < cap) {
          buf = Buffer.concat([buf, chunk.subarray(0, cap - buf.length)]);
          if (upRes.statusCode === 200) {
            const decision = mintSseDecision(buf);
            if (decision?.error) {
              const error = decision.error;
              if (error.status) out.status = error.status;
              finish({ reason: `sse_error:${error.code}`, terminalError: error.terminal });
              upRes.destroy();
              return;
            }
            out.served = decision?.model ?? out.served;
          }
        }
        // 200 拿到判决或扫满窗口即拆;非 200 攒够诊断体同样拆。先 finish 再
        // destroy:拆流引发的 error 晚一步到,被 finished 闸门挡在外面。
        if (out.served !== undefined || buf.length >= cap) {
          finish({ reason: 'ok' });
          upRes.destroy();
        }
      });
      upRes.on('end', () => {
        out.snippet = upRes.statusCode === 200 ? undefined : buf.toString('utf8', 0, MINT_SNIPPET_BYTES);
        finish({ reason: 'ok' });
      });
      upRes.on('error', (err) => {
        out.snippet = upRes.statusCode === 200 ? undefined : buf.toString('utf8', 0, MINT_SNIPPET_BYTES);
        finish({ reason: `stream:${err.code || 'err'}` });
      });
      upRes.on('aborted', () => finish({ reason: 'aborted' }));
    });

    upReq.on('error', (err) => finish({ reason: err.code === 'ETIMEDOUT' ? 'timeout' : `transport:${err.code || err.message}` }));
    upReq.end(mintPayload(model));
  });
  return { req: upReq, done };
}

// 打票传输适配:两种协议共用验收、缓存、冷却与取消流程。
function mintStatusOK(attempt) {
  return attempt.status === (attempt.transport === 'websocket' ? 101 : 200);
}

function fireMintAttempt(cfg, edgeIp, creds, model, cookieHeader) {
  return cfg.mint.transport === 'websocket'
    ? fireWsMintAttempt(cfg, edgeIp, creds, model, cookieHeader)
    : fireSseMintAttempt(cfg, edgeIp, creds, model, cookieHeader);
}

function readMintHeaders(out, response) {
  out.status = response.statusCode;
  out.edgeIp = response.socket?.remoteAddress;
  out.ticket = first(response.headers['x-codex-turn-state']) || '';
  out.len = out.ticket.length;
  out.pairs = mintPairs(response.headers['set-cookie']);
  out.routeCookieChanged = (response.headers['set-cookie'] || []).some((line) => /^__(?:cflb|oailb)=/i.test(line));
  if (out.pairs) {
    out.gateway = mintGatewayLabel(out.pairs.__cflb, out.pairs.__oailb);
    out.expiresAt = mintExpiry(out.pairs.__oailb, response.headers['set-cookie']);
  }
}

// 客户端帧必须掩码;打票只发送小型文本消息、pong 和 close,不协商压缩。
function mintWsClientFrame(opcode, payload) {
  const body = Buffer.isBuffer(payload) ? payload : Buffer.from(payload);
  if (body.length > 65535) throw new Error('outgoing websocket message too large');
  const extended = body.length >= 126;
  const header = Buffer.alloc(extended ? 4 : 2);
  header[0] = 0x80 | opcode;
  header[1] = 0x80 | (extended ? 126 : body.length);
  if (extended) header.writeUInt16BE(body.length, 2);
  const mask = crypto.randomBytes(4);
  const masked = Buffer.from(body);
  for (let i = 0; i < masked.length; i += 1) masked[i] ^= mask[i % 4];
  return Buffer.concat([header, mask, masked]);
}

// 严格解析服务端帧头,大长度在分配消息缓冲之前拒绝。半帧返回 null 等待续包。
function readMintWsFrame(buf) {
  if (buf.length < 2) return null;
  const fin = !!(buf[0] & 0x80);
  const opcode = buf[0] & 0x0f;
  if ((buf[0] & 0x70) || (buf[1] & 0x80)) throw new Error('unexpected websocket flags');
  if (![0, 1, 8, 9, 10].includes(opcode)) throw new Error('unsupported websocket opcode');
  let length = buf[1] & 0x7f;
  let offset = 2;
  if (opcode >= 8 && (!fin || length > 125)) throw new Error('invalid websocket control frame');
  if (length === 126) {
    if (buf.length < 4) return null;
    length = buf.readUInt16BE(2);
    if (length < 126) throw new Error('noncanonical websocket length');
    offset = 4;
  } else if (length === 127) {
    // 当前消息上限小于 65536,所有合法的 64 位长度帧必然超限。
    throw new Error('websocket frame too large');
  }
  if (length > MINT_BODY_SCAN_BYTES) throw new Error('websocket frame too large');
  if (buf.length < offset + length) return null;
  return { fin, opcode, payload: buf.subarray(offset, offset + length), consumed: offset + length };
}

// 合并文本分片,控制帧可以穿插。只有完整 UTF-8 JSON 消息进入模型判定。
function mintWsReader(socket, onMessage, onStop) {
  let pending = Buffer.alloc(0);
  let fragment = null;
  let wireBytes = 0;
  return (chunk) => {
    try {
      wireBytes += chunk.length;
      if (wireBytes > MINT_BODY_SCAN_BYTES * 4) throw new Error('websocket scan limit');
      pending = Buffer.concat([pending, chunk]);
      while (pending.length) {
        const frame = readMintWsFrame(pending);
        if (!frame) break;
        pending = pending.subarray(frame.consumed);
        if (frame.opcode === 8) { onStop('ws_closed'); return; }
        if (frame.opcode === 9) { socket.write(mintWsClientFrame(10, frame.payload)); continue; }
        if (frame.opcode === 10) continue;
        if ((frame.opcode === 0) !== (fragment !== null)) throw new Error('invalid websocket continuation');
        fragment = fragment === null ? frame.payload : Buffer.concat([fragment, frame.payload]);
        if (fragment.length > MINT_BODY_SCAN_BYTES) throw new Error('websocket message too large');
        if (!frame.fin) continue;
        const text = new TextDecoder('utf-8', { fatal: true }).decode(fragment);
        fragment = null;
        if (onMessage(text)) return;
      }
    } catch { onStop('ws_protocol'); }
  };
}

function fireWsMintAttempt(cfg, edgeIp, creds, model, cookieHeader) {
  let request;
  let socket;
  let timer;
  let finished = false;
  let finish;
  const out = { transport: 'websocket', status: 0, len: 0, gateway: '', steered: !!cookieHeader, model };
  const done = new Promise((resolve) => {
    finish = (extra) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      resolve({ ...out, ...extra });
      socket?.destroy();
      request?.destroy();
    };
    const key = crypto.randomBytes(16).toString('base64');
    const hostname = cfg.upstream.hostname.replace(/^\[|\]$/g, '');
    const secure = cfg.upstream.protocol === 'https:';
    const headers = { ...mintHeaders(creds, crypto.randomUUID(), cookieHeader),
      host: cfg.upstream.host, connection: 'Upgrade', upgrade: 'websocket',
      'sec-websocket-key': key, 'sec-websocket-version': '13',
      'openai-beta': 'responses_websockets=2026-02-06',
    };
    delete headers.accept;
    delete headers['content-type'];
    request = (secure ? https : http).request({
      host: edgeIp || hostname, port: cfg.upstream.port || (secure ? 443 : 80),
      method: 'GET', path: MINT_PATH, headers, agent: false,
      servername: secure && !net.isIP(hostname) ? hostname : undefined,
      checkServerIdentity: (_host, cert) => tls.checkServerIdentity(hostname, cert),
    });
    armConnectTimeout(request, cfg.connectTimeoutMs);
    timer = setTimeout(() => finish({ reason: 'timeout' }), cfg.mint.attemptTimeoutMs);
    request.on('error', (err) => finish({ reason: `transport:${err.code || 'err'}` }));
    request.on('response', (res) => readWsMintRejection(res, out, finish));
    request.on('upgrade', (res, connection, head) => {
      socket = connection;
      if (finished) { socket.destroy(); return; }
      attachMintWebSocket({ res, socket, head, key, out, model, finish });
    });
    request.end();
  });
  // 与 HTTP ClientRequest 的取消接口一致,关闭升级后的 socket,不只关闭握手请求。
  return { req: { destroy: () => finish({ reason: 'aborted' }) }, done };
}

function attachMintWebSocket({ res, socket, head, key, out, model, finish }) {
  socket.on('error', () => finish({ reason: 'ws_transport' }));
  socket.on('close', () => finish({ reason: 'ws_closed' }));
  const accept = crypto.createHash('sha1').update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest('base64');
  readMintHeaders(out, res);
  if (res.statusCode !== 101 || res.headers['sec-websocket-accept'] !== accept
      || res.headers.upgrade?.toLowerCase() !== 'websocket'
      || !res.headers.connection?.toLowerCase().split(',').some((token) => token.trim() === 'upgrade')
      || res.headers['sec-websocket-extensions'] || res.headers['sec-websocket-protocol']) {
    finish({ reason: 'ws_bad_handshake' });
    return;
  }
  const receive = mintWsReader(socket, (text) => wsMintMessage(text, out, finish),
    (reason) => finish({ reason }));
  socket.on('data', receive);
  const payload = JSON.parse(mintPayload(model));
  delete payload.stream;
  socket.write(mintWsClientFrame(1, JSON.stringify({ ...payload, type: 'response.create' })));
  if (head.length) receive(head);
}

function wsMintMessage(text, out, finish) {
  // 上游已把响应头搬进 codex.response.metadata 消息:票(x-codex-turn-state)
  // 不再出现在 101 握手头上,在这里取。其他头忽略,继续等 response.created。
  if (text.includes('"codex.response.metadata"')) {
    try {
      const meta = JSON.parse(text);
      const ticket = meta?.headers?.['x-codex-turn-state'];
      if (typeof ticket === 'string' && ticket) { out.ticket = ticket; out.len = ticket.length; }
    } catch { /* 非完整 JSON,按普通消息继续 */ }
    return false;
  }
  const error = mintEventError(text);
  if (error) {
    if (error.status) out.status = error.status;
    finish({ reason: error.code === 'unknown' ? 'ws_error_event' : `ws_error:${error.code}`,
      terminalError: error.terminal });
    return true;
  }
  const model = createdModelFromJson(text);
  if (model !== undefined) {
    out.served = model;
    finish({ reason: 'ok' });
    return true;
  }
  return false;
}

function readWsMintRejection(res, out, finish) {
  out.status = res.statusCode;
  let snippet = Buffer.alloc(0);
  res.on('data', (chunk) => {
    snippet = Buffer.concat([snippet, chunk.subarray(0, MINT_SNIPPET_BYTES - snippet.length)]);
    if (snippet.length >= MINT_SNIPPET_BYTES) {
      finish({ reason: 'ws_upgrade_rejected', snippet: snippet.toString() });
      res.destroy();
    }
  });
  res.on('end', () => finish({ reason: 'ws_upgrade_rejected', snippet: snippet.toString() }));
  res.on('error', () => finish({ reason: 'ws_transport' }));
  res.on('aborted', () => finish({ reason: 'aborted' }));
}

// 票归属节点的判定:本发若新铸/轮换了 pair,票就在那个新节点上;定向打且没
// 重铸时,票在当前 pair 的节点上;裸打又没发 cookie 则节点不可知('' → 有目标
// 时必拒收)。
function mintAttemptAccepted(attempt, want, pair) {
  if (!mintStatusOK(attempt)) return `http:${attempt.status || 'err'}`;
  if (!attempt.ticket) return 'no_ticket';
  if (want.ticketLen > 0 && attempt.len !== want.ticketLen) return `len:${attempt.len}`;
  const node = attempt.pairs ? attempt.gateway : (attempt.steered && pair ? pair.gateway : '');
  if (want.gateway && node !== want.gateway) return `gateway:${node || '?'}`;
  if (attempt.served === undefined) return 'no_model_field';
  if (attempt.served !== attempt.model) return `served:${attempt.served}`;
  return null; // 全部满足
}

// 等待期间不发上游请求;客户端断开立即取消定时器并解除事件监听。
function waitMintCooldown(signal, milliseconds) {
  if (signal.aborted) return Promise.resolve(false);
  return new Promise((resolve) => {
    const finish = (ready) => {
      clearTimeout(timer);
      signal.removeEventListener('abort', onClose);
      resolve(ready);
    };
    const onClose = () => finish(false);
    const timer = setTimeout(() => finish(true), milliseconds);
    signal.addEventListener('abort', onClose, { once: true });
  });
}

// 服务端独立截止时间不依赖 HTTP 触发器传播 close；总预算覆盖模型、补 pair 和冷却轮次。
function mintLifetime(res, limits) {
  const controller = new AbortController();
  let inFlight;
  let attempts = 0;
  const stop = (reason) => {
    if (controller.signal.aborted) return;
    controller.abort(reason);
    inFlight?.destroy();
  };
  const onClose = () => stop('client_closed');
  res.once('close', onClose);
  const timer = setTimeout(() => stop('total_timeout'), limits.totalTimeoutMs);
  timer.unref();
  return {
    signal: controller.signal,
    get reason() { return controller.signal.aborted ? controller.signal.reason : ''; },
    take() {
      if (!this.available()) return false;
      attempts += 1;
      return true;
    },
    available() {
      if (attempts >= limits.maxTotalAttempts) stop('total_attempt_limit');
      return !controller.signal.aborted;
    },
    track(request) {
      inFlight = request;
      if (controller.signal.aborted) request.destroy();
    },
    clear() { inFlight = null; },
    close() {
      clearTimeout(timer);
      res.removeListener('close', onClose);
      inFlight?.destroy();
    },
  };
}

// 上游错误消息可能含凭据；日志只给受控原因和安全标签，不输出原始正文。
function mintTraceReason(reason) {
  if (!reason) return 'candidate_ok'; // 单发初筛通过不代表最终票/pair 验期成功。
  if (reason.startsWith('served:')) return 'model_mismatch';
  return /^(?:gateway:unified-\d+|gateway:\?|http:\d+|http:err|len:\d+|[a-z_]+(?::[A-Z0-9_]+|:[a-z_]+)?)$/.test(reason)
    ? reason : 'unclassified_failure';
}

// 打票日志只输出 SHA-256 短指纹,绝不截取原始票或 Cookie。
function mintFingerprint(value) {
  return value ? crypto.createHash('sha256').update(value).digest('base64url').slice(0, 8) : '';
}

function mintAttemptTrace(sent, attempt, diagnostic = {}) {
  const issued = fernetIssuedAt(attempt.ticket, NaN);
  const age = Number.isFinite(issued) ? Math.max(0, Math.floor((Date.now() - issued) / 1000)) : null;
  // 定向打且上游没发新 pair 时,票铸在发送 pair 钉住的节点上 —— 与
  // mintAttemptAccepted 的 node 判定同口径,不能记成"网关未知"。
  const gateway = attempt.pairs ? attempt.gateway : (sent.gateway || '');
  const served = /^[a-z0-9][a-z0-9._-]{0,95}$/i.test(attempt.served || '') ? attempt.served : '';
  const change = sent.gateway && gateway ? (sent.gateway === gateway ? '网关未变' : '网关变化') : '网关变化未知';
  const trace = {
    sent_gateway: sent.gateway || '', sent_cookie_fingerprint: mintFingerprint(sent.cookie || ''),
    received_gateway: gateway, ticket_length: attempt.len || 0,
    ticket_fingerprint: mintFingerprint(attempt.ticket || ''), ticket_age_s: age,
    served_model: served, status: attempt.status,
  };
  const input = sent.cookie ? `定向 Cookie · #${trace.sent_cookie_fingerprint} · ${sent.gateway || '网关未知'}` : '裸打';
  const output = `得到 ${gateway || '网关未知'} · 票长 ${trace.ticket_length} · #${trace.ticket_fingerprint || '未知'}`;
  const model = served ? `模型 ${served}` : '未见模型';
  const line = `${input} · ${output} · ${age === null ? '票龄未知' : `这张票龄 ${age}s`} · ${model} · ${change}`;
  // 诊断字段只加入 FC 日志，原有 attempt_log 响应结构保持兼容。
  console.log(JSON.stringify({ fn: 'chatgpt-relay', event: 'mint_attempt', ...trace, ...diagnostic, line }));
  return trace;
}

// 打票模式入口。参数:请求头 > 环境变量 > 默认。X-Relay-Mint 的值顺手当目标网关
// 用(X-Relay-Mint: unified-88),不是网关样的值就退回 X-Mint-Gateway/环境变量。
//
// 流程:每个模型一张票。缓存里有活票直接复用(TTL 判定,过期才算缺失);缺的
// 进打票循环 —— 有 on-target 活 pair 就携带定向打(票铸在该节点上),没有就裸
// 打(顺带铸 pair)。上游一旦发新 pair 即采纳:on-target 入缓存继续定向,
// off-target 不入缓存、下一发自然回裸打。票与 pair 不绑定(实测),所以票只跟
// 自己的 TTL 走,pair 死了单独重打。
async function mintTickets(req, res, cfg, entry, edgeIp) {
  req.resume(); // 调用方可能带了体 —— 打票不用它,排空别占着 socket
  entry.mint = {}; // 日志行先打上标记,再填 want/attempts

  const authorization = first(req.headers['authorization']);
  if (!authorization) {
    sendError(res, 400, 'mint_no_auth', 'X-Relay-Mint mints with the request\'s Authorization + Chatgpt-Account-Id; both absent');
    return;
  }
  const creds = { authorization, accountId: first(req.headers['chatgpt-account-id']) };

  const transport = first(req.headers['x-mint-transport']) || cfg.mint.transport;
  if (!['sse', 'websocket'].includes(transport)) {
    sendError(res, 400, 'mint_bad_params', 'X-Mint-Transport must be sse or websocket');
    return;
  }
  cfg = { ...cfg, mint: { ...cfg.mint, transport } };
  const want = {
    gateway: mintGatewayTarget(
      first(req.headers['x-mint-gateway']) || mintGatewayHint(first(req.headers['x-relay-mint'])) || cfg.mint.gateway,
    ),
    models: parseModels(first(req.headers['x-mint-models']) || first(req.headers['x-mint-model']) || cfg.mint.models.join(',')),
    ticketLen: nonNegInt(first(req.headers['x-mint-len']), cfg.mint.ticketLen),
    ticketTtlS: nonNegInt(first(req.headers['x-mint-ttl']), cfg.mint.ticketTtlS),
    maxAttempts: clampInt(first(req.headers['x-mint-attempts']), cfg.mint.maxAttempts, 1, 128),
  };
  if (!want.models.length) {
    sendError(res, 400, 'mint_bad_params', 'mint model list must not be empty');
    return;
  }
  let seed;
  try { seed = mintSeedPair(first(req.headers.cookie), want.gateway); }
  catch {
    sendError(res, 400, 'mint_bad_cookie', 'route Cookie must contain one complete, unexpired pair for the requested gateway');
    return;
  }
  Object.assign(entry.mint, {
    transport, models: want.models, want_gateway: want.gateway || 'any', want_len: want.ticketLen,
  });

  const lifetime = mintLifetime(res, cfg.mint);
  try {
    await mintWithLifetime({ res, cfg, entry, edgeIp, want, creds, transport, lifetime, seed });
  } finally {
    if (lifetime.reason) entry.mint.stop_reason = lifetime.reason;
    lifetime.close();
  }
}

async function mintWithLifetime({ res, cfg, entry, edgeIp, want, creds, transport, lifetime, seed }) {
  // 握手取得的票可能具有协议相关语义,不同传输的票与 pair 保守隔离。
  const seedKey = seed ? crypto.createHash('sha256').update(JSON.stringify(seed.pairs)).digest('hex') : '';
  const gwKey = `${transport}|${want.gateway || 'any'}${seed ? `|seed:${seedKey}` : ''}`;
  const pairKey = mintCacheKey(creds, 'pair', gwKey);
  const tickets = {};   // model → {ticket, len, served, issuedAt, expiresAt, edgeIp}
  const cachedHit = new Set();
  let missing = [];
  const errors = {};    // model → {attempts, last:{status,len,gateway,served,reason,why}}
  const seen = { gateways: new Set() };
  let pair = (want.ticketTtlS > 0 ? mintCacheGet(pairKey) : null) || seed;
  let attempts = 0;
  let last = null;
  const attemptLog = [];

  for (const m of want.models) {
    const hit = mintCachedTicket(mintCacheKey(creds, 'ticket', gwKey, m), want);
    if (hit) {
      tickets[m] = hit;
      cachedHit.add(m);
    } else {
      missing.push(m);
    }
  }

  // 定向条件:有 pair、没过期、且在目标节点上。off-target 的 pair 携带只会把
  // 请求钉到错误节点 —— 不如裸打让边缘重新分。
  const pairLive = () => pair && pair.expiresAt > Date.now()
    && (!want.gateway || pair.gateway === want.gateway);
  const cookieHeader = () => `__cflb=${pair.pairs.__cflb}; __oailb=${pair.pairs.__oailb}`;

  // 补票与补 pair 共用一次请求的记账、取消及凭据级拒绝处理。
  const runAttempt = async (model) => {
    // 显式要求带 Cookie 的调用，失去有效目标 pair 后不能静默切回裸打。
    if (seed && !pairLive()) {
      if (!last) last = { model, why: 'seed_pair_expired' };
      return null;
    }
    if (!lifetime.take()) return null;
    attempts += 1;
    entry.mint.attempts = attempts;
    const sent = pairLive() ? { gateway: pair.gateway, cookie: cookieHeader() } : {};
    const shot = fireMintAttempt(cfg, edgeIp, creds, model, sent.cookie || null);
    lifetime.track(shot.req);
    const attempt = await shot.done;
    lifetime.clear();
    if (lifetime.reason === 'client_closed') return null;
    const why = lifetime.reason || (attempt.reason !== 'ok' ? attempt.reason : mintAttemptAccepted(attempt, want, pair));
    attemptLog.push(mintAttemptTrace(sent, attempt, {
      request_id: entry.reqId, wanted_gateway: want.gateway || 'any',
      requested_model: /^[a-z0-9][a-z0-9._-]{0,95}$/i.test(model) ? model : '',
      reject_reason: mintTraceReason(why),
    }));
    if (attemptLog.length > 40) attemptLog.shift();
    last = {
      model, status: attempt.status, len: attempt.len,
      gateway: attempt.gateway || undefined, served: attempt.served, reason: attempt.reason, why: why || undefined,
    };
    if (attempt.gateway) seen.gateways.add(attempt.gateway);
    if (lifetime.reason) return null;
    if (attempt.status === 401 || attempt.status === 403) {
      entry.mint.attempts = attempts;
      sendError(res, 502, 'mint_rejected', `upstream rejected the mint request (status ${attempt.status})`, {
        attempts, model, upstream_status: attempt.status, upstream_body: attempt.snippet, attempt_log: attemptLog,
      });
      return null;
    }
    // 只接受完整成功响应中的 pair;过期/非目标轮换同时清掉旧缓存,下发改回裸打。
    if (mintStatusOK(attempt) && attempt.reason === 'ok' && attempt.pairs) {
      const expiresAt = attempt.expiresAt ? Date.parse(attempt.expiresAt) : Date.now() + 3600_000;
      pair = { pairs: attempt.pairs, gateway: attempt.gateway, expiresAt, edgeIp: attempt.edgeIp };
      if (!pairLive()) mintCache.delete(pairKey);
      else if (want.ticketTtlS > 0) mintCacheSet(pairKey, pair);
    } else if (seed && attempt.routeCookieChanged) {
      pair = null;
    }
    if (seed && !pairLive()) {
      last.why = why || 'seed_route_lost';
      return null;
    }
    return attempt;
  };

  const terminalModels = new Set();
  // 一轮单模型打票:false 表示已成功或永久失败,true 表示预算耗尽,null 表示整单停止。
  const mintModelRound = async (model) => {
    delete errors[model];
    for (let i = 1; i <= want.maxAttempts && !lifetime.reason; i += 1) {
      const before = attempts;
      const attempt = await runAttempt(model);
      if (!attempt) {
        errors[model] = { attempts: i - 1 + (attempts - before),
	      last: { ...last, why: lifetime.reason || last?.why || 'rejected' } };
        return null;
      }
      if (MINT_FATAL_STATUS.has(attempt.status) || attempt.terminalError) {
        terminalModels.add(model);
        errors[model] = { attempts: i, last: { ...last, why: attempt.terminalError ? attempt.reason : `http:${attempt.status}` } };
        return false;
      }
      const why = attempt.reason !== 'ok' ? attempt.reason : mintAttemptAccepted(attempt, want, pair);
      if (why) {
        last.why = why;
        continue;
      }
      const issuedAt = fernetIssuedAt(attempt.ticket, Date.now());
      const rec = {
        ticket: attempt.ticket, len: attempt.len, served: attempt.served,
        issuedAt, expiresAt: issuedAt + want.ticketTtlS * 1000, edgeIp: attempt.edgeIp,
      };
      if (want.ticketTtlS > 0 && rec.expiresAt <= Date.now()) {
        last.why = 'expired_ticket';
        continue;
      }
      if (want.ticketTtlS > 0) mintCacheSet(mintCacheKey(creds, 'ticket', gwKey, model), rec);
      tickets[model] = rec;
      return false;
    }
    errors[model] = { attempts: want.maxAttempts, last };
    return true;
  };

  // 每轮失败预算耗尽后冷却再继续;永久拒绝不重试,总次数跨轮累计。
  rounds: while (!lifetime.reason) {
    let retry = false;
    for (const model of missing) {
      const exhausted = await mintModelRound(model);
      if (exhausted === null) break rounds;
      retry ||= exhausted;
    }
    if (lifetime.reason) break;

    // 票与 pair 独立续期:即使全票命中也要补失效 pair,已有票保持原样。
    // 每轮补 pair 共享 maxAttempts 次预算,不重复使用已被明确拒绝的模型。
    const pairModels = want.models.filter((model) => tickets[model] && !terminalModels.has(model));
    for (let i = 0; i < want.maxAttempts && pairModels.length && !pairLive() && !lifetime.reason; i += 1) {
      const attempt = await runAttempt(pairModels[0]);
      if (!attempt) break rounds;
      if (MINT_FATAL_STATUS.has(attempt.status) || attempt.terminalError) terminalModels.add(pairModels.shift());
      if (!pairLive()) last.why = 'no_live_pair';
    }
    if (lifetime.reason) break;

    if (!pairLive() && pairModels.length) retry = true;
    if (!retry) break;
    if (!lifetime.available()) break;

    entry.mint.cooldowns = (entry.mint.cooldowns || 0) + 1;
    console.log(JSON.stringify({ fn: 'chatgpt-relay', event: 'mint_cooldown', request_id: entry.reqId,
      attempts, wait_ms: cfg.mint.retryCooldownMs }));
    if (!await waitMintCooldown(lifetime.signal, cfg.mint.retryCooldownMs)) break;
    // 冷却中票/pair 可能过期:保留仍有效的票,只重打缺失或失效且可重试的模型。
    for (const model of want.models) {
      if (want.ticketTtlS > 0 && tickets[model]?.expiresAt <= Date.now()) {
        delete tickets[model];
        cachedHit.delete(model);
      }
    }
    missing = want.models.filter((model) => !tickets[model] && !terminalModels.has(model));
  }
  if (lifetime.reason === 'client_closed' || res.writableEnded) return;

  // 未轮到的模型也要说明未完成原因，不能在部分成功响应中静默消失。
  if (lifetime.reason) {
    for (const model of want.models) {
      if (!tickets[model] && !errors[model]) {
        errors[model] = { attempts: 0, last: { model, why: lifetime.reason } };
      }
    }
  }

  const now = Date.now();
  const goodPair = pairLive();
  const ticketsOut = {};
  for (const m of want.models) {
    const rec = tickets[m];
    if (!rec) continue;
    // 其他模型补票/补 pair 可能耗时很久,序列化前再验期,不能把过期票报成功。
    if (want.ticketTtlS > 0 && rec.expiresAt <= now) {
      errors[m] = { last: { why: 'expired_ticket' } };
      cachedHit.delete(m);
      continue;
    }
    ticketsOut[m] = {
      turn_state: rec.ticket,
      ticket_len: rec.len,
      served_model: rec.served,
      issued_at: new Date(rec.issuedAt).toISOString(),
      expires_at: new Date(rec.expiresAt).toISOString(),
      age_s: Math.max(0, Math.round((now - rec.issuedAt) / 1000)),
      cached: cachedHit.has(m),
    };
  }
  const okCount = Object.keys(ticketsOut).length;
  entry.mint.attempts = attempts;
  entry.mint.cached = cachedHit.size;
  if (pair && pair.gateway) entry.mint.gateway = pair.gateway;

  if (okCount === 0 || !goodPair) {
    entry.mint.last = last;
    const message = !goodPair ? 'no live target pair' : 'no acceptable ticket';
    const stopped = lifetime.reason ? ` (${lifetime.reason})` : '';
    sendError(res, 502, 'mint_exhausted', `${message} after ${attempts} attempts${stopped}`, {
      attempts,
      want: { models: want.models, ticket_len: want.ticketLen, gateway: want.gateway || 'any' },
      last,
      gateways_seen: [...seen.gateways],
      attempt_log: attemptLog,
      errors: Object.keys(errors).length ? errors : undefined,
    });
    return;
  }

  const body = JSON.stringify({
    transport,
    gateway: goodPair ? pair.gateway : want.gateway || undefined,
    cookies: goodPair ? pair.pairs : undefined,
    cookie_header: goodPair ? `__cflb=${pair.pairs.__cflb}; __oailb=${pair.pairs.__oailb}` : undefined,
    expires_at: goodPair ? new Date(pair.expiresAt).toISOString() : undefined,
    edge_ip: (goodPair && pair.edgeIp) || undefined,
    attempts,
    attempt_log: attemptLog,
    tickets: ticketsOut,
    errors: Object.keys(errors).length ? errors : undefined,
    // 单模型调用保留平铺字段,方便调用方不翻 tickets 表。
    ...(want.models.length === 1 && ticketsOut[want.models[0]] ? {
      model: want.models[0],
      turn_state: ticketsOut[want.models[0]].turn_state,
      ticket_len: ticketsOut[want.models[0]].ticket_len,
      served_model: ticketsOut[want.models[0]].served_model,
    } : {}),
  });
  res.writeHead(200, {
    'content-type': 'application/json',
    'content-length': Buffer.byteLength(body),
    'cache-control': 'no-store',
  });
  res.end(body);
}

function relay(req, res) {
  const started = Date.now();
  const entry = {
    fn: 'chatgpt-relay',
    reqId: first(req.headers['x-fc-request-id']),
    method: req.method,
    path: req.url.split('?')[0], // 不记 query
  };
  let bytes = 0;
  res.on('close', () => {
    if (res.headersSent) entry.status = res.statusCode;
    entry.bytes = bytes;
    entry.ms = Date.now() - started;
    if (!res.writableFinished) entry.aborted = true;
    console.log(JSON.stringify(entry));
  });

  let cfg;
  try {
    cfg = config();
  } catch (err) {
    entry.error = err.message;
    sendError(res, 500, 'relay_misconfigured', err.message);
    return;
  }

  // 鉴权:RELAY_KEY 未配置 = 全拒(fail closed)。
  if (!keyMatches(cfg.relayKey, first(req.headers['x-relay-key']))) {
    sendError(res, 403, 'bad_relay_key', 'bad or missing X-Relay-Key');
    return;
  }

  const edgeIp = (first(req.headers['x-edge-ip']) || '').trim();
  if (edgeIp) {
    if (!net.isIP(edgeIp)) {
      sendError(res, 400, 'bad_edge_ip', 'X-Edge-IP must be an IP literal');
      return;
    }
    if (!cfg.allowPrivateEdge && !isPublicIP(edgeIp)) {
      sendError(res, 400, 'bad_edge_ip', 'X-Edge-IP must be a public IP');
      return;
    }
    entry.edge = edgeIp;
  }

  // 打票模式:控制头存在即触发,路径与方法都无意义,不进透传。
  // 透明模式完全关闭云端打票:即使客户端误带 X-Relay-Mint,也按普通请求
  // 继续透传；控制头会在 filterRequestHeaders 中被剥离，不会外发给上游。
  if (cfg.mode !== 'transparent' && 'x-relay-mint' in req.headers) {
    mintTickets(req, res, cfg, entry, edgeIp).catch((err) => {
      entry.error = String((err && err.stack) || err);
      sendError(res, 500, 'internal', 'internal error');
    });
    return;
  }

  // 只接受 origin-form(/path?query):absolute-form 会让上游按请求行里的主机
  // 路由,绕开我们固定的 Host。
  if (!req.url.startsWith('/')) {
    sendError(res, 400, 'bad_target', 'request target must be an origin-form path');
    return;
  }

  const { upstream } = cfg;
  const isHttps = upstream.protocol === 'https:';
  const headers = filterRequestHeaders(req.headers);
  headers.host = upstream.host; // 非默认端口时含端口

  // agent:false —— 每个请求一条新连接,响应结束即关:
  //  · 钉 IP 天然逐请求生效,不会被连接池复用到别的 IP 上;
  //  · FC 空闲时会冻结实例,池里的 keep-alive 连接解冻后可能已被对端关掉,
  //    复用它会让本可成功的请求 ECONNRESET;
  //  · 代价是每请求一次握手 —— FC 与 Cloudflare 边缘同地域,只有几毫秒。
  // 钉 IP 时拨号地址换成该 IP,servername 仍是上游域名:SNI 与证书校验不变。
  const upReq = (isHttps ? https : http).request({
    host: edgeIp || upstream.hostname,
    port: upstream.port || (isHttps ? 443 : 80),
    method: req.method,
    path: req.url,
    headers,
    agent: false,
    servername: isHttps ? upstream.hostname : undefined,
  });
  armConnectTimeout(upReq, cfg.connectTimeoutMs);

  upReq.on('response', (upRes) => {
    const out = filterResponseHeaders(upRes.headers);
    // 回报实际连上的边缘 IP:默认 DNS 时是解析结果,钉 IP 时就是所钉的 IP。
    const edgeAddr = upRes.socket && upRes.socket.remoteAddress;
    if (edgeAddr) out['x-relay-edge-ip'] = edgeAddr;
    // FC 网关自身按 chunked 判定流式;这是给链路上可能存在的 nginx 系反代的提示。
    if (/^text\/event-stream/i.test(String(upRes.headers['content-type'] || ''))) {
      out['x-accel-buffering'] = 'no';
    }
    res.writeHead(upRes.statusCode, out);
    upRes.on('data', (chunk) => { bytes += chunk.length; });
    // pipeline:背压 + 任一端出错两端一起拆。上游中途断开时客户端连接被直接
    // 掐断(而不是 end()),截断的流不会被当成完整响应。
    pipeline(upRes, res, (err) => {
      if (err && !entry.error) entry.error = err.code || err.message;
    });
  });

  upReq.on('error', (err) => {
    if (!entry.error) entry.error = err.code || err.message;
    // 响应已开始时交给 pipeline 收尾:上游断开会传导成客户端连接被掐断;
    // 而上游已完整回了响应(如提前回 401 后关连接)时不该再去掐它。
    if (res.headersSent) return;
    if (err.code === 'ETIMEDOUT') sendError(res, 504, 'upstream_timeout', err.message);
    else sendError(res, 502, 'upstream_unreachable', err.code || err.message);
  });

  // 客户端提前断开(含响应中途)→ 拆掉上游,不白占 FC 时长。
  res.on('close', () => {
    if (!res.writableFinished) upReq.destroy();
  });

  // 请求体流式转发。用 pipe 而不是 pipeline:上游出错时 pipeline 会连带销毁 req,
  // 进而掐断客户端连接,就回不了 502/504 了。
  req.pipe(upReq);
}

// 同步阶段的意外异常不能冒泡:一个实例同时承载多条流,进程崩了全断。
function handler(req, res) {
  try {
    relay(req, res);
  } catch (err) {
    console.log(JSON.stringify({ fn: 'chatgpt-relay', msg: 'handler error', error: String(err && err.stack || err) }));
    sendError(res, 500, 'internal', 'internal error');
  }
}

// --- WebSocket 隧道(FC HTTP 触发器支持 Upgrade)---------------------------
//
// FC 把 upgrade 请求交给运行时的 HTTP server,'upgrade' 事件给出客户端裸
// socket。实现是握手后字节级隧道:重建客户端的升级请求(滤掉控制/身份头、
// Host 改上游)写进上游连接,然后双向 splice —— 之后的 WS 帧、扩展协商、
// ping/pong 全部透明,本服务不解析任何帧。上游回 101 或非 101 都原样流到
// 客户端,客户端据此自己判成败。会话时长被函数 timeout 封顶(平台上限
// 86400s);空闲保活靠 WS 自带 ping/pong,经隧道透传无需处理。

// upgrade 握手没有 res 对象,拒绝只能把错误写在裸 socket 上再关掉。
function wsError(socket, status, code, message) {
  if (socket.destroyed) return;
  const body = JSON.stringify({ error: { message: `relay: ${message}`, type: 'relay_error', code } });
  socket.end(
    `HTTP/1.1 ${status} ${http.STATUS_CODES[status] || ''}\r\n`
    + 'content-type: application/json\r\n'
    + `x-relay-error: ${code}\r\n`
    + `content-length: ${Buffer.byteLength(body)}\r\n`
    + 'connection: close\r\n\r\n'
    + body,
  );
}

// 重建发往上游的升级请求头:逐条过 rawHeaders(保序保大小写),丢控制/身份/
// 平台头,Host 改写为上游。
function wsRequestHead(req, upstream) {
  const lines = [`${req.method} ${req.url} HTTP/${req.httpVersion}`];
  const hopHeaders = new Set((req.headers.connection || '').toLowerCase().split(',').map((v) => v.trim()));
  for (let i = 0; i + 1 < req.rawHeaders.length; i += 2) {
    const name = req.rawHeaders[i];
    const lower = name.toLowerCase();
    if (WS_DROP_REQUEST_HEADERS.has(lower) || hopHeaders.has(lower)) continue;
    if (DROP_REQUEST_PREFIXES.some((p) => lower.startsWith(p))) continue;
    lines.push(`${name}: ${req.rawHeaders[i + 1]}`);
  }
  lines.push(`Host: ${upstream.host}`, 'Connection: Upgrade', 'Upgrade: websocket');
  return `${lines.join('\r\n')}\r\n\r\n`;
}

function wsRelay(req, socket, head) {
  const started = Date.now();
  const entry = {
    fn: 'chatgpt-relay',
    reqId: first(req.headers['x-fc-request-id']),
    method: req.method,
    path: req.url.split('?')[0],
    ws: true,
  };
  let bytes = 0;
  let finished = false;
  const logOnce = () => {
    if (finished) return;
    finished = true;
    entry.bytes = bytes;
    entry.ms = Date.now() - started;
    console.log(JSON.stringify(entry));
  };

  let cfg;
  try {
    cfg = config();
  } catch (err) {
    entry.error = err.message;
    logOnce();
    wsError(socket, 500, 'relay_misconfigured', err.message);
    return;
  }
  if (!keyMatches(cfg.relayKey, first(req.headers['x-relay-key']))) {
    logOnce();
    wsError(socket, 403, 'bad_relay_key', 'bad or missing X-Relay-Key');
    return;
  }
  const edgeIp = (first(req.headers['x-edge-ip']) || '').trim();
  if (edgeIp) {
    if (!net.isIP(edgeIp) || (!cfg.allowPrivateEdge && !isPublicIP(edgeIp))) {
      logOnce();
      wsError(socket, 400, 'bad_edge_ip', 'X-Edge-IP must be a public IP literal');
      return;
    }
    entry.edge = edgeIp;
  }
  if (!req.url.startsWith('/')) {
    logOnce();
    wsError(socket, 400, 'bad_target', 'request target must be an origin-form path');
    return;
  }

  if (req.method !== 'GET' || (req.headers.upgrade || '').toLowerCase() !== 'websocket') {
    logOnce();
    wsError(socket, 400, 'bad_upgrade', 'only GET websocket upgrades are supported');
    return;
  }

  const { upstream } = cfg;
  const hostname = upstream.hostname.replace(/^\[|\]$/g, '');
  const isHttps = upstream.protocol === 'https:';
  const up = isHttps
    ? tls.connect({
      host: edgeIp || hostname,
      port: Number(upstream.port) || 443,
      servername: net.isIP(hostname) ? undefined : hostname,
      checkServerIdentity: (_host, cert) => tls.checkServerIdentity(hostname, cert),
    })
    : net.connect({
      host: edgeIp || hostname,
      port: Number(upstream.port) || 80,
    });
  up.setNoDelay(true);

  let spliced = false;  // 隧道是否已建立(建了之后只拆连接,不再写 HTTP 错误)
  let replied = false;  // 错误头只写一次,写第二个就是坏协议
  const replyErr = (status, code, msg) => {
    if (replied || socket.destroyed) return;
    replied = true;
    wsError(socket, status, code, msg);
  };

  // 超时覆盖 TCP/TLS 与完整 WS 响应头;升级成功后不设空闲超时。
  const connTimer = setTimeout(() => {
    entry.error = 'ETIMEDOUT';
    up.destroy();
    replyErr(504, 'upstream_timeout', `websocket handshake timeout after ${cfg.connectTimeoutMs}ms`);
    logOnce();
  }, cfg.connectTimeoutMs);
  const disarm = () => clearTimeout(connTimer);

  up.once(isHttps ? 'secureConnect' : 'connect', () => {
    if (socket.destroyed || replied) { up.destroy(); return; }
    entry.edge = edgeIp || up.remoteAddress;
    up.write(wsRequestHead(req, upstream));
    if (head && head.length) up.write(head);
    // splice:write 先于 pipe 排进同一发送队列,顺序不乱。
    socket.pipe(up);
  });

  // 完整握手头到达前暂存,防止部分响应与本地 502/504 拼成损坏的 HTTP。
  let responseHead = Buffer.alloc(0);
  const onHandshake = (chunk) => {
    responseHead = Buffer.concat([responseHead, chunk]);
    const end = responseHead.indexOf('\r\n\r\n');
    if ((end < 0 ? responseHead.length : end + 4) > WS_RESPONSE_HEADER_LIMIT) {
      replyErr(502, 'upstream_bad_handshake', 'upstream response headers too large');
      up.destroy();
      return;
    }
    if (end < 0) return;
    const status = /^HTTP\/1\.[01] (\d{3}) /.exec(responseHead.toString('latin1', 0, end));
    if (!status) {
      replyErr(502, 'upstream_bad_handshake', 'invalid upstream response');
      up.destroy();
      return;
    }
    disarm();
    spliced = true;
    entry.status = Number(status[1]);
    up.removeListener('data', onHandshake);
    // 包含握手之后随包的首帧,必须先写再接上 pipe,保持字节序与背压。
    bytes += responseHead.length;
    socket.write(responseHead);
    responseHead = null;
    up.on('data', (data) => { bytes += data.length; });
    up.pipe(socket);
  };
  up.on('data', onHandshake);
  up.on('end', () => {
    if (!spliced) replyErr(502, 'upstream_unreachable', 'upstream closed before handshake');
  });

  up.on('error', (err) => {
    disarm();
    if (!entry.error) entry.error = err.code || err.message;
    if (!spliced) replyErr(502, 'upstream_unreachable', err.code || err.message);
    else socket.destroy();
    logOnce();
  });
  // 任一端收摊都拆掉对端:客户端断开不白占函数时长,上游断开不吊着客户端。
  socket.on('error', () => { up.destroy(); });
  socket.on('close', () => { disarm(); up.destroy(); logOnce(); });
  up.on('close', () => {
    disarm();
    if (!spliced) replyErr(502, 'upstream_unreachable', 'upstream closed before handshake');
    // 正常 EOF 交给 pipe/end 排空缓冲;只有异常断流才直接销毁客户端。
    else if (!up.readableEnded) socket.destroy();
    logOnce();
  });
}

function wsHandler(req, socket, head) {
  // 鉴权失败也可能遇到客户端复位,必须在任何写操作之前接住 socket error。
  socket.on('error', () => socket.destroy());
  socket.setTimeout(0);
  socket.setNoDelay(true);
  try {
    wsRelay(req, socket, head);
  } catch (err) {
    console.log(JSON.stringify({ fn: 'chatgpt-relay', msg: 'ws handler error', error: String((err && err.stack) || err) }));
    socket.destroy();
  }
}

// FC 对自定义运行时 HTTP Server 的要求(「自定义运行时 基本原理」):监听 0.0.0.0,
// 连接保持 keep-alive,服务端超时不小于函数最大运行时长(24h)。三项超时全部关掉:
//  · keepAliveTimeout:Node 默认 5s 关闭空闲连接,与平台网关的连接复用竞态 ——
//    网关复用到一条刚被关掉的连接,这次调用直接失败;
//  · requestTimeout:Node 18+ 默认须在 300s 内收完请求;
//  · timeout:socket 空闲超时,Node 13+ 默认已是 0,显式写明。
function createServer() {
  const server = http.createServer(handler);
  // HTTP 触发器放行 Upgrade 时(FC 支持 WebSocket 触发),同一端口走隧道;
  // 平台不放行时该事件根本不触发,不影响 HTTP 中继。
  server.on('upgrade', wsHandler);
  server.keepAliveTimeout = 0;
  server.requestTimeout = 0;
  server.timeout = 0;
  return server;
}

// FC 自定义运行时/本地直跑:`node index.js`,监听 $FC_SERVER_PORT(默认 9000)。
if (require.main === module) {
  const server = createServer();
  server.listen(Number(process.env.FC_SERVER_PORT || 9000), '0.0.0.0', () => {
    console.log(JSON.stringify({ fn: 'chatgpt-relay', msg: `listening on 0.0.0.0:${server.address().port}` }));
  });
}

module.exports = {
  handler,
  createServer,
  // 仅供测试触及的内部件
  _internals: {
    isPublicIP, keyMatches, filterRequestHeaders, filterResponseHeaders,
    mintGatewayLabel, mintGatewayTarget, createdModelFromSse, createdModelFromJson, mintPairs,
    fernetIssuedAt, parseModels, mintAttemptTrace, mintFingerprint, fireMintAttempt, mintWsClientFrame, readMintWsFrame,
  },
};
