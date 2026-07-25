import { constants } from 'node:fs'
import { access, chmod, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import type { FullConfig } from '@playwright/test'
import { describe, expect, it } from 'vitest'

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
