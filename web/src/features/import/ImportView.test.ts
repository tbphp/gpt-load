import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import { createAppRouter } from '@/app/router'
import type { ImportRecoveryService } from '@/features/import/import-recovery'
import { importRecoveryKey } from '@/features/import/import-recovery'
import { createAppI18n } from '@/i18n'

import ImportView from './ImportView.vue'

function recovery(overrides: Partial<ImportRecoveryService> = {}): ImportRecoveryService {
  return {
    register: () => () => {},
    captureForUnauthorized: () => 'no-active-draft',
    consume: () => null,
    clear: () => {},
    sweep: () => {},
    dispose: () => {},
    ...overrides,
  }
}

async function mountView(path: string, service = recovery()) {
  const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const wrapper = mount(ImportView, {
    global: {
      plugins: [router, createAppI18n(undefined, 'en-US').plugin],
      provide: { [importRecoveryKey as symbol]: service },
      stubs: {
        NewGroupImport: {
          props: ['initialDraft'],
          template: '<div data-test="new-import">{{ initialDraft ? "recovered" : "new" }}</div>',
        },
      },
    },
  })
  return { router, wrapper }
}

describe('ImportView', () => {
  it.each(['/import', '/import?mode=new'])('renders new-Group mode for %s', async (path) => {
    const { wrapper } = await mountView(path)
    expect(wrapper.get('[data-test="new-import"]').text()).toBe('new')
  })

  it('consumes a 401 recovery immediately even when Login preserved an Import query variant', async () => {
    const consume = vi.fn(() => ({
      mode: 'new' as const,
      step: 1 as const,
      preset_id: 'custom' as const,
      name: '',
      upstream_url: '',
      protocols: [],
      keys: 'canary',
      header_rules: { set: {}, remove: [] },
      models: [],
    }))
    const { wrapper } = await mountView('/import?mode=existing&group_id=7', recovery({ consume }))
    expect(consume).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-test="new-import"]').text()).toBe('recovered')
  })

  it('normalizes unknown modes to new without carrying unrelated query data', async () => {
    const { router, wrapper } = await mountView('/import?mode=bad&group_id=7')
    await flushPromises()
    expect(wrapper.find('[data-test="new-import"]').exists()).toBe(true)
    expect(router.currentRoute.value.query).toEqual({ mode: 'new' })
  })
})
