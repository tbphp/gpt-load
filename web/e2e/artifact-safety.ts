export interface ArtifactFile {
  path: string
  bytes: Uint8Array
}

const generatedKeyPattern = /sk-gl-[0-9a-f]{32}/
const decoder = new TextDecoder()

export function findSensitiveArtifacts(files: ArtifactFile[], canaries: string[]): string[] {
  return files
    .filter(({ bytes }) => {
      const contents = decoder.decode(bytes)
      return (
        canaries.some((canary) => contents.includes(canary)) || generatedKeyPattern.test(contents)
      )
    })
    .map(({ path }) => path)
}
