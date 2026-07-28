import { realpath } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, dirname, resolve } from 'node:path'

const runDirectoryPrefix = 'gpt-load-e2e-run-'

export async function resolveSafeRunDirectory(readyFile) {
  if (typeof readyFile !== 'string' || readyFile.length === 0) {
    throw new Error('Unsafe E2E run directory')
  }

  const temporaryRoot = await realpath(tmpdir())
  const candidate = await realpath(dirname(resolve(readyFile)))
  if (dirname(candidate) !== temporaryRoot || !basename(candidate).startsWith(runDirectoryPrefix)) {
    throw new Error('Unsafe E2E run directory')
  }
  return candidate
}
