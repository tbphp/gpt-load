import { spawnSync } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, readFile, readdir, realpath, rm } from 'node:fs/promises'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { basename, dirname, join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

import { expect, it } from 'vitest'

import { resolveSafeRunDirectory } from './run-directory-safety.mjs'

const posixOnlyFailure = 'E2E harness requires a POSIX platform'

async function harnessTempDirectories(): Promise<string[]> {
  return (await readdir(tmpdir())).filter((name) => name.startsWith('gpt-load-e2e-')).sort()
}

it('fails before starting services when the harness runs on Windows', async () => {
  const blocker = createServer()
  blocker.listen(0, '127.0.0.1')
  await once(blocker, 'listening')
  const before = await harnessTempDirectories()
  const launcherUrl = pathToFileURL(resolve(process.cwd(), 'e2e/start-e2e-server.mjs')).href
  const probe = [
    "Object.defineProperty(process, 'platform', { value: 'win32' })",
    `await import(${JSON.stringify(launcherUrl)})`,
  ].join(';')

  try {
    const result = spawnSync(process.execPath, ['--input-type=module', '--eval', probe], {
      cwd: process.cwd(),
      encoding: 'utf8',
      timeout: 5_000,
    })

    expect(result.status).toBe(1)
    expect(result.stdout).toBe('')
    expect(result.stderr.trim()).toBe(posixOnlyFailure)
    expect(await harnessTempDirectories()).toEqual(before)
  } finally {
    blocker.close()
    await once(blocker, 'close')
  }
})

it('uses ephemeral ports with explicit serial, per-run isolation', async () => {
  const launcherPath = resolve(process.cwd(), 'e2e/start-e2e-server.mjs')
  const runnerPath = resolve(process.cwd(), 'e2e/run-e2e.mjs')
  const source = `${await readFile(launcherPath, 'utf8')}\n${await readFile(runnerPath, 'utf8')}`

  expect(source).toContain('server.listen(0, host')
  expect(source).not.toMatch(/\b3107\b|\b3108\b/)
  expect(source).toContain('`${artifactStem}-ready.json`')
  expect(source).toContain('`${project}-serial-${scenario}`')
  expect(source).toContain("isolation: 'per-run'")
  expect(source).toContain('workers: 1')
})

it('accepts only a canonical harness-owned run directory beside the ready file', async () => {
  const safeDirectory = await mkdtemp(join(tmpdir(), 'gpt-load-e2e-run-'))
  const unsafeDirectory = await mkdtemp(join(tmpdir(), 'foreign-e2e-run-'))
  try {
    const safe = await resolveSafeRunDirectory(join(safeDirectory, 'chromium-ready.json'))
    expect(safe).toBe(await realpath(safeDirectory))
    expect(dirname(safe)).toBe(await realpath(tmpdir()))
    expect(basename(safe)).toMatch(/^gpt-load-e2e-run-/)

    await expect(
      resolveSafeRunDirectory(join(unsafeDirectory, 'chromium-ready.json')),
    ).rejects.toThrow('Unsafe E2E run directory')
  } finally {
    await Promise.all([
      rm(safeDirectory, { recursive: true, force: true }),
      rm(unsafeDirectory, { recursive: true, force: true }),
    ])
  }
})
