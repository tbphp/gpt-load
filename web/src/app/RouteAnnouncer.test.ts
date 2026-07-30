import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, reactive } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

import { authSessionKey, type AuthSession } from '@/features/auth/auth-session'
import { createTestAppI18n } from '@/test/i18n'

import RouteAnnouncer from './RouteAnnouncer.vue'

const Home = {
  template: '<main id="main-content" tabindex="-1"><h1 tabindex="-1">Home</h1></main>',
}
const Settings = {
  template:
    '<main id="main-content" tabindex="-1"><h1 tabindex="-1">Settings</h1><button type="button">Keep focus</button></main>',
}

describe('RouteAnnouncer', () => {
  it('focuses and politely announces pathname changes without resetting focus for query-only updates', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', name: 'home', component: Home },
        { path: '/settings', name: 'settings', component: Settings },
      ],
    })
    await router.push('/')
    await router.isReady()
    const wrapper = mount(
      defineComponent({
        components: { RouteAnnouncer },
        template: '<RouteAnnouncer /><RouterView />',
      }),
      {
        attachTo: document.body,
        global: {
          plugins: [router, createTestAppI18n(undefined, 'en-US').plugin],
        },
      },
    )

    await router.push('/settings')
    await flushPromises()
    await vi.waitFor(() => {
      expect(document.activeElement?.textContent).toBe('Settings')
    })
    const live = wrapper.get('[data-test="route-announcer"]')
    expect(live.attributes()).toMatchObject({
      'aria-live': 'polite',
      'aria-atomic': 'true',
    })
    expect(live.text()).toBe('Settings')

    const button = wrapper.get<HTMLButtonElement>('button')
    button.element.focus()
    await router.push('/settings?section=appearance')
    await flushPromises()

    expect(document.activeElement).toBe(button.element)
    expect(live.text()).toBe('Settings')
    wrapper.unmount()
  })

  it('waits for delayed authentication before focusing and announcing a protected route', async () => {
    const authState = reactive({
      phase: 'unvalidated',
      retryAfterSeconds: 0,
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/settings',
          name: 'settings',
          component: Settings,
          meta: { requiresAuth: true },
        },
      ],
    })
    await router.push('/settings')
    await router.isReady()
    const wrapper = mount(
      {
        components: { RouteAnnouncer },
        setup: () => ({ authState }),
        template: `
          <RouteAnnouncer />
          <main v-if="authState.phase !== 'validated'" id="auth-gate">
            <h1 tabindex="-1">GPT-Load</h1>
          </main>
          <RouterView v-else />
        `,
      },
      {
        attachTo: document.body,
        global: {
          plugins: [router, createTestAppI18n(undefined, 'en-US').plugin],
          provide: {
            [authSessionKey as symbol]: { state: authState } as unknown as AuthSession,
          },
        },
      },
    )

    await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
    await flushPromises()
    expect(wrapper.get('[data-test="route-announcer"]').text()).toBe('')

    authState.phase = 'validated'
    await flushPromises()
    await vi.waitFor(() => {
      expect(document.activeElement?.textContent).toBe('Settings')
      expect(wrapper.get('[data-test="route-announcer"]').text()).toBe('Settings')
    })
    wrapper.unmount()
  })

  it('promotes focus and announcement when an asynchronous route heading appears', async () => {
    const viewState = reactive({ ready: false })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', name: 'home', component: Home }],
    })
    await router.push('/')
    await router.isReady()
    const wrapper = mount(
      {
        components: { RouteAnnouncer },
        setup: () => ({ viewState }),
        template: `
          <RouteAnnouncer />
          <main id="main-content" tabindex="-1">
            <h1 v-if="viewState.ready">Asynchronous home</h1>
          </main>
        `,
      },
      {
        attachTo: document.body,
        global: {
          plugins: [router, createTestAppI18n(undefined, 'en-US').plugin],
        },
      },
    )

    await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
    await flushPromises()
    expect(document.activeElement?.id).toBe('main-content')

    viewState.ready = true
    await flushPromises()
    await vi.waitFor(() => {
      expect(document.activeElement).toBe(wrapper.get('h1').element)
      expect(wrapper.get('[data-test="route-announcer"]').text()).toBe('Asynchronous home')
    })
    wrapper.unmount()
  })

  it('cancels pending focus work when unmounted before the animation frame', async () => {
    const callbacks: FrameRequestCallback[] = []
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      callbacks.push(callback)
      return callbacks.length
    })
    const persistentMain = document.createElement('main')
    persistentMain.id = 'main-content'
    persistentMain.tabIndex = -1
    document.body.append(persistentMain)
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', name: 'home', component: Home }],
    })
    await router.push('/')
    await router.isReady()
    const wrapper = mount(RouteAnnouncer, {
      attachTo: document.body,
      global: {
        plugins: [router, createTestAppI18n(undefined, 'en-US').plugin],
      },
    })
    await flushPromises()
    expect(callbacks).toHaveLength(1)

    wrapper.unmount()
    callbacks[0]?.(performance.now())
    await flushPromises()

    expect(document.activeElement).not.toBe(persistentMain)
    persistentMain.remove()
  })
})
