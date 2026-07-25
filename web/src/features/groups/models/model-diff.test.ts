import type { GroupModelDto } from '@/api/control/types'

import {
  buildModelDiff,
  hasModelRemovals,
  normalizeSelectedModels,
  sameNormalizedModels,
} from './model-diff'

const saved: GroupModelDto[] = [
  { id: 'old', alias: 'public' },
  { id: 'legacy', alias: 'legacy-public' },
]

describe('Group model discovery diff', () => {
  it('preserves saved aliases, keeps missing saved models selected, and adds new candidates', () => {
    expect(buildModelDiff(saved, ['old', 'new', 'old', ' '])).toEqual([
      {
        id: 'old',
        alias: 'public',
        origin: 'persisted',
        rediscovered: true,
        selected: true,
      },
      {
        id: 'legacy',
        alias: 'legacy-public',
        origin: 'persisted',
        rediscovered: false,
        selected: true,
      },
      {
        id: 'new',
        alias: '',
        origin: 'discovered',
        rediscovered: true,
        selected: true,
      },
    ])
  })

  it('preserves every saved alias when one upstream ID has multiple public names', () => {
    expect(
      buildModelDiff(
        [
          { id: 'provider', alias: 'public-a' },
          { id: 'provider', alias: 'public-b' },
        ],
        ['provider'],
      ),
    ).toEqual([
      {
        id: 'provider',
        alias: 'public-a',
        origin: 'persisted',
        rediscovered: true,
        selected: true,
      },
      {
        id: 'provider',
        alias: 'public-b',
        origin: 'persisted',
        rediscovered: true,
        selected: true,
      },
    ])
  })

  it('normalizes the complete selected list and detects normalized no-ops', () => {
    const draft = [
      {
        id: ' old ',
        alias: ' public ',
        origin: 'persisted' as const,
        rediscovered: true,
        selected: true,
      },
      {
        id: 'ignored',
        alias: 'ignored',
        origin: 'persisted' as const,
        rediscovered: false,
        selected: false,
      },
      {
        id: ' new ',
        alias: ' fresh ',
        origin: 'manual' as const,
        rediscovered: false,
        selected: true,
      },
      {
        id: 'new',
        alias: 'fresh',
        origin: 'manual' as const,
        rediscovered: false,
        selected: true,
      },
    ]

    expect(normalizeSelectedModels(draft)).toEqual([
      { id: 'old', alias: 'public' },
      { id: 'new', alias: 'fresh' },
    ])
    expect(sameNormalizedModels([{ id: 'old', alias: 'public' }], [draft[0]!])).toBe(true)
    expect(sameNormalizedModels(saved, draft)).toBe(false)
  })

  it('detects removals by normalized saved model pair without inventing AccessKey dependencies', () => {
    expect(
      hasModelRemovals(saved, [
        {
          id: 'old',
          alias: 'public',
          origin: 'persisted',
          rediscovered: true,
          selected: true,
        },
        {
          id: 'legacy',
          alias: 'legacy-public',
          origin: 'persisted',
          rediscovered: false,
          selected: false,
        },
      ]),
    ).toBe(true)
    expect(hasModelRemovals(saved, buildModelDiff(saved, []))).toBe(false)
  })
})
