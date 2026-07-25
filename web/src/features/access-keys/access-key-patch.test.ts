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
  key: 'sk-gl-ACCESS_KEY_CANARY',
  status: 'active',
  filters: { groups: [7], protocols: ['openai-response'], models: ['known', 'free-entry'] },
  rpm_limit: 12,
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

  it('returns an empty update patch for a normalized no-op', () => {
    const draft = createAccessKeyDraft(base)
    draft.name = ' client '

    expect(buildAccessKeyUpdatePatch(base, draft)).toEqual({})
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
