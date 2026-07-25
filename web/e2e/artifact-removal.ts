export type ArtifactRemover = (path: string) => Promise<void>

export async function removeArtifacts(
  paths: string[],
  removeArtifact: ArtifactRemover,
): Promise<boolean> {
  const results = await Promise.allSettled(paths.map(removeArtifact))
  return results.every(({ status }) => status === 'fulfilled')
}
