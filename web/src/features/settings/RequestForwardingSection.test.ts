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

    await wrapper.get('[data-test="value-request_timeout"]').setValue('900')
    await wrapper.get('[data-test="override-header_rules"]').setValue(false)
    await wrapper.get('[data-test="request-forwarding-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/settings', {
      method: 'PUT',
      json: { settings: { request_timeout: 900, header_rules: null } },
      signal: expect.any(AbortSignal),
    })
    expect(queryClient.getQueryData(controlQueryKeys.settings())).toEqual(returned)
    expect(wrapper.get('[data-test="request-forwarding-save"]').attributes()).toHaveProperty(
      'disabled',
    )
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0)
    wrapper.unmount()
  })
})
