import { controlQueryKeys } from './query-keys'

describe('control query keys', () => {
  it('owns stable resource prefixes', () => {
    expect(controlQueryKeys.all).toEqual(['control'])
    expect(controlQueryKeys.groups.all).toEqual(['control', 'groups'])
    expect(controlQueryKeys.groups.details()).toEqual(['control', 'groups', 'detail'])
    expect(controlQueryKeys.groups.keyLists()).toEqual(['control', 'groups', 'keys'])
    expect(controlQueryKeys.accessKeys.all).toEqual(['control', 'access-keys'])
    expect(controlQueryKeys.logs.all).toEqual(['control', 'logs'])
    expect(controlQueryKeys.usage.all).toEqual(['control', 'usage'])
    expect(controlQueryKeys.settingsAll).toEqual(['control', 'settings'])
  })

  it('canonicalizes log identity and excludes drawer-only selection', () => {
    const requestID = 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173'
    expect(
      controlQueryKeys.logs.list({
        status: 'error',
        model: 'gpt-5.6',
        selected_request_id: requestID,
      } as Parameters<typeof controlQueryKeys.logs.list>[0] & {
        selected_request_id: string
      }),
    ).toEqual(['control', 'logs', 'list', { model: 'gpt-5.6', status: 'error' }])
  })

  it('canonicalizes usage identity and excludes unknown fields', () => {
    expect(
      controlQueryKeys.usage.report({
        range: '24h',
        model: 'gpt-5.6',
        selected_request_id: 'ignored',
      } as Parameters<typeof controlQueryKeys.usage.report>[0] & {
        selected_request_id: string
      }),
    ).toEqual([
      'control',
      'usage',
      'report',
      { range: '24h', breakdown_order: 'requests', model: 'gpt-5.6' },
    ])
  })
})
