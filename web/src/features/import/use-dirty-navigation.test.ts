import { mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

import { createAppI18n } from '@/i18n'

import {
  createDirtyNavigationController,
  dirtyNavigationKey,
  useDirtyNavigation,
} from './use-dirty-navigation'

async function mountDirty() {
  const dirty = ref(true)
  const Page = defineComponent({
    setup() {
      useDirtyNavigation(dirty)
      return {}
    },
    template: '<div>draft</div>',
  })
  const Other = { template: '<div>other</div>' }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/import', component: Page },
      { path: '/login', component: Other },
    ],
  })
  const controller = createDirtyNavigationController()
  await router.push('/import')
  await router.isReady()
  const wrapper = mount(
    { template: '<RouterView />' },
    {
      global: {
        plugins: [router, createAppI18n(undefined, 'en-US').plugin],
        provide: { [dirtyNavigationKey as symbol]: controller },
      },
    },
  )
  return { controller, dirty, router, wrapper }
}

describe('dirty import navigation', () => {
  it('prompts for route leave and beforeunload while the draft is dirty', async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    const { router, wrapper } = await mountDirty()
    await router.push('/login')
    expect(router.currentRoute.value.path).toBe('/import')
    expect(confirm).toHaveBeenCalledOnce()

    const event = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
    wrapper.unmount()
    vi.unstubAllGlobals()
  })

  it('prompts before a same-route query update while the draft is dirty', async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    const { router, wrapper } = await mountDirty()

    await router.push('/import?mode=existing')

    expect(router.currentRoute.value.fullPath).toBe('/import')
    expect(confirm).toHaveBeenCalledOnce()
    wrapper.unmount()
    vi.unstubAllGlobals()
  })

  it('allows a global 401 to bypass the prompt exactly once', async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    const { controller, router, wrapper } = await mountDirty()
    controller.bypassNext()
    await router.push('/login')
    expect(router.currentRoute.value.path).toBe('/login')
    expect(confirm).not.toHaveBeenCalled()
    wrapper.unmount()
    vi.unstubAllGlobals()
  })
})
