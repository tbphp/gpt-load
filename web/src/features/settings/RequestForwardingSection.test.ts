import { QueryClient } from '@tanstack/vue-query'

import type { ApiClient } from '@/api/client'
import type { SettingsDto } from '@/api/control/settings'
import type { SettingsResource } from '@/app/resources/settings'
import { mountApp } from '@/test/mount-app'

import RequestForwardingSection from './RequestForwardingSection.vue'
import { createSettingsDraft, type SettingsDraft } from './settings-patch'
import type { SettingsMergeConflict } from './settings-response'

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

function resource(settings: SettingsDto): SettingsResource {
  return { settings, settings_etag: `sha256-${'a'.repeat(64)}` }
}

async function mountSection(
  options: {
    base?: SettingsResource
    draft?: SettingsDraft
    disabled?: boolean
    conflicts?: SettingsMergeConflict[]
  } = {},
) {
  const request = vi.fn() as ApiClient['request']
  const base = options.base ?? resource(inherited)
  const mounted = await mountApp(RequestForwardingSection, {
    api: { request },
    queryClient: new QueryClient({ defaultOptions: { queries: { retry: false } } }),
    locale: 'en-US',
    path: '/settings',
    mounting: {
      props: {
        base,
        draft: options.draft ?? createSettingsDraft(base.settings),
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

describe('RequestForwardingSection', () => {
  it('emits exact controlled draft changes without owning an API mutation', async () => {
    const { request, wrapper } = await mountSection()

    expect(wrapper.findAll('[data-test^="setting-timeout-"]')).toHaveLength(4)
    expect(wrapper.find('[data-test="request-forwarding-save"]').exists()).toBe(false)

    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    const ownership = latestChange(wrapper)
    expect(ownership.key).toBe('request_timeout')
    expect(ownership.draft.overrides.has('request_timeout')).toBe(true)
    expect(ownership.draft.values.request_timeout).toBe(600)

    await wrapper.setProps({ draft: ownership.draft })
    await wrapper.get('[data-test="value-request_timeout"]').setValue('900')
    const value = latestChange(wrapper)
    expect(value.key).toBe('request_timeout')
    expect(value.draft.values.request_timeout).toBe(900)
    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps validation field-local, exposes stable targets, and honors page locking', async () => {
    const draft = createSettingsDraft(inherited)
    draft.overrides.add('request_timeout')
    draft.values.request_timeout = 0
    const { wrapper } = await mountSection({ draft, disabled: true })

    const input = wrapper.get<HTMLInputElement>('[data-test="value-request_timeout"]')
    expect(input.attributes('id')).toBe('settings-value-request_timeout')
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('[data-test="error-request_timeout"]').text()).toContain('positive')
    expect(input.attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-test="override-request_timeout"]').attributes()).toHaveProperty(
      'disabled',
    )
    wrapper.unmount()
  })

  it('keeps HeaderRules masked and emits their draft without making a request', async () => {
    const owned: SettingsDto = {
      ...inherited,
      values: {
        ...inherited.values,
        header_rules: { set: { 'X-Secret': 'HEADER_VALUE_CANARY' }, remove: [] },
      },
      overrides: ['header_rules'],
    }
    const { request, wrapper } = await mountSection({
      base: resource(owned),
      draft: createSettingsDraft(owned),
    })

    expect(wrapper.get('[data-test="header-value"]').attributes('type')).toBe('password')
    expect(wrapper.text()).not.toContain('HEADER_VALUE_CANARY')
    await wrapper.get('[data-test="header-value"]').setValue('NEXT_HEADER_CANARY')

    const change = latestChange(wrapper)
    expect(change.key).toBe('header_rules')
    expect(change.draft.values.header_rules.set['X-Secret']).toBe('NEXT_HEADER_CANARY')
    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('renders safe conflict summaries and emits exact mine/latest choices', async () => {
    const conflict: SettingsMergeConflict = {
      key: 'header_rules',
      mine: {
        is_override: true,
        normalized_value: { set: { 'x-secret': 'MINE_CANARY' }, remove: [] },
      },
      latest: {
        is_override: true,
        normalized_value: { set: { 'x-secret': 'LATEST_CANARY' }, remove: ['x-remove'] },
      },
    }
    const { wrapper } = await mountSection({ conflicts: [conflict] })

    const summary = wrapper.get('[data-test="settings-conflicts"]')
    expect(summary.text()).toContain('Advanced HeaderRules')
    expect(summary.text()).toContain('1 Set and 0 Remove rules')
    expect(summary.text()).toContain('1 Set and 1 Remove rules')
    expect(summary.text()).not.toContain('MINE_CANARY')
    expect(summary.text()).not.toContain('LATEST_CANARY')

    await wrapper.get('[data-test="settings-conflict-mine-header_rules"]').trigger('click')
    await wrapper.get('[data-test="settings-conflict-latest-header_rules"]').trigger('click')
    expect(wrapper.emitted('chooseMine')).toEqual([['header_rules']])
    expect(wrapper.emitted('chooseLatest')).toEqual([['header_rules']])
    wrapper.unmount()
  })
})
