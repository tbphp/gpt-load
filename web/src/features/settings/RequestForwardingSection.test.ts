import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import type { SettingsDto } from '@/api/control/settings'
import { ApiError, NetworkError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import type { SettingsResource } from '@/app/resources/settings'
import { apiWithResponseMetadata, testSettingsETags } from '@/test/api-response'
import { mountApp } from '@/test/mount-app'

import RequestForwardingSection from './RequestForwardingSection.vue'

const inherited: SettingsDto = {
  values: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: {}, remove: [] },
    inject_usage_options: true,
    request_log_retention_days: 7,
  },
  overrides: [],
}

function resource(settings: SettingsDto, settingsETag = testSettingsETags.get): SettingsResource {
  return { settings, settings_etag: settingsETag }
}

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountSection(
  settings: SettingsDto,
  request: ApiClient['request'],
  settingsETag = testSettingsETags.get,
  tokenFor?: Parameters<typeof apiWithResponseMetadata>[1],
) {
  const queryClient = client()
  const initial = resource(settings, settingsETag)
  queryClient.setQueryData(controlQueryKeys.settings('en-US'), initial)
  const mounted = await mountApp(RequestForwardingSection, {
    api: apiWithResponseMetadata(request, tokenFor),
    queryClient,
    locale: 'en-US',
    path: '/settings',
    mounting: { props: { resource: initial }, attachTo: document.body },
  })
  return { ...mounted, initial, queryClient }
}

