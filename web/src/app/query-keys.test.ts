import { controlQueryKeys } from './query-keys'

describe('controlQueryKeys', () => {
  it('uses the approved stable hierarchy', () => {
    expect(controlQueryKeys).toMatchObject({
      all: ['control'],
      groups: {
        all: ['control', 'groups'],
      },
    })
    expect(controlQueryKeys.groups.list()).toEqual(['control', 'groups', 'list'])
    expect(controlQueryKeys.groups.details()).toEqual(['control', 'groups', 'detail'])
    expect(controlQueryKeys.groups.detail(42)).toEqual(['control', 'groups', 'detail', 42])
    expect(controlQueryKeys.groups.keyLists()).toEqual(['control', 'groups', 'keys'])
    expect(controlQueryKeys.groups.keys(42)).toEqual(['control', 'groups', 'keys', 42])
    expect(controlQueryKeys.health()).toEqual(['control', 'health'])
    expect(controlQueryKeys.logs.list({ status: 'error' })).toEqual([
      'control',
      'logs',
      'list',
      { status: 'error' },
    ])
    expect(controlQueryKeys.accessKeys.list()).toEqual(['control', 'access-keys', 'list'])
    expect(controlQueryKeys.accessKeys.options()).toEqual(['control', 'access-keys', 'options'])
    expect(controlQueryKeys.settings()).toEqual(['control', 'settings'])
    expect(controlQueryKeys.systemInfo()).toEqual(['control', 'system-info'])
    expect(controlQueryKeys.modelPrices()).toEqual(['control', 'model-prices'])
  })

  it('contains normalized resource identity but never secret material', () => {
    const keys = [
      controlQueryKeys.groups.detail(7),
      controlQueryKeys.groups.keys(7),
      controlQueryKeys.logs.list({ access_key_id: 12 }),
      controlQueryKeys.accessKeys.list(),
      controlQueryKeys.accessKeys.options(),
    ]

    expect(JSON.stringify(keys)).not.toContain('AUTH_KEY_CANARY')
    expect(JSON.stringify(keys)).not.toContain('ACCESS_KEY_CANARY')
    expect(JSON.stringify(keys)).not.toContain('UPSTREAM_KEY_CANARY')
    expect(JSON.stringify(keys)).not.toContain('HEADER_RULE_CANARY')
  })
})
