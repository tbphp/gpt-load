import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import SystemInfoSection from './SystemInfoSection.vue'

const rawInfo = {
  version: '2.0.0',
  deployment: {
    instance_mode: 'single',
    database: 'sqlite',
    distribution: 'single_binary',
  },
  data_dir: '/data/gpt-load',
  auth_key: {
    source: 'key_file',
    path: '/data/gpt-load/auth.key',
    value: 'AUTH_KEY_CONTENT_CANARY',
  },
  encryption: {
    enabled: true,
    source: 'environment',
    path: null,
    key: 'ENCRYPTION_KEY_CONTENT_CANARY',
  },
  database_dsn: 'DATABASE_DSN_CANARY',
}

async function mountSection(request: ApiClient['request']) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const mounted = await mountApp(SystemInfoSection, {
    api: { request },
    queryClient,
    locale: 'en-US',
    path: '/settings',
  })
  await flushPromises()
  return { ...mounted, queryClient }
}

describe('SystemInfoSection', () => {
  it('renders only fixed allowlisted metadata and exposes copy only for non-null paths', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const request = vi.fn().mockResolvedValue(rawInfo) as ApiClient['request']
    const { queryClient, wrapper } = await mountSection(request)

    expect(wrapper.text()).toContain('2.0.0')
    expect(wrapper.text()).toContain('Single instance')
    expect(wrapper.text()).toContain('SQLite')
    expect(wrapper.text()).toContain('Single binary')
    expect(wrapper.text()).toContain('/data/gpt-load/auth.key')
    expect(wrapper.text()).not.toMatch(
      /AUTH_KEY_CONTENT_CANARY|ENCRYPTION_KEY_CONTENT_CANARY|DATABASE_DSN_CANARY/,
    )
    expect(JSON.stringify(queryClient.getQueryData(controlQueryKeys.systemInfo()))).not.toMatch(
      /CANARY/,
    )

    expect(wrapper.find('[data-test="copy-auth-key-path"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="copy-encryption-path"]').exists()).toBe(false)
    await wrapper.get('[data-test="copy-auth-key-path"] button').trigger('click')
    expect(writeText).toHaveBeenCalledWith('/data/gpt-load/auth.key')
    expect(writeText).not.toHaveBeenCalledWith(expect.stringContaining('CANARY'))
    wrapper.unmount()
  })

  it('shows a generic error without reflecting malformed response details', async () => {
    const request = vi.fn().mockResolvedValue({
      ...rawInfo,
      deployment: { ...rawInfo.deployment, database: 'SECRET_DB_CANARY' },
    }) as ApiClient['request']
    const { wrapper } = await mountSection(request)

    expect(wrapper.text()).toContain('Unable to load system information')
    expect(wrapper.html()).not.toContain('SECRET_DB_CANARY')
    wrapper.unmount()
  })
})
