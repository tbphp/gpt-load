import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, rm } from 'node:fs/promises'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const host = '127.0.0.1'
const appPort = 3107
const upstreamPort = 3108
const dataDir = await mkdtemp(join(tmpdir(), 'gpt-load-e2e-'))
const binary = resolve(
  fileURLToPath(new URL('../..', import.meta.url)),
  process.platform === 'win32' ? 'gpt-load.exe' : 'gpt-load',
)
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
const fakeUpstream = createServer((request, response) => {
  if (request.method === 'GET' && request.url === '/v1/models') {
    response.writeHead(200, { 'content-type': 'application/json' })
    response.end(modelList)
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
  detached: process.platform !== 'win32',
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
  await new Promise((resolveClose, rejectClose) => {
    fakeUpstream.close((error) => {
      if (error) {
        rejectClose(error)
        return
      }
      resolveClose()
    })
  })
}

let stopping = false
let stopPromise

function stop(signal = 'SIGTERM') {
  if (stopPromise) return stopPromise
  stopping = true
  stopPromise = (async () => {
    await closeFakeUpstream()
    if (child.exitCode === null && child.signalCode === null) {
      const exited = once(child, 'exit')
      child.kill(signal)
      await exited
    }
    await rm(dataDir, { recursive: true, force: true })
  })()
  return stopPromise
}

function handleUnexpectedExit(code, signal) {
  if (stopping) return
  stopping = true
  stopPromise = (async () => {
    await closeFakeUpstream()
    await rm(dataDir, { recursive: true, force: true })
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
    void stop(signal).then(
      () => process.exit(0),
      () => process.exit(1),
    )
  })
}

child.on('error', () => handleUnexpectedExit(null, null))
child.on('exit', handleUnexpectedExit)
