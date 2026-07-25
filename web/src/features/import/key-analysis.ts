export interface KeyAnalysis {
  raw: string
  nonEmptyCount: number
  emptyLineCount: number
  duplicateCount: number
  likelyAccessKeyCount: number
  tooManyKeys: boolean
}

export function analyzeKeys(raw: string): KeyAnalysis {
  const lines = raw.split('\n').map((line) => line.trim())
  const nonEmpty = lines.filter(Boolean)
  const seen = new Set<string>()
  let duplicateCount = 0

  for (const line of nonEmpty) {
    if (seen.has(line)) duplicateCount += 1
    else seen.add(line)
  }

  return {
    raw,
    nonEmptyCount: nonEmpty.length,
    emptyLineCount: lines.length - nonEmpty.length,
    duplicateCount,
    likelyAccessKeyCount: nonEmpty.filter((line) => line.startsWith('sk-gl-')).length,
    tooManyKeys: nonEmpty.length > 1_000,
  }
}
