import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, rename, rm, writeFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { resolveSafeRunDirectory } from './run-directory-safety.mjs'

if (process.platform === 'win32') {
  console.error('E2E harness requires a POSIX platform')
  process.exit(1)
}

const host = '127.0.0.1'
const readyFile = process.env.GPT_LOAD_E2E_READY_FILE
const authKey = process.env.GPT_LOAD_E2E_AUTH_KEY
if (!readyFile || !authKey) {
  console.error('E2E harness configuration is incomplete')
  process.exit(1)
}

const fakeUpstreamForceCloseDelayMs = 250
const fakeUpstreamShutdownTimeoutMs = 1_000
const childShutdownTimeoutMs = 2_000
const startupTimeoutMs = 30_000
const runDirectory = await resolveSafeRunDirectory(readyFile)
const dataDir = await mkdtemp(join(runDirectory, 'data-'))
const binary = resolve(fileURLToPath(new URL('../..', import.meta.url)), 'gpt-load')
const modelList = JSON.stringify({
  object: 'list',
  data: [
    {
      id: 'e2e-model-one',
      object: 'model',
      created: 1_735_689_600,
      owned_by: 'e2e',
    },
    {
      id: 'e2e-model-two',
      object: 'model',
      created: 1_735_689_600,
      owned_by: 'e2e',
    },
  ],
})

async function listenOnEphemeralPort(server) {
  await new Promise((resolveListen, rejectListen) => {
    server.once('error', rejectListen)
    server.listen(0, host, () => {
      server.off('error', rejectListen)
      resolveListen()
    })
  })
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('E2E server address unavailable')
  return address.port
}

async function reserveEphemeralPort() {
  const reservation = createServer()
  const port = await listenOnEphemeralPort(reservation)
  await new Promise((resolveClose, rejectClose) =>
    reservation.close((error) => (error ? rejectClose(error) : resolveClose())),
  )
  return port
}

async function requestBody(request) {
  const chunks = []
  for await (const chunk of request) chunks.push(chunk)
  return Buffer.concat(chunks).toString('utf8')
}

const fakeUpstream = createServer(async (request, response) => {
  if (request.method === 'GET' && request.url === '/v1/models') {
    response.writeHead(200, { 'content-type': 'application/json' })
    response.end(modelList)
    return
  }

  if (request.method === 'POST' && request.url === '/v1/chat/completions') {
    let payload
    try {
      payload = JSON.parse(await requestBody(request))
    } catch {
      response.writeHead(400, { 'content-type': 'application/json' })
      response.end('{"error":{"message":"invalid request"}}')
      return
    }
    const content = payload.messages?.[0]?.content
    if (content === 'partial-usage' && payload.stream === true) {
      response.writeHead(200, {
        'cache-control': 'no-cache',
        connection: 'keep-alive',
        'content-type': 'text/event-stream',
      })
      response.write(
        'data: {"id":"chatcmpl-e2e-partial","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"}}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}\n\n',
      )
      response.end('data: [DONE]\n\n')
      return
    }

    const result = {
      id: `chatcmpl-e2e-${content === 'missing-usage' ? 'missing' : 'complete'}`,
      object: 'chat.completion',
      created: 1_735_689_600,
      model: 'e2e-model-one',
      choices: [
        {
          index: 0,
          message: { role: 'assistant', content: 'ok' },
          finish_reason: 'stop',
        },
      ],
    }
    if (content !== 'missing-usage') {
      result.usage = {
        prompt_tokens: 11,
        completion_tokens: 7,
        total_tokens: 18,
      }
    }
    response.writeHead(200, { 'content-type': 'application/json' })
    response.end(JSON.stringify(result))
    return
  }

  response.writeHead(404, { 'content-type': 'application/json' })
  response.end('{"error":"not_found"}')
})

