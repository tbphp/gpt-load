import { mount } from '@vue/test-utils'
import { defineComponent, ref, type Ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

import { createTestAppI18n } from '@/test/i18n'

import {
  createUnsavedChangesController,
  unsavedChangesKey,
  useUnsavedChanges,
  type UnsavedChangesGuard,
} from './unsaved-changes'

async function mountGuard(options: { dirty?: Ref<boolean>; blocked?: Ref<boolean> } = {}) {
  const dirty = options.dirty ?? ref(true)
  const blocked = options.blocked ?? ref(false)
  let guard!: UnsavedChangesGuard
  const EditPage = defineComponent({
    setup() {
      guard = useUnsavedChanges(dirty, { blocked })
      return {}
    },
    template: '<div>draft</div>',
  })
  const OtherPage = { template: '<div>other</div>' }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/edit', component: EditPage },
      { path: '/other', component: OtherPage },
    ],
  })
  const controller = createUnsavedChangesController()
  await router.push('/edit')
  await router.isReady()
  const wrapper = mount(
    { template: '<RouterView />' },
    {
      global: {
        plugins: [router, createTestAppI18n(undefined, 'en-US').plugin],
        provide: { [unsavedChangesKey as symbol]: controller },
      },
    },
  )
  return { blocked, controller, dirty, guard, router, wrapper }
}

describe('shared unsaved-changes policy', () => {
  it('prompts for route leave, same-route query changes, and beforeunload', async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    const { router, wrapper } = await mountGuard()

    await router.push('/edit?mode=other')
    expect(router.currentRoute.value.fullPath).toBe('/edit')
    await router.push('/other')
    expect(router.currentRoute.value.path).toBe('/edit')
    expect(confirm).toHaveBeenCalledTimes(2)

    const event = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)

    wrapper.unmount()
    vi.unstubAllGlobals()
  })

  it('blocks busy navigation without downgrading it to a dirty confirm', async () => {
    const confirm = vi.fn(() => true)
    vi.stubGlobal('confirm', confirm)
    const { guard, router, wrapper } = await mountGuard({ blocked: ref(true) })

    await router.push('/other')

    expect(router.currentRoute.value.path).toBe('/edit')
    expect(guard.confirmDiscard()).toBe(false)
    expect(confirm).not.toHaveBeenCalled()
    wrapper.unmount()
    vi.unstubAllGlobals()
  })

  it('bypasses exactly one intentional route update', async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    const { guard, router, wrapper } = await mountGuard()

    await guard.runWithoutPrompt(() => router.push('/edit?applied=1'))
    expect(router.currentRoute.value.fullPath).toBe('/edit?applied=1')
    expect(confirm).not.toHaveBeenCalled()

    await router.push('/other')
    expect(router.currentRoute.value.path).toBe('/edit')
    expect(confirm).toHaveBeenCalledOnce()
    wrapper.unmount()
    vi.unstubAllGlobals()
  })

  it('uses the same discard decision for an overlay close request', async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    const { guard, wrapper } = await mountGuard()

    expect(guard.confirmDiscard()).toBe(false)
    expect(confirm).toHaveBeenCalledOnce()

    confirm.mockReturnValue(true)
    expect(guard.confirmDiscard()).toBe(true)
    expect(confirm).toHaveBeenCalledTimes(2)
    wrapper.unmount()
    vi.unstubAllGlobals()
  })
})
