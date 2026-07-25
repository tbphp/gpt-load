import { spawnSync } from 'node:child_process'
import { once } from 'node:events'
import { readdir } from 'node:fs/promises'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

import { expect, it } from 'vitest'

const posixOnlyFailure = 'E2E harness requires a POSIX platform'

async function harnessTempDirectories(): Promise<string[]> {
  return (await readdir(tmpdir())).filter((name) => name.startsWith('gpt-load-e2e-')).sort()
}

it('fails before starting services when the harness runs on Windows', async () => {
  const blocker = createServer()
  blocker.listen(3108, '127.0.0.1')
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
