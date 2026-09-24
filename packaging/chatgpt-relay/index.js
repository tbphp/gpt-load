'use strict';
// Transparent chatgpt.com relay for oracle-sg / home / FC.
// RELAY_MODE=transparent: Host/SNI stay chatgpt.com; X-Edge-IP dials that IP.
// Mint/hunt live in gpt-load. This process only changes egress IP.

const crypto = require('node:crypto');
const http = require('node:http');
const https = require('node:https');
const net = require('node:net');
const { pipeline } = require('node:stream');

const UPSTREAM = new URL(process.env.RELAY_UPSTREAM || 'https://chatgpt.com');
const CONNECT_TIMEOUT_MS = Number(process.env.RELAY_CONNECT_TIMEOUT_MS) > 0
  ? Number(process.env.RELAY_CONNECT_TIMEOUT_MS) : 10_000;
const RELAY_KEY = process.env.RELAY_KEY || '';

const DROP_REQUEST = new Set([
  'host', 'connection', 'keep-alive', 'proxy-authorization', 'proxy-connection',
  'te', 'trailer', 'transfer-encoding', 'upgrade', 'expect',
  'x-relay-key', 'x-edge-ip', 'x-relay-mint',
  'forwarded', 'x-forwarded-for', 'x-forwarded-host', 'x-forwarded-port',
  'x-forwarded-proto', 'x-real-ip', 'via',
]);
const DROP_PREFIXES = ['x-fc-', 'x-mint-'];
const DROP_RESPONSE = new Set([
  'connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization',
  'te', 'trailer', 'transfer-encoding', 'upgrade',
]);

function keyMatches(expected, given) {
  if (!expected || typeof given !== 'string') return false;
  const digest = (s) => crypto.createHash('sha256').update(s).digest();
  return crypto.timingSafeEqual(digest(expected), digest(given));
}

function first(v) {
  return Array.isArray(v) ? v[0] : v;
}

function sendError(res, status, code, message) {
  if (res.headersSent) {
    res.destroy();
    return;
  }
  const body = JSON.stringify({
    error: { message: `relay: ${message}`, type: 'relay_error', code },
  });
  res.writeHead(status, {
    'content-type': 'application/json',
    'content-length': Buffer.byteLength(body),
    'x-relay-error': code,
  });
  res.end(body);
}

function filterRequestHeaders(headers) {
  const out = {};
  for (const [name, value] of Object.entries(headers || {})) {
    const lower = name.toLowerCase();
    if (DROP_REQUEST.has(lower)) continue;
    if (DROP_PREFIXES.some((p) => lower.startsWith(p))) continue;
    out[name] = value;
  }
  return out;
}

function filterResponseHeaders(headers) {
  const out = {};
  for (const [name, value] of Object.entries(headers || {})) {
    if (DROP_RESPONSE.has(name)) continue;
    out[name] = value;
  }
  return out;
}

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

function relay(req, res) {
  if (!RELAY_KEY || !keyMatches(RELAY_KEY, first(req.headers['x-relay-key']))) {
    sendError(res, 403, 'bad_relay_key', 'bad or missing X-Relay-Key');
    return;
  }
  if (!req.url.startsWith('/')) {
    sendError(res, 400, 'bad_target', 'request target must be an origin-form path');
    return;
  }
  const edgeIp = String(first(req.headers['x-edge-ip']) || '').trim();
  if (edgeIp && !net.isIP(edgeIp)) {
    sendError(res, 400, 'bad_edge_ip', 'X-Edge-IP must be an IP literal');
    return;
  }
  const isHttps = UPSTREAM.protocol === 'https:';
  const headers = filterRequestHeaders(req.headers);
  headers.host = UPSTREAM.host;
  const upReq = (isHttps ? https : http).request({
    host: edgeIp || UPSTREAM.hostname,
    port: UPSTREAM.port || (isHttps ? 443 : 80),
    method: req.method,
    path: req.url,
    headers,
    agent: false,
    servername: isHttps ? UPSTREAM.hostname : undefined,
  });
  armConnectTimeout(upReq, CONNECT_TIMEOUT_MS);
  upReq.on('response', (upRes) => {
    const out = filterResponseHeaders(upRes.headers);
    if (upRes.socket && upRes.socket.remoteAddress) {
      out['x-relay-edge-ip'] = upRes.socket.remoteAddress;
    }
    if (/^text\/event-stream/i.test(String(upRes.headers['content-type'] || ''))) {
      out['x-accel-buffering'] = 'no';
    }
    res.writeHead(upRes.statusCode, out);
    pipeline(upRes, res, () => {});
  });
  upReq.on('error', (err) => {
    if (res.headersSent) return;
    if (err.code === 'ETIMEDOUT') sendError(res, 504, 'upstream_timeout', err.message);
    else sendError(res, 502, 'upstream_unreachable', err.code || err.message);
  });
  res.on('close', () => {
    if (!res.writableFinished) upReq.destroy();
  });
  req.pipe(upReq);
}

const server = http.createServer((req, res) => {
  try {
    relay(req, res);
  } catch (err) {
    sendError(res, 500, 'internal', 'internal error');
    console.log(JSON.stringify({ fn: 'chatgpt-relay', error: String(err && err.stack || err) }));
  }
});
server.keepAliveTimeout = 0;
server.requestTimeout = 0;
server.timeout = 0;
server.listen(Number(process.env.FC_SERVER_PORT || 9000), '0.0.0.0', () => {
  console.log(JSON.stringify({ fn: 'chatgpt-relay', msg: `listening on 0.0.0.0:${server.address().port}` }));
});
