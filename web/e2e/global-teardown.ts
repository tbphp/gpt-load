import { readdir, readFile, rm } from 'node:fs/promises'
import { join } from 'node:path'

import type { FullConfig } from '@playwright/test'

import { findSensitiveArtifacts, type ArtifactFile } from './artifact-safety'

const credentialCanaries = ['e2e-auth-canary', 'e2e-upstream-key-one']
const artifactSafetyFailure = 'Playwright artifact safety check failed'

async function readArtifactFiles(directory: string): Promise<ArtifactFile[]> {
  let entries
  try {
    entries = await readdir(directory, { withFileTypes: true })
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return []
    throw error
  }

  const nested = await Promise.all(
    entries.map(async (entry): Promise<ArtifactFile[]> => {
      const path = join(directory, entry.name)
      if (entry.isDirectory()) return readArtifactFiles(path)
      return [{ path, bytes: await readFile(path) }]
    }),
  )
  return nested.flat()
}

export default async function globalTeardown(config: FullConfig) {
  try {
    const outputDirectories = [...new Set(config.projects.map(({ outputDir }) => outputDir))]
    const files = (await Promise.all(outputDirectories.map(readArtifactFiles))).flat()
    const offendingPaths = findSensitiveArtifacts(files, credentialCanaries)
    if (offendingPaths.length === 0) return

    await Promise.all(offendingPaths.map((path) => rm(path, { force: true })))
  } catch {
    throw new Error(artifactSafetyFailure)
  }
  throw new Error(artifactSafetyFailure)
}
