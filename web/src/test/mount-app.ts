import { VueQueryPlugin, type QueryClient } from '@tanstack/vue-query'
import { mount, type ComponentMountingOptions } from '@vue/test-utils'
import type { Component } from 'vue'
import { createMemoryHistory } from 'vue-router'

import type { ApiClient } from '@/api/client'
import { apiClientKey } from '@/api/client-context'
import { createAppRouter } from '@/app/router'
import { appI18nKey } from '@/i18n/context'
import { createAppI18n } from '@/i18n'

export async function mountApp(
  component: Component,
  options: {
    api: ApiClient
    queryClient: QueryClient
    path?: string
    locale?: 'zh-CN' | 'en-US' | 'ja-JP'
    mounting?: ComponentMountingOptions<Component>
  },
) {
  const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
  await router.push(options.path ?? '/')
  await router.isReady()
  const appI18n = createAppI18n(undefined, options.locale ?? 'zh-CN')
  const wrapper = mount(component, {
    ...options.mounting,
    global: {
      ...options.mounting?.global,
      plugins: [appI18n.plugin, [VueQueryPlugin, { queryClient: options.queryClient }], router],
      provide: {
        ...options.mounting?.global?.provide,
        [apiClientKey as symbol]: options.api,
        [appI18nKey as symbol]: appI18n,
      },
    },
  })
  return { appI18n, router, wrapper }
}
