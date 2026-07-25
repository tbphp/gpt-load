import { spawn } from 'node:child_process'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

const dataDir = await mkdtemp(join(tmpdir(), 'gpt-load-csp-'))
const binary = resolve(
  process.cwd(),
  '..',
  process.platform === 'win32' ? 'gpt-load.exe' : 'gpt-load',
)
const child = spawn(binary, [], {
  env: {
    ...process.env,
    HOST: '127.0.0.1',
    PORT: '3107',
    AUTH_KEY: 'csp-auth-key',
    DATA_DIR: dataDir,
    LOG_LEVEL: 'error',
  },
  stdio: 'ignore',
})

let stopping = false
async function stop(signal = 'SIGTERM') {
  if (stopping) return
  stopping = true
  if (!child.killed) child.kill(signal)
  await rm(dataDir, { recursive: true, force: true })
}

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    void stop(signal).finally(() => process.exit(0))
  })
}

child.on('exit', (code, signal) => {
  void rm(dataDir, { recursive: true, force: true }).finally(() => {
    if (!stopping) {
      const reason = signal ? `signal ${signal}` : `code ${code ?? 1}`
      console.error(`CSP test server exited with ${reason}`)
      process.exit(code ?? 1)
    }
  })
})
