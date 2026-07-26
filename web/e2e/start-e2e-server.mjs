import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, rm } from 'node:fs/promises'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

if (process.platform === 'win32') {
  console.error('E2E harness requires a POSIX platform')
  process.exit(1)
}

const host = '127.0.0.1'
const appPort = 3107
const upstreamPort = 3108
const fakeUpstreamForceCloseDelayMs = 250
const fakeUpstreamShutdownTimeoutMs = 1_000
const childShutdownTimeoutMs = 2_000
const dataDir = await mkdtemp(join(tmpdir(), 'gpt-load-e2e-'))
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

try {
  await new Promise((resolveListen, rejectListen) => {
    fakeUpstream.once('error', rejectListen)
    fakeUpstream.listen(upstreamPort, host, () => {
      fakeUpstream.off('error', rejectListen)
      resolveListen()
    })
  })
} catch (error) {
  await rm(dataDir, { recursive: true, force: true })
  throw error
}

const child = spawn(binary, [], {
  detached: true,
  env: {
    ...process.env,
    HOST: host,
    PORT: String(appPort),
    AUTH_KEY: 'e2e-auth-canary',
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
    fakeUpstream.close((error) => {
      if (error) {
        rejectClose(error)
        return
      }
      resolveClose()
    })
  })
  fakeUpstream.closeIdleConnections()
  const timedOut = new Promise((_, rejectTimeout) => {
    forceClose = setTimeout(() => {
      fakeUpstream.closeAllConnections()
    }, fakeUpstreamForceCloseDelayMs)
    timeout = setTimeout(() => {
      rejectTimeout(new Error('Fake upstream shutdown timed out'))
    }, fakeUpstreamShutdownTimeoutMs)
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
    let cleanupFailed = false
    try {
      await closeFakeUpstream()
    } catch {
      cleanupFailed = true
    }
    try {
      await terminateChild()
    } catch {
      cleanupFailed = true
    }
    try {
      await rm(dataDir, { recursive: true, force: true })
    } catch {
      cleanupFailed = true
    }
    if (cleanupFailed) throw new Error('E2E harness cleanup failed')
  })()
  return stopPromise
}

function handleUnexpectedExit(code, signal) {
  if (stopping) return
  stopping = true
  stopPromise = (async () => {
    try {
      await closeFakeUpstream()
    } finally {
      await rm(dataDir, { recursive: true, force: true })
    }
  })()
  void stopPromise.finally(() => {
    console.error(
      `E2E test server exited unexpectedly (code=${code ?? 'null'}, signal=${signal ?? 'null'})`,
    )
    process.exit(code && code > 0 ? code : 1)
  })
}

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    void stop().then(
      () => process.exit(0),
      () => process.exit(1),
    )
  })
}

child.on('error', () => handleUnexpectedExit(null, null))
child.on('exit', handleUnexpectedExit)