describe('RequestForwardingSection', () => {
  it('renders four timeouts, validates safe integers, and suppresses no-op saves', async () => {
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountSection(inherited, request)

    expect(wrapper.findAll('[data-test^="setting-timeout-"]')).toHaveLength(4)
    expect(
      wrapper.get('[data-test="settings-header-disclosure"]').attributes('aria-expanded'),
    ).toBe('false')
    const save = wrapper.get('[data-test="request-forwarding-save"]')
    expect(save.attributes()).toHaveProperty('disabled')

    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-request_timeout"]').setValue('0')
    expect(save.attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="error-request_timeout"]').text()).toContain('positive')

    await wrapper.get('[data-test="override-request_timeout"]').setValue(false)
    await save.trigger('click')
    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps HeaderRules masked and blocks case-insensitive duplicate names', async () => {
    const owned: SettingsDto = {
      ...inherited,
      values: {
        ...inherited.values,
        header_rules: { set: { 'X-Secret': 'HEADER_VALUE_CANARY' }, remove: [] },
      },
      overrides: ['header_rules'],
    }
    const { wrapper } = await mountSection(owned, vi.fn() as ApiClient['request'])

    expect(wrapper.get('[data-test="header-value"]').attributes('type')).toBe('password')
    expect(wrapper.text()).not.toContain('HEADER_VALUE_CANARY')
    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    await wrapper.findAll('[data-test="header-name"]')[1]!.setValue('x-secret')
    expect(wrapper.get('[data-test="request-forwarding-save"]').attributes()).toHaveProperty(
      'disabled',
    )
    wrapper.unmount()
  })

  it('disables mutable controls while an OCC write is pending', async () => {
    const request = vi.fn(() => new Promise(() => {})) as ApiClient['request']
    const { wrapper } = await mountSection(inherited, request)
    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')

    expect(wrapper.get('[data-test="override-request_timeout"]').attributes()).toHaveProperty(
      'disabled',
    )
    expect(wrapper.get('[data-test="value-request_timeout"]').attributes()).toHaveProperty(
      'disabled',
    )
    wrapper.unmount()
  })

  it('sends quoted If-Match, rebases the resource, and invalidates only Group details', async () => {
    const owned: SettingsDto = {
      ...inherited,
      values: {
        ...inherited.values,
        header_rules: { set: { 'X-Custom': 'HEADER_RULE_VALUE_CANARY' }, remove: [] },
      },
      overrides: ['request_timeout', 'header_rules'],
    }
    const returned: SettingsDto = {
      ...inherited,
      overrides: ['request_timeout'],
      values: { ...inherited.values, request_timeout: 900 },
    }
    const request = vi.fn().mockResolvedValue(returned) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(owned, request)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    await wrapper.get('[data-test="value-request_timeout"]').setValue('900')
    await wrapper.get('[data-test="override-header_rules"]').setValue(false)
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/settings', {
      method: 'PUT',
      headers: { 'If-Match': `"${testSettingsETags.get}"` },
      json: { settings: { request_timeout: 900, header_rules: null } },
      signal: expect.any(AbortSignal),
    })
    expect(queryClient.getQueryData(controlQueryKeys.settings('en-US'))).toEqual(
      resource(returned, testSettingsETags.put),
    )
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.groups.details(),
    })
    expect(wrapper.get('[data-test="request-forwarding-save"]').attributes()).toHaveProperty(
      'disabled',
    )
    wrapper.unmount()
  })

  it('rebases an external ETag while preserving this section dirty fields', async () => {
    const request = vi.fn().mockResolvedValue(inherited) as ApiClient['request']
    const { wrapper } = await mountSection(inherited, request)
    await wrapper.get('[data-test="override-connect_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-connect_timeout"]').setValue('30')

    const refreshed: SettingsDto = {
      ...inherited,
      values: { ...inherited.values, request_timeout: 900 },
      overrides: ['request_timeout'],
    }
    await wrapper.setProps({
      resource: resource(refreshed, `sha256-${'c'.repeat(64)}`),
    })
    await flushPromises()
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')

    expect(request).toHaveBeenCalledWith('/api/settings', {
      method: 'PUT',
      headers: { 'If-Match': `"sha256-${'c'.repeat(64)}"` },
      json: { settings: { connect_timeout: 30 } },
      signal: expect.any(AbortSignal),
    })
    wrapper.unmount()
  })

  it('blocks a same-field external refresh before a save can bypass OCC', async () => {
    const owned: SettingsDto = {
      ...inherited,
      values: { ...inherited.values, connect_timeout: 15 },
      overrides: ['connect_timeout'],
    }
    const { wrapper } = await mountSection(owned, vi.fn() as ApiClient['request'])
    await wrapper.get('[data-test="value-connect_timeout"]').setValue('30')
    await wrapper.setProps({
      resource: resource(
        {
          ...owned,
          values: { ...owned.values, connect_timeout: 45 },
        },
        `sha256-${'c'.repeat(64)}`,
      ),
    })
    await flushPromises()

    expect(wrapper.get('[data-test="settings-conflicts"]').text()).toContain('Latest: 45')
    expect(wrapper.get('[data-test="request-forwarding-save"]').attributes()).toHaveProperty(
      'disabled',
    )
    wrapper.unmount()
  })

  it('labels and blocks a divergent external HeaderRules refresh without exposing values', async () => {
    const owned: SettingsDto = {
      ...inherited,
      values: {
        ...inherited.values,
        header_rules: { set: { 'X-Secret': 'BASE_HEADER_CANARY' }, remove: [] },
      },
      overrides: ['header_rules'],
    }
    const { wrapper } = await mountSection(owned, vi.fn() as ApiClient['request'])
    await wrapper.get('[data-test="header-value"]').setValue('DRAFT_HEADER_CANARY')
    await wrapper.setProps({
      resource: resource(
        {
          ...owned,
          values: {
            ...owned.values,
            header_rules: { set: { 'X-Secret': 'LATEST_HEADER_CANARY' }, remove: [] },
          },
        },
        `sha256-${'c'.repeat(64)}`,
      ),
    })
    await flushPromises()

    const conflict = wrapper.get('[data-test="settings-conflicts"]')
    expect(conflict.text()).toContain('Advanced HeaderRules')
    expect(conflict.text()).toContain('1 Set and 0 Remove rules')
    expect(conflict.text()).not.toContain('DRAFT_HEADER_CANARY')
    expect(conflict.text()).not.toContain('LATEST_HEADER_CANARY')
    wrapper.unmount()
  })

  it('reconciles a network-unknown write through GET without reporting a definite failure', async () => {
    const applied: SettingsDto = {
      ...inherited,
      values: { ...inherited.values, connect_timeout: 30 },
      overrides: ['connect_timeout'],
    }
    const requestMock = vi
      .fn()
      .mockRejectedValueOnce(new NetworkError())
      .mockResolvedValueOnce(applied)
    const request = requestMock as ApiClient['request']
    const { wrapper } = await mountSection(
      inherited,
      request,
      testSettingsETags.get,
      () => `sha256-${'e'.repeat(64)}`,
    )

    await wrapper.get('[data-test="override-connect_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-connect_timeout"]').setValue('30')
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    await flushPromises()

    expect(requestMock.mock.calls[1]).toEqual([
      '/api/settings',
      { signal: expect.any(AbortSignal) },
    ])
    expect(wrapper.text()).toContain('Settings saved.')
    expect(wrapper.text()).not.toContain('Unable to update')
    wrapper.unmount()
  })

  it('keeps an unresolved network-unknown write indeterminate and offers Check result', async () => {
    const request = vi
      .fn()
      .mockRejectedValueOnce(new NetworkError())
      .mockResolvedValueOnce(inherited) as ApiClient['request']
    const { wrapper } = await mountSection(inherited, request)

    await wrapper.get('[data-test="override-connect_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-connect_timeout"]').setValue('30')
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="settings-indeterminate"]').text()).toContain(
      'could not be confirmed',
    )
    expect(wrapper.find('[data-test="settings-check-result"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('Unable to update')
    wrapper.unmount()
  })

  it('refetches instead of ordering unrelated response and cache ETags', async () => {
    let resolve!: (value: SettingsDto) => void
    const late = new Promise<SettingsDto>((done) => {
      resolve = done
    })
    const request = vi.fn(() => late) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(inherited, request)
    const refetch = vi.spyOn(queryClient, 'refetchQueries')
    const unrelated = resource(
      { ...inherited, values: { ...inherited.values, first_byte_timeout: 211 } },
      `sha256-${'c'.repeat(64)}`,
    )

    await wrapper.get('[data-test="override-connect_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-connect_timeout"]').setValue('33')
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    queryClient.setQueryData(controlQueryKeys.settings('en-US'), unrelated)
    resolve({ ...inherited, values: { ...inherited.values, connect_timeout: 99 } })
    await flushPromises()

    expect(refetch).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.settings('en-US'),
      exact: true,
    })
    expect(queryClient.getQueryData(controlQueryKeys.settings('en-US'))).toEqual(unrelated)
    expect(wrapper.text()).not.toContain('Settings saved.')
    wrapper.unmount()
  })

  it('three-way merges a non-conflicting 412 and retries with the latest ETag', async () => {
    const latestToken = `sha256-${'c'.repeat(64)}`
    const latest: SettingsDto = {
      ...inherited,
      values: { ...inherited.values, request_timeout: 900 },
      overrides: ['request_timeout'],
    }
    const returned: SettingsDto = {
      ...latest,
      values: { ...latest.values, connect_timeout: 30 },
      overrides: ['connect_timeout', 'request_timeout'],
    }
    const requestMock = vi
      .fn()
      .mockRejectedValueOnce(
        new ApiError(412, 'SETTINGS_VERSION_CONFLICT', 'conflict', {
          settings: latest,
          settings_etag: latestToken,
        }),
      )
      .mockResolvedValueOnce(returned)
    const request = requestMock as ApiClient['request']
    const { wrapper } = await mountSection(inherited, request)

    await wrapper.get('[data-test="override-connect_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-connect_timeout"]').setValue('30')
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('merged with your non-conflicting edits')
    expect(wrapper.find('[data-test="settings-conflicts"]').exists()).toBe(false)

    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    await flushPromises()
    expect(requestMock.mock.calls[1]?.[1]).toMatchObject({
      headers: { 'If-Match': `"${latestToken}"` },
      json: { settings: { connect_timeout: 30 } },
    })
    wrapper.unmount()
  })

  it('blocks a same-field 412 until mine/latest is explicitly chosen', async () => {
    const latestToken = `sha256-${'d'.repeat(64)}`
    const base: SettingsDto = {
      ...inherited,
      values: { ...inherited.values, connect_timeout: 15 },
      overrides: ['connect_timeout'],
    }
    const latest: SettingsDto = {
      ...base,
      values: { ...base.values, connect_timeout: 45 },
    }
    const request = vi.fn().mockRejectedValue(
      new ApiError(412, 'SETTINGS_VERSION_CONFLICT', 'conflict', {
        settings: latest,
        settings_etag: latestToken,
      }),
    ) as ApiClient['request']
    const { wrapper } = await mountSection(base, request)

    await wrapper.get('[data-test="value-connect_timeout"]').setValue('30')
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    await flushPromises()

    const conflict = wrapper.get('[data-test="settings-conflicts"]')
    expect(conflict.text()).toContain('Mine: 30')
    expect(conflict.text()).toContain('Latest: 45')
    expect(wrapper.get('[data-test="request-forwarding-save"]').attributes()).toHaveProperty(
      'disabled',
    )
    const useLatest = conflict.findAll('button').find((button) => button.text() === 'Use latest')
    await useLatest?.trigger('click')
    expect(
      (wrapper.get('[data-test="value-connect_timeout"]').element as HTMLInputElement).value,
    ).toBe('45')
    expect(wrapper.find('[data-test="settings-conflicts"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('ignores a late response after unmount without recreating Settings cache', async () => {
    let resolve!: (value: SettingsDto) => void
    const late = new Promise<SettingsDto>((done) => {
      resolve = done
    })
    const request = vi.fn(() => late) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(inherited, request)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    wrapper.unmount()
    queryClient.removeQueries({ queryKey: controlQueryKeys.settings('en-US'), exact: true })
    resolve(inherited)
    await flushPromises()

    expect(queryClient.getQueryData(controlQueryKeys.settings('en-US'))).toBeUndefined()
    expect(invalidate).not.toHaveBeenCalled()
  })
})
