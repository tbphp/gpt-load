import type { AccessKeyDto } from '@/api/control/types'

import {
  buildAccessKeyUpdatePatch,
  buildCreateAccessKeyInput,
  createAccessKeyDraft,
  isAccessKeyDraftValid,
  type AccessKeyDraft,
} from './access-key-patch'

const base: AccessKeyDto = {
  id: 9,
  name: 'client',
  masked_key: 'sk-gl-••••••••••••',
  status: 'active',
  filters: { groups: [7], protocols: ['openai-response'], models: ['known', 'free-entry'] },
  rpm_limit: 12,
  created_at: '2026-07-28T00:00:00Z',
  updated_at: '2026-07-28T00:00:00Z',
}

describe('AccessKey request normalization', () => {
  it('builds a create body with exact empty arrays, zero RPM, and no status', () => {
    const draft = createAccessKeyDraft()
    draft.name = ' client '

    const input = buildCreateAccessKeyInput(draft)

    expect(input).toEqual({
      name: 'client',
      filters: { groups: [], protocols: [], models: [] },
      rpm_limit: 0,
    })
    expect(input).not.toHaveProperty('status')
  })

  it('rejects an injected reserved protocol for a create draft', () => {
    const draft = createAccessKeyDraft()
    draft.name = 'new-client'
    draft.filters.protocols = ['openai-response']
    draft.scopeModes.protocols = 'restricted'

    expect(isAccessKeyDraftValid(draft)).toBe(false)
  })

  it('rejects an injected reserved protocol for an ordinary edit draft', () => {
    const ordinaryBase: AccessKeyDto = {
      ...base,
      filters: { ...base.filters, protocols: ['openai'] },
    }
    const draft = createAccessKeyDraft(ordinaryBase)
    draft.filters.protocols = ['openai', 'openai-response']

    expect(isAccessKeyDraftValid(draft, ordinaryBase)).toBe(false)
  })

  it('accepts retaining a historical reserved protocol alongside enabled changes', () => {
    const draft = createAccessKeyDraft(base)
    draft.filters.protocols = ['openai-response', 'gemini']

    expect(isAccessKeyDraftValid(draft, base)).toBe(true)
  })

  it('accepts explicit historical reserved removal and includes it in the update patch', () => {
    const draft = createAccessKeyDraft(base)
    draft.filters.protocols = []
    draft.scopeModes.protocols = 'all'

    expect(isAccessKeyDraftValid(draft, base)).toBe(true)
    expect(buildAccessKeyUpdatePatch(base, draft)).toEqual({
      filters: {
        groups: [7],
        protocols: [],
        models: ['known', 'free-entry'],
      },
    })
  })

  it('keeps reserved draft values intact in builders instead of silently filtering them', () => {
    const draft = createAccessKeyDraft()
    draft.name = 'new-client'
    draft.filters.protocols = ['openai-response']
    draft.scopeModes.protocols = 'restricted'

    expect(buildCreateAccessKeyInput(draft).filters.protocols).toEqual(['openai-response'])
  })

  it('returns an empty update patch for a normalized no-op', () => {
    const draft = createAccessKeyDraft(base)
    draft.name = ' client '

    expect(buildAccessKeyUpdatePatch(base, draft)).toEqual({})
  })

  it('treats filter arrays with the same members in a different order as unchanged sets', () => {
    const reorderedBase: AccessKeyDto = {
      ...base,
      filters: {
        groups: [7, 3],
        protocols: ['openai-response', 'anthropic'],
        models: ['known', 'free-entry'],
      },
    }
    const draft = createAccessKeyDraft(reorderedBase)
    draft.filters = {
      groups: [3, 7],
      protocols: ['anthropic', 'openai-response'],
      models: ['free-entry', 'known'],
    }

    expect(buildAccessKeyUpdatePatch(reorderedBase, draft)).toEqual({})
  })

  it('sends only normalized dirty fields and preserves empty filters as exact arrays', () => {
    const draft = createAccessKeyDraft(base)
    draft.status = 'disabled'
    draft.filters = { groups: [], protocols: [], models: [] }
    draft.rpm_limit = 0

    expect(buildAccessKeyUpdatePatch(base, draft)).toEqual({
      status: 'disabled',
      filters: { groups: [], protocols: [], models: [] },
      rpm_limit: 0,
    })
  })

  it.each([-1, 1.5, Number.MAX_SAFE_INTEGER + 1, Infinity, Number.NaN])(
    'rejects unsafe or negative RPM %s',
    (rpm) => {
      const draft: AccessKeyDraft = { ...createAccessKeyDraft(), name: 'client', rpm_limit: rpm }
      expect(isAccessKeyDraftValid(draft)).toBe(false)
    },
  )

  it('accepts zero and positive safe-integer RPM values', () => {
    expect(isAccessKeyDraftValid({ ...createAccessKeyDraft(), name: 'client', rpm_limit: 0 })).toBe(
      true,
    )
    expect(
      isAccessKeyDraftValid({
        ...createAccessKeyDraft(),
        name: 'client',
        rpm_limit: Number.MAX_SAFE_INTEGER,
      }),
    ).toBe(true)
  })
})
