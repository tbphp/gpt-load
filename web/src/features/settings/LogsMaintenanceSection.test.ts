import { QueryClient } from '@tanstack/vue-query'

import type { ApiClient } from '@/api/client'
import type { SettingsDto } from '@/app/resources/settings'
import type { SettingsResource } from '@/app/resources/settings'
import { mountApp } from '@/test/mount-app'

import LogsMaintenanceSection from './LogsMaintenanceSection.vue'
import { createSettingsDraft, type SettingsDraft } from './settings-patch'
import type { SettingsMergeConflict } from './settings-response'

const settings: SettingsDto = {
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
const base: SettingsResource = {
  settings,
  settings_etag: `sha256-${'a'.repeat(64)}`,
}

async function mountSection(
  options: {
    draft?: SettingsDraft
    disabled?: boolean
    conflicts?: SettingsMergeConflict[]
  } = {},
) {
  const request = vi.fn() as ApiClient['request']
  const mounted = await mountApp(LogsMaintenanceSection, {
    api: { request },
    queryClient: new QueryClient({ defaultOptions: { queries: { retry: false } } }),
    locale: 'en-US',
    path: '/settings',
    mounting: {
      props: {
        base,
        draft: options.draft ?? createSettingsDraft(settings),
        disabled: options.disabled ?? false,
        conflicts: options.conflicts ?? [],
      },
      attachTo: document.body,
    },
  })
  return { ...mounted, request }
}

function latestChange(wrapper: Awaited<ReturnType<typeof mountSection>>['wrapper']) {
  const event = wrapper.emitted('change')?.at(-1)?.[0]
  if (!event) throw new Error('missing Settings draft change')
  return event as { key: string; draft: SettingsDraft }
}

describe('LogsMaintenanceSection', () => {
  it('emits ownership and value changes without owning an API mutation', async () => {
    const { request, wrapper } = await mountSection()

    expect(wrapper.find('[data-test="logs-maintenance-save"]').exists()).toBe(false)
    await wrapper.get('[data-test="override-request_log_retention_days"]').setValue(true)
    const ownership = latestChange(wrapper)
    expect(ownership.key).toBe('request_log_retention_days')
    expect(ownership.draft.overrides.has('request_log_retention_days')).toBe(true)

    await wrapper.setProps({ draft: ownership.draft })
    await wrapper.get('[data-test="value-request_log_retention_days"]').setValue('30')
    const value = latestChange(wrapper)
    expect(value.key).toBe('request_log_retention_days')
    expect(value.draft.values.request_log_retention_days).toBe(30)
    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps retention validation local with a stable target and page lock', async () => {
    const draft = createSettingsDraft(settings)
    draft.overrides.add('request_log_retention_days')
    draft.values.request_log_retention_days = 366
    const { wrapper } = await mountSection({ draft, disabled: true })

    const input = wrapper.get<HTMLInputElement>('[data-test="value-request_log_retention_days"]')
    expect(input.attributes('id')).toBe('settings-value-request_log_retention_days')
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('[data-test="error-request_log_retention_days"]').text()).toContain('365')
    expect(input.attributes()).toHaveProperty('disabled')
    expect(
      wrapper.get('[data-test="override-request_log_retention_days"]').attributes(),
    ).toHaveProperty('disabled')
    wrapper.unmount()
  })

  it('emits the exact conflict choice and never mutates independently', async () => {
    const conflict: SettingsMergeConflict = {
      key: 'request_log_retention_days',
      mine: { is_override: true, normalized_value: 30 },
      latest: { is_override: true, normalized_value: 60 },
    }
    const { wrapper } = await mountSection({ conflicts: [conflict] })

    expect(wrapper.get('[data-test="settings-conflicts"]').text()).toContain('Mine: 30')
    expect(wrapper.get('[data-test="settings-conflicts"]').text()).toContain('Latest: 60')
    await wrapper
      .get('[data-test="settings-conflict-mine-request_log_retention_days"]')
      .trigger('click')
    await wrapper
      .get('[data-test="settings-conflict-latest-request_log_retention_days"]')
      .trigger('click')

    expect(wrapper.emitted('chooseMine')).toEqual([['request_log_retention_days']])
    expect(wrapper.emitted('chooseLatest')).toEqual([['request_log_retention_days']])
    wrapper.unmount()
  })
})
