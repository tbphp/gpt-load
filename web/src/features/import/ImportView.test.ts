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
        ExistingGroupImport: {
          name: 'ExistingGroupImport',
          props: ['initialDraft'],
          template:
            '<div data-test="existing-import">{{ initialDraft ? "recovered" : "existing" }}</div>',
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

  it('renders the route-backed existing selector and restores mode with browser history', async () => {
    const { router, wrapper } = await mountView('/import?mode=existing')

    expect(wrapper.get('[data-test="existing-import"]').text()).toBe('existing')
    expect(wrapper.get('[data-test="mode-existing"]').attributes()['aria-pressed']).toBe('true')

    await wrapper.get('[data-test="mode-new"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/import?mode=new')
    expect(wrapper.get('[data-test="new-import"]').text()).toBe('new')

    router.back()
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/import?mode=existing')
    expect(wrapper.get('[data-test="existing-import"]').text()).toBe('existing')

    router.forward()
    await flushPromises()
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

  it('restores an existing-mode recovery draft with raw keys kept out of the route', async () => {
    const consume = vi.fn(() => ({
      mode: 'existing' as const,
      group_id: 7,
      keys: 'RECOVERED_EXISTING_KEY_CANARY',
    }))
    const { router, wrapper } = await mountView('/import', recovery({ consume }))
    await flushPromises()

    expect(wrapper.get('[data-test="existing-import"]').text()).toBe('recovered')
    expect(wrapper.findComponent({ name: 'ExistingGroupImport' }).props('initialDraft')).toEqual({
      mode: 'existing',
      group_id: 7,
      keys: 'RECOVERED_EXISTING_KEY_CANARY',
    })
    expect(router.currentRoute.value.fullPath).toBe('/import?mode=existing&group_id=7')
    expect(router.currentRoute.value.fullPath).not.toContain('RECOVERED_EXISTING_KEY_CANARY')
  })

  it.each(['/import?mode=bad&group_id=7', '/import?mode=existing&mode=new&group_id=7'])(
    'normalizes unknown or malformed modes to new without carrying unrelated query data',
    async (path) => {
      const { router, wrapper } = await mountView(path)
      await flushPromises()
      expect(wrapper.find('[data-test="new-import"]').exists()).toBe(true)
      expect(router.currentRoute.value.query).toEqual({ mode: 'new' })
    },
  )
})
