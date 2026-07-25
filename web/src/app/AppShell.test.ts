import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { createMemoryHistory } from 'vue-router'

import { createAppRouter } from '@/app/router'
import AppSelect from '@/components/ui/AppSelect.vue'
import { authSessionKey, createAuthSession } from '@/features/auth/auth-session'
import { importRecoveryKey, type ImportRecoveryService } from '@/features/import/import-recovery'
import {
  createDirtyNavigationController,
  dirtyNavigationKey,
} from '@/features/import/use-dirty-navigation'
import { createThemeController, themeControllerKey } from '@/features/preferences/theme'
import { appI18nKey } from '@/i18n/context'
import { createAppI18n } from '@/i18n'

import AppShell from './AppShell.vue'

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
  const Page = defineComponent({ template: '<h1>Page body</h1>' })
  const wrapper = mount(AppShell, {
    slots: { default: Page },
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

  it('logs out without placing the credential in rendered markup', async () => {
    const { dirtyNavigation, recovery, session, wrapper } = await mountShell()

    expect(wrapper.html()).not.toContain('test-key')
    await wrapper.get('[aria-label="Sign out"]').trigger('click')

    expect(session.hasCredential()).toBe(false)
    expect(recovery.clear).toHaveBeenCalledOnce()
    expect(dirtyNavigation.consumeBypass()).toBe(false)
  })
})
