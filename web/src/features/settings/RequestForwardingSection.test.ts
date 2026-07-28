import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import type { SettingsDto } from '@/api/control/settings'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import RequestForwardingSection from './RequestForwardingSection.vue'

const inherited: SettingsDto = {
  revision: 4,
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

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountSection(settings: SettingsDto, request: ApiClient['request']) {
  const queryClient = client()
  queryClient.setQueryData(controlQueryKeys.settings(), settings)
  const mounted = await mountApp(RequestForwardingSection, {
    api: { request },
    queryClient,
    locale: 'en-US',
    path: '/settings',
    mounting: { props: { settings }, attachTo: document.body },
  })
  return { ...mounted, queryClient }
}

describe('RequestForwardingSection', () => {
  it('shows exactly four timeouts plus advanced HeaderRules and starts collapsed when inherited', async () => {
    const { wrapper } = await mountSection(inherited, vi.fn() as ApiClient['request'])

    expect(wrapper.findAll('[data-test^="setting-timeout-"]')).toHaveLength(4)
    expect(wrapper.html()).not.toContain('request_log_retention_days')
    expect(
      wrapper.get('[data-test="settings-header-disclosure"]').attributes('aria-expanded'),
    ).toBe('false')
    expect(wrapper.find('[data-test="header-rules-editor"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('seeds ownership, validates positive safe integers, and transport-blocks normalized no-ops', async () => {
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountSection(inherited, request)
    const save = wrapper.get('[data-test="request-forwarding-save"]')

    expect(save.attributes()).toHaveProperty('disabled')
    await save.trigger('click')
    expect(request).not.toHaveBeenCalled()

    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    expect(
      (wrapper.get('[data-test="value-request_timeout"]').element as HTMLInputElement).value,
    ).toBe('600')
    await wrapper.get('[data-test="value-request_timeout"]').setValue('0')
    expect(save.attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="error-request_timeout"]').text()).toContain('positive')

    await wrapper.get('[data-test="override-request_timeout"]').setValue(false)
    await save.trigger('click')
    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps HeaderRules open for override/dirty/error states and masks every Set value', async () => {
    const owned: SettingsDto = {
      ...inherited,
      overrides: ['header_rules'],
      values: {
        ...inherited.values,
        header_rules: { set: { 'X-Custom': 'HEADER_RULE_VALUE_CANARY' }, remove: [] },
      },
    }
    const { wrapper } = await mountSection(owned, vi.fn() as ApiClient['request'])

    expect(
      wrapper.get('[data-test="settings-header-disclosure"]').attributes('aria-expanded'),
    ).toBe('true')
    expect(wrapper.get('[data-test="header-value"]').attributes('type')).toBe('password')
    expect(wrapper.text()).not.toContain('HEADER_RULE_VALUE_CANARY')

    await wrapper.get('[data-test="header-name"]').setValue('X-Duplicate')
    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    await wrapper.findAll('[data-test="header-name"]')[1]!.setValue('x-duplicate')
    expect(wrapper.get('[data-test="header-rules-editor"] [role="alert"]').text()).toContain(
      'duplicate',
    )
    expect(wrapper.get('[data-test="request-forwarding-save"]').attributes()).toHaveProperty(
      'disabled',
    )
    expect(
      wrapper.get('[data-test="settings-header-disclosure"]').attributes('aria-expanded'),
    ).toBe('true')

    await wrapper.get('[data-test="override-header_rules"]').setValue(false)
    expect(wrapper.get('[data-test="request-forwarding-save"]').attributes()).not.toHaveProperty(
      'disabled',
    )
    wrapper.unmount()
  })

  it('disables mutable request controls while a direct save is pending', async () => {
    let resolve!: (value: SettingsDto) => void
    const request = vi.fn(
      () => new Promise<SettingsDto>((done) => (resolve = done)),
    ) as ApiClient['request']
    const { wrapper } = await mountSection(inherited, request)
    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    expect(wrapper.get('[data-test="override-request_timeout"]').attributes()).toHaveProperty(
      'disabled',
    )
    expect(wrapper.get('[data-test="value-request_timeout"]').attributes()).toHaveProperty(
      'disabled',
    )
    resolve(inherited)
    await flushPromises()
    wrapper.unmount()
  })

  it.each(['resolve', 'reject'] as const)(
    'ignores a signal-ignoring late %s after unmount without recreating sensitive cache or feedback',
    async (outcome) => {
      let settle!: (value: SettingsDto) => void
      let fail!: (reason: Error) => void
      const late = new Promise<SettingsDto>((resolve, reject) => {
        settle = resolve
        fail = reject
      })
      const sensitive = {
        ...inherited,
        values: {
          ...inherited.values,
          header_rules: { set: { 'X-Late': 'HEADER_CANARY_LATE' }, remove: [] },
        },
      }
      const request = vi.fn(() => late) as ApiClient['request']
      const { queryClient, wrapper } = await mountSection(inherited, request)
      const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
      await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
      await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
      wrapper.unmount()
      queryClient.removeQueries({ queryKey: controlQueryKeys.settings(), exact: true })

      if (outcome === 'resolve') settle(sensitive)
      else fail(new Error('late ordinary failure'))
      await flushPromises()

      expect(queryClient.getQueryData(controlQueryKeys.settings())).toBeUndefined()
      expect(invalidate).not.toHaveBeenCalled()
      expect(document.body.textContent).not.toContain('Unable to save')
      expect(document.body.textContent).not.toContain('HEADER_CANARY_LATE')
    },
  )

  it('sends only the request section dirty patch with JSON null reset and rebases the response', async () => {
    const owned: SettingsDto = {
      ...inherited,
      overrides: ['request_timeout', 'header_rules'],
      values: {
        ...inherited.values,
        header_rules: { set: { 'X-Custom': 'HEADER_RULE_VALUE_CANARY' }, remove: [] },
      },
    }
    const returned: SettingsDto = {
      ...inherited,
      revision: 5,
      overrides: ['request_timeout'],
      values: { ...inherited.values, request_timeout: 900 },
    }
    const request = vi.fn().mockResolvedValue(returned) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(owned, request)
    const cancel = vi.spyOn(queryClient, 'cancelQueries')
    const setQueryData = vi.spyOn(queryClient, 'setQueryData')

    await wrapper.get('[data-test="value-request_timeout"]').setValue('900')
    await wrapper.get('[data-test="override-header_rules"]').setValue(false)
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/settings', {
      method: 'PUT',
      json: { settings: { request_timeout: 900, header_rules: null } },
      signal: expect.any(AbortSignal),
    })
    expect(cancel).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.settings(),
      exact: true,
    })
    expect(cancel.mock.invocationCallOrder[0]).toBeLessThan(
      setQueryData.mock.invocationCallOrder[0]!,
    )
    expect(queryClient.getQueryData(controlQueryKeys.settings())).toEqual(returned)
    expect(wrapper.get('[data-test="request-forwarding-save"]').attributes()).toHaveProperty(
      'disabled',
    )
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0)
    wrapper.unmount()
  })

  it('does not reset an externally refreshed untouched setting while preserving a local edit', async () => {
    const returned: SettingsDto = {
      ...inherited,
      revision: 6,
      overrides: ['connect_timeout', 'request_timeout'],
      values: { ...inherited.values, connect_timeout: 30, request_timeout: 900 },
    }
    const request = vi.fn().mockResolvedValue(returned) as ApiClient['request']
    const { wrapper } = await mountSection(inherited, request)
    await wrapper.get('[data-test="override-connect_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-connect_timeout"]').setValue('30')
    await wrapper.setProps({
      settings: {
        ...inherited,
        revision: 5,
        overrides: ['request_timeout'],
        values: { ...inherited.values, request_timeout: 900 },
      },
    })
    await flushPromises()

    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/settings', {
      method: 'PUT',
      json: { settings: { connect_timeout: 30 } },
      signal: expect.any(AbortSignal),
    })
    wrapper.unmount()
  })

  it('keeps a newer settings cache and rebases the dirty request draft after a stale response', async () => {
    let resolve!: (value: SettingsDto) => void
    const late = new Promise<SettingsDto>((done) => {
      resolve = done
    })
    const request = vi.fn(() => late) as ApiClient['request']
    const newer: SettingsDto = {
      ...inherited,
      revision: 11,
      overrides: ['header_rules'],
      values: {
        ...inherited.values,
        connect_timeout: 44,
        first_byte_timeout: 211,
        header_rules: {
          set: { 'X-Revision-11': 'HEADER_CANARY_REVISION_11' },
          remove: [],
        },
      },
    }
    const stale: SettingsDto = {
      ...inherited,
      revision: 10,
      overrides: ['header_rules'],
      values: {
        ...inherited.values,
        connect_timeout: 99,
        first_byte_timeout: 299,
        header_rules: {
          set: { 'X-Stale-Only': 'HEADER_CANARY_STALE_RESPONSE' },
          remove: [],
        },
      },
    }
    const { queryClient, wrapper } = await mountSection(inherited, request)
    const cancel = vi.spyOn(queryClient, 'cancelQueries')
    const getQueryData = vi.spyOn(queryClient, 'getQueryData')
    const setQueryData = vi.spyOn(queryClient, 'setQueryData')
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    await wrapper.get('[data-test="override-connect_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-connect_timeout"]').setValue('33')
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')

    queryClient.setQueryData(controlQueryKeys.settings(), newer)
    cancel.mockClear()
    getQueryData.mockClear()
    setQueryData.mockClear()
    invalidate.mockClear()
    resolve(stale)
    await flushPromises()

    expect(cancel).toHaveBeenCalledTimes(1)
    expect(cancel).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.settings(),
      exact: true,
    })
    expect(getQueryData).toHaveBeenCalledWith(controlQueryKeys.settings())
    expect(cancel.mock.invocationCallOrder[0]).toBeLessThan(
      getQueryData.mock.invocationCallOrder[0]!,
    )
    expect(setQueryData).not.toHaveBeenCalled()
    expect(queryClient.getQueryData(controlQueryKeys.settings())).toEqual(newer)
    expect(
      (wrapper.get('[data-test="value-connect_timeout"]').element as HTMLInputElement).value,
    ).toBe('33')
    expect(wrapper.get('[data-test="setting-timeout-first_byte_timeout"]').text()).toContain(
      'Effective value: 211',
    )
    expect(
      wrapper
        .findAll('[data-test="header-name"]')
        .map((field) => (field.element as HTMLInputElement).value),
    ).toEqual(['X-Revision-11'])
    expect(
      wrapper
        .findAll('[data-test="header-value"]')
        .map((field) => (field.element as HTMLInputElement).value),
    ).not.toContain('HEADER_CANARY_STALE_RESPONSE')
    expect(wrapper.get('[data-test="request-forwarding-save"]').attributes()).not.toHaveProperty(
      'disabled',
    )
    expect(wrapper.text()).toContain('Settings saved.')
    expect(invalidate).toHaveBeenCalledTimes(1)
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.groups.details(),
    })
    wrapper.unmount()
  })

  it('refetches exact settings without applying the response when the cache disappears', async () => {
    let resolve!: (value: SettingsDto) => void
    const late = new Promise<SettingsDto>((done) => {
      resolve = done
    })
    const response: SettingsDto = {
      ...inherited,
      revision: 10,
      overrides: ['header_rules'],
      values: {
        ...inherited.values,
        connect_timeout: 99,
        header_rules: {
          set: { 'X-Unconfirmed': 'HEADER_CANARY_UNCONFIRMED_RESPONSE' },
          remove: [],
        },
      },
    }
    const request = vi.fn(() => late) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(inherited, request)
    const cancel = vi.spyOn(queryClient, 'cancelQueries')
    const getQueryData = vi.spyOn(queryClient, 'getQueryData')
    const setQueryData = vi.spyOn(queryClient, 'setQueryData')
    const refetch = vi.spyOn(queryClient, 'refetchQueries')

    await wrapper.get('[data-test="override-connect_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-connect_timeout"]').setValue('33')
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')

    queryClient.removeQueries({ queryKey: controlQueryKeys.settings(), exact: true })
    cancel.mockClear()
    getQueryData.mockClear()
    setQueryData.mockClear()
    refetch.mockClear()
    resolve(response)
    await flushPromises()

    expect(cancel).toHaveBeenCalledTimes(1)
    expect(cancel).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.settings(),
      exact: true,
    })
    expect(getQueryData).toHaveBeenCalledWith(controlQueryKeys.settings())
    expect(cancel.mock.invocationCallOrder[0]).toBeLessThan(
      getQueryData.mock.invocationCallOrder[0]!,
    )
    expect(setQueryData).not.toHaveBeenCalled()
    expect(refetch).toHaveBeenCalledTimes(1)
    expect(refetch).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.settings(),
      exact: true,
    })
    expect(getQueryData.mock.invocationCallOrder[0]).toBeLessThan(
      refetch.mock.invocationCallOrder[0]!,
    )
    expect(queryClient.getQueryData(controlQueryKeys.settings())).toBeUndefined()
    expect(
      (wrapper.get('[data-test="value-connect_timeout"]').element as HTMLInputElement).value,
    ).toBe('33')
    expect(wrapper.find('[data-test="header-rules-editor"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Settings saved.')
    expect(wrapper.get('[data-test="override-connect_timeout"]').attributes()).not.toHaveProperty(
      'disabled',
    )
    wrapper.unmount()
  })

  it('stops after a blocked settings cancellation loses operation ownership', async () => {
    let resolveTransport!: (value: SettingsDto) => void
    const transport = new Promise<SettingsDto>((resolve) => {
      resolveTransport = resolve
    })
    let markCancelStarted!: () => void
    const cancelStarted = new Promise<void>((resolve) => {
      markCancelStarted = resolve
    })
    let releaseCancel!: () => void
    const cancelBarrier = new Promise<void>((resolve) => {
      releaseCancel = resolve
    })
    const sensitive: SettingsDto = {
      ...inherited,
      revision: 10,
      overrides: ['header_rules'],
      values: {
        ...inherited.values,
        header_rules: {
          set: { 'X-Cancel-Barrier': 'HEADER_CANARY_CANCEL_BARRIER' },
          remove: [],
        },
      },
    }
    const request = vi.fn(() => transport) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(inherited, request)
    const cancel = vi.spyOn(queryClient, 'cancelQueries').mockImplementation(async () => {
      markCancelStarted()
      await cancelBarrier
    })
    const getQueryData = vi.spyOn(queryClient, 'getQueryData')
    const setQueryData = vi.spyOn(queryClient, 'setQueryData')
    const refetch = vi.spyOn(queryClient, 'refetchQueries')
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    await wrapper.get('[data-test="override-connect_timeout"]').setValue(true)
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    resolveTransport(sensitive)
    await cancelStarted

    wrapper.unmount()
    queryClient.removeQueries({ queryKey: controlQueryKeys.settings(), exact: true })
    releaseCancel()
    await flushPromises()

    expect(cancel).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.settings(),
      exact: true,
    })
    expect(getQueryData).not.toHaveBeenCalled()
    expect(setQueryData).not.toHaveBeenCalled()
    expect(refetch).not.toHaveBeenCalled()
    expect(invalidate).not.toHaveBeenCalled()
    expect(queryClient.getQueryState(controlQueryKeys.settings())).toBeUndefined()
    expect(document.body.textContent).not.toContain('Settings saved.')
    expect(document.body.textContent).not.toContain('Unable to update')
    expect(document.body.textContent).not.toContain('HEADER_CANARY_CANCEL_BARRIER')
  })
})
