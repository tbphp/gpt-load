import { constants, existsSync } from 'node:fs'
import { access, chmod, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import type { FullConfig } from '@playwright/test'
import { describe, expect, it } from 'vitest'

import { removeArtifacts } from './artifact-removal'
import globalTeardown from './global-teardown'

const authCanary = 'e2e-auth-canary'
const upstreamCanary = 'e2e-upstream-key-one'
const generatedKey = 'sk-gl-0123456789abcdef0123456789abcdef'
const sanitizedFailure = 'Playwright artifact safety check failed'

function teardownConfig(outputDir: string): FullConfig {
  return {
    projects: [{ outputDir }],
  } as FullConfig
}

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((next) => {
    resolve = next
  })
  return { promise, resolve }
}

describe('globalTeardown', () => {
  it('deletes sensitive artifacts before rejecting without exposing secrets or paths', async () => {
    const outputDir = await mkdtemp(join(tmpdir(), 'gpt-load-artifact-safety-'))
    const nestedDir = join(outputDir, 'nested')
    await mkdir(nestedDir)
    const sensitivePath = join(nestedDir, `${authCanary}-${generatedKey}.txt`)
    await writeFile(sensitivePath, `${upstreamCanary} ${generatedKey}`)

    try {
      const rejection = await globalTeardown(teardownConfig(outputDir)).catch(
        (error: unknown) => error,
      )

      await expect(access(sensitivePath, constants.F_OK)).rejects.toMatchObject({
        code: 'ENOENT',
      })
      expect(rejection).toBeInstanceOf(Error)
      expect((rejection as Error).message).toBe(sanitizedFailure)
      expect((rejection as Error).message).not.toContain(authCanary)
      expect((rejection as Error).message).not.toContain(generatedKey)
      expect((rejection as Error).message).not.toContain(sensitivePath)
    } finally {
      await rm(outputDir, { recursive: true, force: true })
    }
  })

  it.skipIf(process.platform === 'win32')(
    'rejects with a sanitized error when a sensitive artifact cannot be deleted',
    async () => {
      const outputDir = await mkdtemp(join(tmpdir(), 'gpt-load-artifact-safety-'))
      const sensitivePath = join(outputDir, `${authCanary}-${generatedKey}.txt`)
      await writeFile(sensitivePath, `${upstreamCanary} ${generatedKey}`)
      await chmod(outputDir, 0o500)

      try {
        const rejection = await globalTeardown(teardownConfig(outputDir)).catch(
          (error: unknown) => error,
        )

        await expect(access(sensitivePath, constants.F_OK)).resolves.toBeUndefined()
        expect(rejection).toBeInstanceOf(Error)
        expect((rejection as Error).message).toBe(sanitizedFailure)
        expect((rejection as Error).message).not.toContain(authCanary)
        expect((rejection as Error).message).not.toContain(generatedKey)
        expect((rejection as Error).message).not.toContain(sensitivePath)
      } finally {
        await chmod(outputDir, 0o700)
        await rm(outputDir, { recursive: true, force: true })
      }
    },
  )

  it.skipIf(process.platform === 'win32')(
    'waits for every sensitive artifact deletion attempt before rejecting',
    async () => {
      const outputDir = await mkdtemp(join(tmpdir(), 'gpt-load-artifact-safety-'))
      const protectedDir = join(outputDir, 'protected')
      await mkdir(protectedDir)
      const protectedPath = join(protectedDir, `${authCanary}.txt`)
      await writeFile(protectedPath, upstreamCanary)
      await chmod(protectedDir, 0o500)
      const removablePath = join(outputDir, `removable-${authCanary}.txt`)
      await writeFile(removablePath, generatedKey)

      const removableStarted = deferred()
      const protectedFailed = deferred()
      const releaseProtectedFailure = deferred()
      const releaseRemovable = deferred()
      const removableFinished = deferred()
      const removalPromise = removeArtifacts([protectedPath, removablePath], async (path) => {
        if (path === protectedPath) {
          await removableStarted.promise
          try {
            await rm(path, { force: true })
          } catch (error) {
            protectedFailed.resolve()
            await releaseProtectedFailure.promise
            throw error
          }
          return
        }

        removableStarted.resolve()
        await releaseRemovable.promise
        await rm(path, { force: true })
        removableFinished.resolve()
      })

      try {
        await protectedFailed.promise
        let removalCompleted = false
        void removalPromise.then(() => {
          removalCompleted = true
        })
        releaseProtectedFailure.resolve()
        await new Promise<void>((resolve) => setImmediate(resolve))
        const completedBeforeRelease = removalCompleted

        releaseRemovable.resolve()
        const removed = await removalPromise
        await removableFinished.promise

        expect(completedBeforeRelease).toBe(false)
        expect(removed).toBe(false)
        expect(existsSync(removablePath)).toBe(false)
        expect(existsSync(protectedPath)).toBe(true)
      } finally {
        releaseProtectedFailure.resolve()
        releaseRemovable.resolve()
        await removalPromise
        await chmod(protectedDir, 0o700)
        await rm(outputDir, { recursive: true, force: true })
      }
    },
  )

  it.skipIf(process.platform === 'win32')(
    'rejects with a sanitized error when an artifact directory cannot be scanned',
    async () => {
      const outputDir = await mkdtemp(join(tmpdir(), `gpt-load-${authCanary}-`))
      await chmod(outputDir, 0o000)

      try {
        const rejection = await globalTeardown(teardownConfig(outputDir)).catch(
          (error: unknown) => error,
        )

        expect(rejection).toBeInstanceOf(Error)
        expect((rejection as Error).message).toBe(sanitizedFailure)
        expect((rejection as Error).message).not.toContain(authCanary)
        expect((rejection as Error).message).not.toContain(outputDir)
      } finally {
        await chmod(outputDir, 0o700)
        await rm(outputDir, { recursive: true, force: true })
      }
    },
  )
})
