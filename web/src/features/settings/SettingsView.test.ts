import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { createMemoryHistory } from 'vue-router'

import type { ApiClient, ApiPath, ApiRequestOptions } from '@/api/client'
import type { SettingsDto } from '@/app/resources/settings'
import { NetworkError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { createAppRouter } from '@/app/router'
import { createThemeController, themeControllerKey } from '@/features/preferences/theme'
import { mountApp } from '@/test/mount-app'
import { apiWithResponseMetadata, testSettingsETags } from '@/test/api-response'

import SettingsView from './SettingsView.vue'

const headerCanary = 'HEADER_RULE_CACHE_CANARY_8f31'
const base: SettingsDto = {
  values: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: { 'X-Global': headerCanary }, remove: [] },
    inject_usage_options: true,
    request_log_retention_days: 7,
  },
  overrides: [],
}
const saved: SettingsDto = {
  values: {
    ...base.values,
    request_timeout: 900,
    request_log_retention_days: 30,
  },
  overrides: ['request_timeout', 'request_log_retention_days'],
}
const systemInfo = {
  version: '2.0.0',
  deployment: {
    instance_mode: 'single',
    database: 'sqlite',
    distribution: 'single_binary',
  },
  data_dir: '/data',
  auth_key: { source: 'environment', path: null },
  encryption: { enabled: true, source: 'environment', path: null },
}

type SettingsHandler = (
  options?: ApiRequestOptions,
  getCount?: number,
) => unknown | Promise<unknown>

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountView(
  settingsHandler: SettingsHandler = (options) => (options?.method === 'PUT' ? saved : base),
  tokenFor?: (path: ApiPath, options: ApiRequestOptions | undefined, data: unknown) => string,
) {
  const client = queryClient()
  client.setQueryData(controlQueryKeys.groups.list(), [{ id: 7 }])
  client.setQueryData(controlQueryKeys.groups.detail(7), { id: 7, effective_config: {} })
  client.setQueryData(controlQueryKeys.groups.detail(8), { id: 8, effective_config: {} })
  client.setQueryData(controlQueryKeys.groups.keys(7), [{ id: 1 }])
  client.setQueryData(controlQueryKeys.health(), { observed_at: 'now' })
  client.setQueryData(controlQueryKeys.accessKeys.list(), [{ id: 9 }])
  let getCount = 0
  const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
    if (path === '/api/settings') {
      if (options?.method === undefined) getCount += 1
      return settingsHandler(options, getCount)
    }
    if (path === '/api/system/info') return systemInfo
    throw new Error(`unexpected request ${path}`)
  })
  const theme = createThemeController({
    documentElement: document.documentElement,
    storage: localStorage,
    matchMedia: window.matchMedia.bind(window),
  })
  const Host = defineComponent({ template: '<RouterView />' })
  const mounted = await mountApp(Host, {
    api: apiWithResponseMetadata(requestMock as ApiClient['request'], tokenFor),
    queryClient: client,
    locale: 'en-US',
    path: '/settings',
    mounting: {
      attachTo: document.body,
      global: { provide: { [themeControllerKey as symbol]: theme } },
    },
  })
  await flushPromises()
  return { ...mounted, queryClient: client, requestMock, theme }
}

