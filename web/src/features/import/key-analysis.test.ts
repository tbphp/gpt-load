import { analyzeKeys } from './key-analysis'

describe('analyzeKeys', () => {
  it('preserves raw input while counting empty, duplicate, and likely downstream-key lines as hints', () => {
    const raw = ' upstream-one \n\nupstream-one\nsk-gl-downstream\n  '

    expect(analyzeKeys(raw)).toEqual({
      raw,
      nonEmptyCount: 3,
      emptyLineCount: 2,
      duplicateCount: 1,
      likelyAccessKeyCount: 1,
      tooManyKeys: false,
    })
  })

  it('blocks only when more than 1,000 non-empty lines are present', () => {
    expect(
      analyzeKeys(Array.from({ length: 1_000 }, (_, index) => `key-${index}`).join('\n'))
        .tooManyKeys,
    ).toBe(false)
    expect(
      analyzeKeys(Array.from({ length: 1_001 }, (_, index) => `key-${index}`).join('\n'))
        .tooManyKeys,
    ).toBe(true)
  })
})
