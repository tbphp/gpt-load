export interface KeyAnalysis {
  raw: string
  nonEmptyCount: number
  emptyLineCount: number
  duplicateCount: number
  likelyAccessKeyCount: number
  tooManyKeys: boolean
}

export function analyzeKeys(raw: string): KeyAnalysis {
  const lines = raw
    .replace(/\r/g, '')
    .split('\n')
    .map((line) => line.trim())
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
    emptyLineCount: raw ? lines.length - nonEmpty.length : 0,
    duplicateCount,
    likelyAccessKeyCount: nonEmpty.filter((line) => /^sk-gl-/i.test(line)).length,
    tooManyKeys: nonEmpty.length > 1_000,
  }
}