async function editBothSections(wrapper: Awaited<ReturnType<typeof mountView>>['wrapper']) {
  await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
  await wrapper.get('[data-test="value-request_timeout"]').setValue('900')
  await wrapper.get('[data-test="override-request_log_retention_days"]').setValue(true)
  await wrapper.get('[data-test="value-request_log_retention_days"]').setValue('30')
}

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('SettingsView', () => {
  it('keeps the secondary Model prices entry and gcTime-zero localized Settings boundary', async () => {
    const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
    const routeComponent = router.resolve('/settings').matched.at(-1)?.components?.default
    expect(typeof routeComponent).toBe('function')
    expect(await (routeComponent as () => Promise<unknown>)()).toBe(SettingsView)

    const { queryClient: client, theme, wrapper } = await mountView()
    expect(wrapper.get('h1').text()).toBe('Settings')
    expect(
      wrapper.get<HTMLAnchorElement>('[data-test="model-prices-entry"]').attributes('href'),
    ).toBe('/settings/model-prices')
    expect(
      client.getQueryCache().find({ queryKey: controlQueryKeys.settings('en-US') })?.options.gcTime,
    ).toBe(0)
    expect(wrapper.html()).not.toContain(headerCanary)

    wrapper.unmount()
    await flushPromises()
    expect(client.getQueryData(controlQueryKeys.settings('en-US'))).toBeUndefined()
    theme.dispose()
  })

  it('saves both sections with one action, one If-Match, and one confirmed timestamp', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-28T14:00:00.000Z'))
    const { queryClient: client, requestMock, theme, wrapper } = await mountView()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    await editBothSections(wrapper)

    expect(
      wrapper.findAll('button').filter((button) => button.text().includes('Save changes')),
    ).toHaveLength(1)
    await wrapper.get('[data-test="settings-save-all"]').trigger('click')
    await flushPromises()

    expect(requestMock).toHaveBeenCalledWith('/api/settings', {
      method: 'PUT',
      headers: { 'If-Match': `"${testSettingsETags.get}"` },
      json: {
        settings: {
          request_timeout: 900,
          request_log_retention_days: 30,
        },
      },
      signal: expect.any(AbortSignal),
    })
    expect(client.getQueryData(controlQueryKeys.settings('en-US'))).toEqual({
      settings: saved,
      settings_etag: testSettingsETags.put,
    })
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.details() },
    ])
    expect(wrapper.get('[data-test="settings-saved-at"]').attributes('datetime')).toBe(
      '2026-07-28T14:00:00.000Z',
    )
    expect(wrapper.find('[data-test="settings-dirty-summary"]').exists()).toBe(false)
    wrapper.unmount()
    theme.dispose()
  })

  it('keeps the complete dirty draft across section anchors and discards it atomically', async () => {
    const { theme, wrapper } = await mountView()
    await editBothSections(wrapper)

    await wrapper.get('[data-test="settings-nav-logs"]').trigger('click')
    expect(document.activeElement).toBe(wrapper.get('#settings-logs-maintenance').element)
    expect(wrapper.get('[data-test="settings-dirty-summary"]').text()).toContain('2')
    expect(wrapper.get<HTMLInputElement>('[data-test="value-request_timeout"]').element.value).toBe(
      '900',
    )
    expect(
      wrapper.get<HTMLInputElement>('[data-test="value-request_log_retention_days"]').element.value,
    ).toBe('30')

    await wrapper.get('[data-test="settings-discard"]').trigger('click')
    expect(wrapper.find('[data-test="settings-dirty-summary"]').exists()).toBe(false)
    expect(
      wrapper.get<HTMLInputElement>('[data-test="override-request_timeout"]').element.checked,
    ).toBe(false)
    expect(
      wrapper.get<HTMLInputElement>('[data-test="override-request_log_retention_days"]').element
        .checked,
    ).toBe(false)
    wrapper.unmount()
    theme.dispose()
  })

  it('links the validation summary to and focuses the first invalid field', async () => {
    const { theme, wrapper } = await mountView()
    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-request_timeout"]').setValue('0')

    const errorLink = wrapper.get('[data-test="settings-error-link-request_timeout"]')
    expect(errorLink.attributes('href')).toBe('#settings-value-request_timeout')
    await errorLink.trigger('click')
    expect(document.activeElement).toBe(wrapper.get('[data-test="value-request_timeout"]').element)
    expect(wrapper.get('[data-test="settings-save-all"]').attributes()).toHaveProperty('disabled')
    wrapper.unmount()
    theme.dispose()
  })

  it('blocks Save all while the HeaderRules editor contains duplicate Set rows', async () => {
    const { requestMock, theme, wrapper } = await mountView()
    await wrapper.get('[data-test="settings-header-disclosure"]').trigger('click')
    await wrapper.get('[data-test="override-header_rules"]').setValue(true)
    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    const names = wrapper.findAll('[data-test="header-name"]')
    await names[0]!.setValue('X-Api-Key')
    await names[1]!.setValue('X-Api-Key')

    expect(
      wrapper.findAll('[role="alert"]').some((feedback) => feedback.text().includes('duplicate')),
    ).toBe(true)
    expect(wrapper.get('[data-test="settings-error-link-header_rules"]').attributes('href')).toBe(
      '#settings-header-rules',
    )
    const save = wrapper.get('[data-test="settings-save-all"]')
    expect(save.attributes()).toHaveProperty('disabled')
    await save.trigger('click')
    await flushPromises()
    expect(
      requestMock.mock.calls.filter(
        ([path, options]) => path === '/api/settings' && options?.method === 'PUT',
      ),
    ).toHaveLength(0)
    wrapper.unmount()
    theme.dispose()
  })

  it('prompts while dirty and blocks busy navigation without a discard prompt', async () => {
    let resolveSave!: () => void
    const pending = new Promise<SettingsDto>((resolve) => {
      resolveSave = () => resolve(saved)
    })
    const { router, theme, wrapper } = await mountView((options) =>
      options?.method === 'PUT' ? pending : base,
    )
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    await wrapper.get('[data-test="value-request_timeout"]').setValue('900')

    await router.push('/groups')
    expect(router.currentRoute.value.path).toBe('/settings')
    expect(confirm).toHaveBeenCalledOnce()

    await wrapper.get('[data-test="settings-save-all"]').trigger('click')
    await flushPromises()
    await router.push('/groups')
    expect(router.currentRoute.value.path).toBe('/settings')
    expect(confirm).toHaveBeenCalledOnce()

    resolveSave()
    await flushPromises()
    wrapper.unmount()
    theme.dispose()
  })

  it('preserves both dirty sections across a localized ETag refresh', async () => {
    const localizedETag = `sha256-${'c'.repeat(64)}`
    let representationCount = 0
    const { appI18n, requestMock, theme, wrapper } = await mountView(
      (options) => (options?.method === 'PUT' ? saved : base),
      (_path, options) => {
        if (options?.method === 'PUT') return testSettingsETags.put
        representationCount += 1
        return representationCount === 1 ? testSettingsETags.get : localizedETag
      },
    )

    await editBothSections(wrapper)
    await appI18n.setLocale('ja-JP')
    await flushPromises()
    await wrapper.get('[data-test="settings-save-all"]').trigger('click')
    await flushPromises()

    expect(requestMock).toHaveBeenLastCalledWith('/api/settings', {
      method: 'PUT',
      headers: { 'If-Match': `"${localizedETag}"` },
      json: {
        settings: {
          request_timeout: 900,
          request_log_retention_days: 30,
        },
      },
      signal: expect.any(AbortSignal),
    })
    wrapper.unmount()
    theme.dispose()
  })

  it('reconciles a network-unknown combined save with GET and never resends PUT', async () => {
    let getCount = 0
    const { requestMock, theme, wrapper } = await mountView(
      (options) => {
        if (options?.method === 'PUT') throw new NetworkError()
        getCount += 1
        return getCount === 1 ? base : saved
      },
      (_path, options) =>
        options?.method === 'PUT'
          ? testSettingsETags.put
          : getCount <= 1
            ? testSettingsETags.get
            : `sha256-${'d'.repeat(64)}`,
    )
    await editBothSections(wrapper)
    await wrapper.get('[data-test="settings-save-all"]').trigger('click')
    await flushPromises()

    expect(
      requestMock.mock.calls.filter(
        ([path, options]) => path === '/api/settings' && options?.method === 'PUT',
      ),
    ).toHaveLength(1)
    expect(wrapper.find('[data-test="settings-indeterminate"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="settings-saved-at"]')).toBeDefined()
    wrapper.unmount()
    theme.dispose()
  })
})
