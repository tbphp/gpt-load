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
    expect(controlQueryKeys.accessKeys.list()).toEqual(['control', 'access-keys', 'list'])
    expect(controlQueryKeys.settings()).toEqual(['control', 'settings'])
    expect(controlQueryKeys.systemInfo()).toEqual(['control', 'system-info'])
  })

  it('contains normalized resource identity but never secret material', () => {
    const keys = [
      controlQueryKeys.groups.detail(7),
      controlQueryKeys.groups.keys(7),
      controlQueryKeys.accessKeys.list(),
    ]

    expect(JSON.stringify(keys)).not.toContain('AUTH_KEY_CANARY')
    expect(JSON.stringify(keys)).not.toContain('ACCESS_KEY_CANARY')
    expect(JSON.stringify(keys)).not.toContain('UPSTREAM_KEY_CANARY')
    expect(JSON.stringify(keys)).not.toContain('HEADER_RULE_CANARY')
  })
})
