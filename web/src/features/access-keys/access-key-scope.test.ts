import type { AccessKeyFiltersDto } from '@/api/control/types'

import {
  createAccessKeyScopeModes,
  materializeAccessKeyFilters,
  validateAccessKeyScope,
} from './access-key-scope'

const all: AccessKeyFiltersDto = { groups: [], protocols: [], models: [] }
const restricted: AccessKeyFiltersDto = {
  groups: [7, 99],
  protocols: ['openai', 'openai-response'],
  models: ['gpt-4.1'],
}

describe('AccessKey scope contract', () => {
  it('models all/restricted independently and never treats empty restricted as all', () => {
    expect(createAccessKeyScopeModes(all)).toEqual({
      groups: 'all',
      protocols: 'all',
      models: 'all',
    })
    expect(createAccessKeyScopeModes(restricted)).toEqual({
      groups: 'restricted',
      protocols: 'restricted',
      models: 'restricted',
    })

    expect(
      materializeAccessKeyFilters(restricted, {
        groups: 'all',
        protocols: 'restricted',
        models: 'all',
      }),
    ).toEqual({
      groups: [],
      protocols: ['openai', 'openai-response'],
      models: [],
    })
    expect(
      validateAccessKeyScope({
        base: null,
        filters: all,
        modes: { groups: 'restricted', protocols: 'all', models: 'all' },
        groupCatalog: { state: 'ready', ids: [7] },
      }),
    ).toBe(false)
  })

  it('retains/removes dangling Groups and reserved protocols but cannot add them', () => {
    const base = restricted
    expect(
      validateAccessKeyScope({
        base,
        filters: restricted,
        modes: createAccessKeyScopeModes(restricted),
        groupCatalog: { state: 'ready', ids: [7] },
      }),
    ).toBe(true)
    expect(
      validateAccessKeyScope({
        base,
        filters: { ...restricted, groups: [7], protocols: ['openai'] },
        modes: createAccessKeyScopeModes(restricted),
        groupCatalog: { state: 'ready', ids: [7] },
      }),
    ).toBe(true)
    expect(
      validateAccessKeyScope({
        base: { groups: [7], protocols: ['openai'], models: [] },
        filters: { groups: [7, 99], protocols: ['openai', 'openai-response'], models: [] },
        modes: { groups: 'restricted', protocols: 'restricted', models: 'all' },
        groupCatalog: { state: 'ready', ids: [7] },
      }),
    ).toBe(false)
  })

  it('fails closed while catalog is unavailable and permits only narrowing stale values', () => {
    const modes = createAccessKeyScopeModes(restricted)
    for (const state of ['loading', 'error'] as const) {
      expect(
        validateAccessKeyScope({
          base: restricted,
          filters: restricted,
          modes,
          groupCatalog: { state, ids: [] },
        }),
      ).toBe(true)
      expect(
        validateAccessKeyScope({
          base: restricted,
          filters: { ...restricted, groups: [7] },
          modes,
          groupCatalog: { state, ids: [] },
        }),
      ).toBe(false)
    }

    expect(
      validateAccessKeyScope({
        base: restricted,
        filters: { groups: [7], protocols: ['openai'], models: ['gpt-4.1'] },
        modes,
        groupCatalog: { state: 'stale', ids: [7] },
      }),
    ).toBe(true)
    expect(
      validateAccessKeyScope({
        base: restricted,
        filters: { ...restricted, groups: [7, 8, 99] },
        modes,
        groupCatalog: { state: 'stale', ids: [7, 8] },
      }),
    ).toBe(false)
    expect(
      validateAccessKeyScope({
        base: null,
        filters: all,
        modes: createAccessKeyScopeModes(all),
        groupCatalog: { state: 'error', ids: [] },
      }),
    ).toBe(false)
  })

  it('uses stale Group data only to constrain Group filters', () => {
    expect(
      validateAccessKeyScope({
        base: {
          groups: [7],
          protocols: ['openai'],
          models: ['legacy-model'],
        },
        filters: {
          groups: [7],
          protocols: ['anthropic'],
          models: ['new-model'],
        },
        modes: {
          groups: 'restricted',
          protocols: 'restricted',
          models: 'restricted',
        },
        groupCatalog: { state: 'stale', ids: [7] },
      }),
    ).toBe(true)
  })
})