let upstreamPort
try {
  upstreamPort = await listenOnEphemeralPort(fakeUpstream)
} catch (error) {
  await rm(dataDir, { recursive: true, force: true })
  throw error
}
const appPort = await reserveEphemeralPort()
const child = spawn(binary, [], {
  detached: true,
  env: {
    ...process.env,
    HOST: host,
    PORT: String(appPort),
    AUTH_KEY: authKey,
    DATA_DIR: dataDir,
    DATABASE_DSN: '',
    ENCRYPTION_KEY: '',
    LOG_LEVEL: 'error',
  },
  stdio: 'ignore',
})

async function closeFakeUpstream() {
  if (!fakeUpstream.listening) return
  let forceClose
  let timeout
  const closed = new Promise((resolveClose, rejectClose) => {
    fakeUpstream.close((error) => (error ? rejectClose(error) : resolveClose()))
  })
  fakeUpstream.closeIdleConnections()
  const timedOut = new Promise((_, rejectTimeout) => {
    forceClose = setTimeout(() => fakeUpstream.closeAllConnections(), fakeUpstreamForceCloseDelayMs)
    timeout = setTimeout(
      () => rejectTimeout(new Error('Fake upstream shutdown timed out')),
      fakeUpstreamShutdownTimeoutMs,
    )
  })
  try {
    await Promise.race([closed, timedOut])
  } finally {
    clearTimeout(forceClose)
    clearTimeout(timeout)
  }
}

async function terminateChild() {
  if (child.exitCode !== null || child.signalCode !== null) return
  const exited = once(child, 'exit')
  child.kill('SIGTERM')
  let timeout
  const exitedGracefully = await Promise.race([
    exited.then(() => true),
    new Promise((resolveTimeout) => {
      timeout = setTimeout(() => resolveTimeout(false), childShutdownTimeoutMs)
    }),
  ])
  clearTimeout(timeout)
  if (exitedGracefully) return
  child.kill('SIGKILL')
  await exited
}

let stopping = false
let stopPromise
function stop() {
  if (stopPromise) return stopPromise
  stopping = true
  stopPromise = (async () => {
    const results = await Promise.allSettled([
      terminateChild(),
      closeFakeUpstream(),
      rm(readyFile, { force: true }),
      rm(`${readyFile}.tmp`, { force: true }),
    ])
    await rm(dataDir, { recursive: true, force: true })
    if (results.some(({ status }) => status === 'rejected')) {
      throw new Error('E2E harness cleanup failed')
    }
  })()
  return stopPromise
}

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    void stop().then(
      () => process.exit(0),
      () => process.exit(1),
    )
  })
}

async function waitUntilHealthy() {
  const deadline = Date.now() + startupTimeoutMs
  const healthURL = `http://${host}:${appPort}/health`
  while (Date.now() < deadline) {
    if (child.exitCode !== null || child.signalCode !== null) {
      throw new Error('E2E application exited before readiness')
    }
    try {
      const response = await fetch(healthURL)
      if (response.ok) return
    } catch {
      // The application may still be starting.
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 100))
  }
  throw new Error('E2E application readiness timed out')
}

try {
  await waitUntilHealthy()
  const ready = {
    schema_version: 1,
    project: process.env.GPT_LOAD_E2E_PROJECT ?? 'chromium',
    isolation: 'per-run',
    workers: 1,
    scenario: process.env.GPT_LOAD_E2E_SCENARIO ?? 'full',
    base_url: `http://${host}:${appPort}`,
    upstream_url: `http://${host}:${upstreamPort}`,
    data_dir: dataDir,
  }
  await writeFile(`${readyFile}.tmp`, `${JSON.stringify(ready)}\n`, { mode: 0o600 })
  await rename(`${readyFile}.tmp`, readyFile)
} catch (error) {
  await stop().catch(() => undefined)
  throw error
}

child.on('error', () => {
  if (!stopping) void stop().finally(() => process.exit(1))
})
child.on('exit', (code, signal) => {
  if (stopping) return
  void stop().finally(() => {
    console.error(
      `E2E test server exited unexpectedly (code=${code ?? 'null'}, signal=${signal ?? 'null'})`,
    )
    process.exit(code && code > 0 ? code : 1)
  })
})
