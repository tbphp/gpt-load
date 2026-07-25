import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import type { SettingsDto } from '@/api/control/settings'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import LogsMaintenanceSection from './LogsMaintenanceSection.vue'

const base: SettingsDto = {
  revision: 2,
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

async function mountSection(settings: SettingsDto, request: ApiClient['request']) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  queryClient.setQueryData(controlQueryKeys.settings(), settings)
  const mounted = await mountApp(LogsMaintenanceSection, {
    api: { request },
    queryClient,
    locale: 'en-US',
    path: '/settings',
    mounting: { props: { settings } },
  })
  return { ...mounted, queryClient }
}

describe('LogsMaintenanceSection', () => {
  it('enforces retention days from 1 through 365 and suppresses empty saves', async () => {
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountSection(base, request)
    const save = wrapper.get('[data-test="logs-maintenance-save"]')

    expect(save.attributes()).toHaveProperty('disabled')
    await save.trigger('click')
    expect(request).not.toHaveBeenCalled()

    await wrapper.get('[data-test="override-request_log_retention_days"]').setValue(true)
    for (const value of ['0', '366']) {
      await wrapper.get('[data-test="value-request_log_retention_days"]').setValue(value)
      expect(save.attributes()).toHaveProperty('disabled')
      expect(wrapper.get('[data-test="error-request_log_retention_days"]').text()).toContain(
        '1 and 365',
      )
    }
    for (const value of ['1', '365']) {
      await wrapper.get('[data-test="value-request_log_retention_days"]').setValue(value)
      expect(save.attributes()).not.toHaveProperty('disabled')
    }
    wrapper.unmount()
  })

  it('disables owned retention checkbox and numeric value while save is pending', async () => {
    const owned: SettingsDto = { ...base, overrides: ['request_log_retention_days'] }
    const request = vi.fn(() => new Promise(() => {})) as ApiClient['request']
    const { wrapper } = await mountSection(owned, request)
    await wrapper.get('[data-test="value-request_log_retention_days"]').setValue('8')
    await wrapper.get('[data-test="logs-maintenance-save"]').trigger('click')

    expect(
      wrapper.get('[data-test="override-request_log_retention_days"]').attributes(),
    ).toHaveProperty('disabled')
    expect(
      wrapper.get('[data-test="value-request_log_retention_days"]').attributes(),
    ).toHaveProperty('disabled')
    wrapper.unmount()
  })

  it.each(['resolve', 'reject'] as const)(
    'ignores a signal-ignoring late %s after unmount without recreating settings cache',
    async (outcome) => {
      let settle!: (value: SettingsDto) => void
      let fail!: (reason: Error) => void
      const late = new Promise<SettingsDto>((resolve, reject) => {
        settle = resolve
        fail = reject
      })
      const sensitive = {
        ...base,
        values: {
          ...base.values,
          header_rules: { set: { 'X-Late': 'HEADER_CANARY_LATE' }, remove: [] },
        },
      }
      const request = vi.fn(() => late) as ApiClient['request']
      const { queryClient, wrapper } = await mountSection(base, request)
      const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
      await wrapper.get('[data-test="override-request_log_retention_days"]').setValue(true)
      await wrapper.get('[data-test="logs-maintenance-save"]').trigger('click')
      wrapper.unmount()
      queryClient.removeQueries({ queryKey: controlQueryKeys.settings(), exact: true })

      if (outcome === 'resolve') settle(sensitive)
      else fail(new Error('late ordinary failure'))
      await flushPromises()

      expect(queryClient.getQueryData(controlQueryKeys.settings())).toBeUndefined()
      expect(invalidate).not.toHaveBeenCalled()
      expect(document.body.textContent).not.toContain('HEADER_CANARY_LATE')
    },
  )

  it('sends JSON null when resetting retention and rebases only this section', async () => {
    const owned: SettingsDto = { ...base, overrides: ['request_log_retention_days'] }
    const returned = { ...base, revision: 3 }
    const request = vi.fn().mockResolvedValue(returned) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(owned, request)

    await wrapper.get('[data-test="override-request_log_retention_days"]').setValue(false)
    await wrapper.get('[data-test="logs-maintenance-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/settings', {
      method: 'PUT',
      json: { settings: { request_log_retention_days: null } },
      signal: expect.any(AbortSignal),
    })
    expect(queryClient.getQueryData(controlQueryKeys.settings())).toEqual(returned)
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0)
    wrapper.unmount()
  })
})
