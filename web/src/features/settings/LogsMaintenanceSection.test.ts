import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import type { SettingsDto } from '@/api/control/settings'
import { ApiError, NetworkError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import type { SettingsResource } from '@/app/resources/settings'
import { apiWithResponseMetadata, testSettingsETags } from '@/test/api-response'
import { mountApp } from '@/test/mount-app'

import LogsMaintenanceSection from './LogsMaintenanceSection.vue'

const base: SettingsDto = {
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

async function mountSection(
  settings: SettingsDto,
  request: ApiClient['request'],
  attachTo?: Element,
  tokenFor?: Parameters<typeof apiWithResponseMetadata>[1],
) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const initial = resource(settings)
  queryClient.setQueryData(controlQueryKeys.settings('en-US'), initial)
  const mounted = await mountApp(LogsMaintenanceSection, {
    api: apiWithResponseMetadata(request, tokenFor),
    queryClient,
    locale: 'en-US',
    path: '/settings',
    mounting: { props: { resource: initial }, attachTo },
  })
  return { ...mounted, queryClient }
}

describe('LogsMaintenanceSection', () => {
  it('enforces retention days from 1 through 365 and suppresses empty saves', async () => {
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountSection(base, request)
    const save = wrapper.get('[data-test="logs-maintenance-save"]')

    expect(save.attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-test="override-request_log_retention_days"]').setValue(true)
    for (const value of ['0', '366']) {
      await wrapper.get('[data-test="value-request_log_retention_days"]').setValue(value)
      expect(save.attributes()).toHaveProperty('disabled')
    }
    for (const value of ['1', '365']) {
      await wrapper.get('[data-test="value-request_log_retention_days"]').setValue(value)
      expect(save.attributes()).not.toHaveProperty('disabled')
    }
    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('disables owned retention controls while an OCC write is pending', async () => {
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

  it('sends null with quoted If-Match and rebases only this section', async () => {
    const owned: SettingsDto = { ...base, overrides: ['request_log_retention_days'] }
    const request = vi.fn().mockResolvedValue(base) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(owned, request)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    await wrapper.get('[data-test="override-request_log_retention_days"]').setValue(false)
    await wrapper.get('[data-test="logs-maintenance-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/settings', {
      method: 'PUT',
      headers: { 'If-Match': `"${testSettingsETags.get}"` },
      json: { settings: { request_log_retention_days: null } },
      signal: expect.any(AbortSignal),
    })
    expect(queryClient.getQueryData(controlQueryKeys.settings('en-US'))).toEqual(
      resource(base, testSettingsETags.put),
    )
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.groups.details(),
    })
    wrapper.unmount()
  })

  it('refetches when response and cache ETags are unrelated', async () => {
    let resolve!: (value: SettingsDto) => void
    const late = new Promise<SettingsDto>((done) => {
      resolve = done
    })
    const request = vi.fn(() => late) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(base, request)
    const refetch = vi.spyOn(queryClient, 'refetchQueries')
    const unrelated = resource(
      { ...base, values: { ...base.values, request_log_retention_days: 30 } },
      `sha256-${'c'.repeat(64)}`,
    )

    await wrapper.get('[data-test="override-request_log_retention_days"]').setValue(true)
    await wrapper.get('[data-test="value-request_log_retention_days"]').setValue('8')
    await wrapper.get('[data-test="logs-maintenance-save"]').trigger('click')
    queryClient.setQueryData(controlQueryKeys.settings('en-US'), unrelated)
    resolve({ ...base, values: { ...base.values, request_log_retention_days: 99 } })
    await flushPromises()

    expect(refetch).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.settings('en-US'),
      exact: true,
    })
    expect(queryClient.getQueryData(controlQueryKeys.settings('en-US'))).toEqual(unrelated)
    expect(wrapper.text()).not.toContain('Settings saved.')
    wrapper.unmount()
  })

  it('blocks a retention conflict until mine/latest is explicitly chosen', async () => {
    const owned: SettingsDto = {
      ...base,
      values: { ...base.values, request_log_retention_days: 7 },
      overrides: ['request_log_retention_days'],
    }
    const latest: SettingsDto = {
      ...owned,
      values: { ...owned.values, request_log_retention_days: 30 },
    }
    const latestToken = `sha256-${'d'.repeat(64)}`
    const request = vi.fn().mockRejectedValue(
      new ApiError(412, 'SETTINGS_VERSION_CONFLICT', 'conflict', {
        settings: latest,
        settings_etag: latestToken,
      }),
    ) as ApiClient['request']
    const { wrapper } = await mountSection(owned, request)

    await wrapper.get('[data-test="value-request_log_retention_days"]').setValue('8')
    await wrapper.get('[data-test="logs-maintenance-save"]').trigger('click')
    await flushPromises()

    const conflict = wrapper.get('[data-test="settings-conflicts"]')
    expect(conflict.text()).toContain('Mine: 8')
    expect(conflict.text()).toContain('Latest: 30')
    expect(wrapper.get('[data-test="logs-maintenance-save"]').attributes()).toHaveProperty(
      'disabled',
    )
    const useMine = conflict.findAll('button').find((button) => button.text() === 'Use mine')
    await useMine?.trigger('click')
    expect(wrapper.find('[data-test="settings-conflicts"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="logs-maintenance-save"]').attributes()).not.toHaveProperty(
      'disabled',
    )
    wrapper.unmount()
  })

  it('blocks a same-field external refresh before retention can overwrite it', async () => {
    const owned: SettingsDto = {
      ...base,
      values: { ...base.values, request_log_retention_days: 7 },
      overrides: ['request_log_retention_days'],
    }
    const { wrapper } = await mountSection(owned, vi.fn() as ApiClient['request'])
    await wrapper.get('[data-test="value-request_log_retention_days"]').setValue('8')
    await wrapper.setProps({
      resource: resource(
        {
          ...owned,
          values: { ...owned.values, request_log_retention_days: 30 },
        },
        `sha256-${'c'.repeat(64)}`,
      ),
    })
    await flushPromises()

    expect(wrapper.get('[data-test="settings-conflicts"]').text()).toContain('Latest: 30')
    expect(wrapper.get('[data-test="logs-maintenance-save"]').attributes()).toHaveProperty(
      'disabled',
    )
    wrapper.unmount()
  })

  it('keeps a network-unknown retention write indeterminate instead of reporting failure', async () => {
    const request = vi
      .fn()
      .mockRejectedValueOnce(new NetworkError())
      .mockResolvedValueOnce(base) as ApiClient['request']
    const { wrapper } = await mountSection(base, request)

    await wrapper.get('[data-test="override-request_log_retention_days"]').setValue(true)
    await wrapper.get('[data-test="value-request_log_retention_days"]').setValue('8')
    await wrapper.get('[data-test="logs-maintenance-save"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="settings-indeterminate"]').text()).toContain(
      'could not be confirmed',
    )
    expect(wrapper.text()).not.toContain('Unable to update')
    wrapper.unmount()
  })

  it('ignores a late response after unmount without recreating Settings cache', async () => {
    let resolve!: (value: SettingsDto) => void
    const late = new Promise<SettingsDto>((done) => {
      resolve = done
    })
    const request = vi.fn(() => late) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(base, request, document.body)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    await wrapper.get('[data-test="override-request_log_retention_days"]').setValue(true)
    await wrapper.get('[data-test="logs-maintenance-save"]').trigger('click')
    wrapper.unmount()
    queryClient.removeQueries({ queryKey: controlQueryKeys.settings('en-US'), exact: true })
    resolve(base)
    await flushPromises()

    expect(queryClient.getQueryData(controlQueryKeys.settings('en-US'))).toBeUndefined()
    expect(invalidate).not.toHaveBeenCalled()
  })
})
