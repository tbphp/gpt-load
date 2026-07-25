import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { createMemoryHistory } from 'vue-router'

import { createApiClient } from '@/api/client'
import { controlQueryKeys } from '@/app/query-keys'
import { createAppRouter } from '@/app/router'
import AppSelect from '@/components/ui/AppSelect.vue'
import { authSessionKey, createAuthSession } from '@/features/auth/auth-session'
import {
  createImportRecoveryService,
  importRecoveryKey,
  importRecoveryStorageKey,
  type ImportRecoveryService,
} from '@/features/import/import-recovery'
import type { ImportDraft } from '@/features/import/model-draft'
import {
  createDirtyNavigationController,
  dirtyNavigationKey,
} from '@/features/import/use-dirty-navigation'
import { createThemeController, themeControllerKey } from '@/features/preferences/theme'
import { appI18nKey } from '@/i18n/context'
import { createAppI18n } from '@/i18n'

import AppShell from './AppShell.vue'
import { handleGlobalUnauthorized } from './unauthorized'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

const TestPage = defineComponent({ template: '<h1>Page body</h1>' })

async function mountShell() {
  const queryClient = new QueryClient()
  const session = createAuthSession({
    queryClient,
    validate: async () => ({ authenticated: true }),
  })
  await session.login('test-key')
  const router = createAppRouter(session, createMemoryHistory())
  await router.push('/')
  await router.isReady()
  const appI18n = createAppI18n(undefined, 'en-US')
  const theme = createThemeController({
    documentElement: document.documentElement,
    storage: window.localStorage,
    matchMedia: window.matchMedia.bind(window),
  })
  const recovery: ImportRecoveryService = {
    register: () => () => {},
    captureForUnauthorized: () => 'no-active-draft',
    consume: () => null,
    clear: vi.fn(),
    sweep: () => {},
    dispose: () => {},
  }
  const dirtyNavigation = createDirtyNavigationController()
  const wrapper = mount(AppShell, {
    slots: { default: TestPage },
    attachTo: document.body,
    global: {
      plugins: [appI18n.plugin, [VueQueryPlugin, { queryClient }], router],
      provide: {
        [authSessionKey as symbol]: session,
        [appI18nKey as symbol]: appI18n,
        [themeControllerKey as symbol]: theme,
        [importRecoveryKey as symbol]: recovery,
        [dirtyNavigationKey as symbol]: dirtyNavigation,
      },
    },
  })
  return { appI18n, dirtyNavigation, recovery, router, session, theme, wrapper }
}

describe('AppShell', () => {
  it('renders shared navigation, import action, skip link and main landmark', async () => {
    const { wrapper } = await mountShell()

    expect(wrapper.get('[href="#main-content"]').text()).toBe('Skip to main content')
    expect(wrapper.get('nav').attributes('aria-label')).toBe('Primary navigation')
    expect(wrapper.get('[href="/import"]').text()).toContain('Import upstream keys')
    expect(wrapper.get('main#main-content').text()).toContain('Page body')
    expect(wrapper.find('[aria-label="Open navigation"]').exists()).toBe(true)
  })

  it('changes locale and theme through injected controllers', async () => {
    const { appI18n, wrapper } = await mountShell()

    wrapper.findComponent(AppSelect).vm.$emit('update:modelValue', 'ja-JP')
    await nextTick()
    expect(appI18n.getLocale()).toBe('ja-JP')

    await wrapper.get('[aria-label="ダークテーマを使用"]').trigger('click')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('ignores a delayed global 401 after explicit Import logout without recreating recovery', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(controlQueryKeys.groups.list(), [{ id: 7 }])
    queryClient.getMutationCache().build(queryClient, { mutationFn: async () => undefined })
    const session = createAuthSession({
      storage: window.sessionStorage,
      queryClient,
      validate: async () => ({ authenticated: true }),
    })
    await session.login('active-key')
    const router = createAppRouter(session, createMemoryHistory())
    await router.push('/import')
    await router.isReady()
    const recovery = createImportRecoveryService({
      storage: window.sessionStorage,
      now: () => 10_000,
      setTimer: (callback, delayMs) => setTimeout(callback, delayMs),
      clearTimer: (timer) => clearTimeout(timer),
    })
    const recoveredDraft: ImportDraft = {
      mode: 'new',
      step: 1,
      preset_id: 'custom',
      name: 'Delayed',
      upstream_url: 'https://api.example.com',
      protocols: ['openai'],
      keys: 'LATE_401_KEY_CANARY',
      header_rules: { set: { 'X-Canary': 'LATE_401_HEADER_CANARY' }, remove: [] },
      models: [],
    }
    const unregister = recovery.register(() => recoveredDraft)
    const capture = vi.spyOn(recovery, 'captureForUnauthorized')
    const dirtyNavigation = createDirtyNavigationController()
    const delayed = deferred<Response>()
    const apiClient = createApiClient({
      fetch: vi.fn(() => delayed.promise) as typeof fetch,
      getAuthKey: () => session.getAuthKey(),
      getLocale: () => 'en-US',
      onUnauthorized: () => {
        const redirect =
          router.currentRoute.value.meta.requiresAuth === true
            ? router.currentRoute.value.fullPath
            : '/'
        void handleGlobalUnauthorized({
          recovery,
          dirtyNavigation,
          session,
          router,
          redirect,
        })
      },
    })
    const request = apiClient.request('/api/groups')
    const appI18n = createAppI18n(undefined, 'en-US')
    const theme = createThemeController({
      documentElement: document.documentElement,
      storage: window.localStorage,
      matchMedia: window.matchMedia.bind(window),
    })
    const wrapper = mount(AppShell, {
      slots: { default: TestPage },
      global: {
        plugins: [appI18n.plugin, [VueQueryPlugin, { queryClient }], router],
        provide: {
          [authSessionKey as symbol]: session,
          [appI18nKey as symbol]: appI18n,
          [themeControllerKey as symbol]: theme,
          [importRecoveryKey as symbol]: recovery,
          [dirtyNavigationKey as symbol]: dirtyNavigation,
        },
      },
    })

    await wrapper.get('[aria-label="Sign out"]').trigger('click')
    await flushPromises()
    delayed.resolve(
      new Response(JSON.stringify({ code: 'UNAUTHORIZED', message: 'unauthorized' }), {
        status: 401,
      }),
    )
    await expect(request).rejects.toMatchObject({ code: 'UNAUTHORIZED' })
    await flushPromises()

    expect(capture).not.toHaveBeenCalled()
    expect(window.sessionStorage.getItem(importRecoveryStorageKey)).toBeNull()
    expect(router.currentRoute.value.name).toBe('login')
    expect(session.hasCredential()).toBe(false)
    expect(queryClient.getQueryData(controlQueryKeys.groups.list())).toBeUndefined()
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0)
    expect(dirtyNavigation.consumeBypass()).toBe(false)

    unregister()
    wrapper.unmount()
    recovery.dispose()
    theme.dispose()
  })

  it('logs out without placing the credential in rendered markup', async () => {
    const { dirtyNavigation, recovery, session, wrapper } = await mountShell()

    expect(wrapper.html()).not.toContain('test-key')
    await wrapper.get('[aria-label="Sign out"]').trigger('click')

    expect(session.hasCredential()).toBe(false)
    expect(recovery.clear).toHaveBeenCalledOnce()
    expect(dirtyNavigation.consumeBypass()).toBe(false)
  })
})
