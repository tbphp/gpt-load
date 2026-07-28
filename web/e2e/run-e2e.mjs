import { randomBytes } from 'node:crypto'
import { spawn } from 'node:child_process'
import { access, mkdtemp, readFile, realpath, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { resolveSafeRunDirectory } from './run-directory-safety.mjs'

if (process.platform === 'win32') {
  console.error('E2E harness requires a POSIX platform')
  process.exit(1)
}

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const repositoryRoot = resolve(webRoot, '..')
const binary = join(repositoryRoot, 'gpt-load')
await access(binary)

const project = 'chromium'
const playwrightArgs = process.argv.slice(2)
const scenario = (playwrightArgs.find((value) => value.endsWith('.spec.ts')) ?? 'full')
  .replace(/^.*\//, '')
  .replace(/\.spec\.ts$/, '')
  .replace(/[^a-z0-9-]/gi, '-')
const artifactStem = `${project}-serial-${scenario}`
const runDirectory = await mkdtemp(join(tmpdir(), 'gpt-load-e2e-run-'))
const readyFile = join(runDirectory, `${artifactStem}-ready.json`)
const authKey = `e2e-auth-${randomBytes(24).toString('hex')}`
const server = spawn(process.execPath, [join(webRoot, 'e2e/start-e2e-server.mjs')], {
  cwd: webRoot,
  env: {
    ...process.env,
    GPT_LOAD_E2E_READY_FILE: readyFile,
    GPT_LOAD_E2E_AUTH_KEY: authKey,
    GPT_LOAD_E2E_PROJECT: project,
    GPT_LOAD_E2E_SCENARIO: scenario,
  },
  stdio: ['ignore', 'inherit', 'inherit'],
})

function waitForExit(child) {
  return new Promise((resolveExit) =>
    child.once('exit', (code, signal) => resolveExit({ code, signal })),
  )
}

const serverExit = waitForExit(server)
let interrupted = false
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    interrupted = true
    server.kill(signal)
  })
}

async function waitForReady() {
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    if (server.exitCode !== null || server.signalCode !== null) {
      throw new Error('E2E harness exited before readiness')
    }
    try {
      const ready = JSON.parse(await readFile(readyFile, 'utf8'))
      for (const field of ['base_url', 'upstream_url']) {
        const url = new URL(ready[field])
        if (url.hostname !== '127.0.0.1' || Number(url.port) <= 0) {
          throw new Error('E2E harness returned an unsafe endpoint')
        }
      }
      return ready
    } catch (error) {
      if (error instanceof SyntaxError || error?.code === 'ENOENT') {
        await new Promise((resolveWait) => setTimeout(resolveWait, 100))
        continue
      }
      throw error
    }
  }
  throw new Error('E2E harness ready artifact timed out')
}

let exitCode = 1
try {
  const ready = await waitForReady()
  const cli = join(webRoot, 'node_modules/@playwright/test/cli.js')
  const playwright = spawn(
    process.execPath,
    [cli, 'test', ...playwrightArgs, '--config', 'playwright.config.ts'],
    {
      cwd: webRoot,
      env: {
        ...process.env,
        GPT_LOAD_E2E_BASE_URL: ready.base_url,
        GPT_LOAD_E2E_UPSTREAM_URL: ready.upstream_url,
        GPT_LOAD_E2E_AUTH_KEY: authKey,
        GPT_LOAD_E2E_ARTIFACT_DIR: `test-results/${artifactStem}`,
      },
      stdio: 'inherit',
    },
  )
  const result = await waitForExit(playwright)
  exitCode = result.code ?? 1
} finally {
  if (server.exitCode === null && server.signalCode === null) server.kill('SIGTERM')
  await serverExit
  const safeRunDirectory = await resolveSafeRunDirectory(readyFile)
  if (safeRunDirectory !== (await realpath(runDirectory))) {
    throw new Error('Refusing to remove an unsafe E2E run directory')
  }
  await rm(safeRunDirectory, { recursive: true, force: true })
}

process.exit(interrupted ? 130 : exitCode)
