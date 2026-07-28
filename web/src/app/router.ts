import type { Component } from 'vue'
import type { Router, RouterHistory, RouteRecordRaw } from 'vue-router'
import { createRouter, createWebHistory } from 'vue-router'

import type { MessageNamespace } from '@/i18n'

function lazyView(loader: () => Promise<{ default: Component }>) {
  return () => loader().then((module) => module.default)
}

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'home',
    component: lazyView(() => import('@/features/home/HomeView.vue')),
    meta: { titleKey: 'home.title', requiresAuth: true, primaryNav: 'home' },
  },
  {
    path: '/login',
    name: 'login',
    component: lazyView(() => import('@/features/auth/LoginView.vue')),
  },
  {
    path: '/import',
    name: 'import',
    component: lazyView(() => import('@/features/import/ImportView.vue')),
    meta: {
      titleKey: 'shell.import',
      requiresAuth: true,
      messageNamespaces: ['import'],
    },
  },
  {
    path: '/groups/:id',
    name: 'group-detail',
    component: lazyView(() => import('@/features/groups/GroupDetailView.vue')),
    meta: {
      titleKey: 'shell.groupDetail',
      requiresAuth: true,
      messageNamespaces: ['group', 'import'],
    },
  },
  {
    path: '/access-keys',
    name: 'access-keys',
    component: lazyView(() => import('@/features/access-keys/AccessKeysView.vue')),
    meta: {
      titleKey: 'shell.accessKeys',
      requiresAuth: true,
      primaryNav: 'access-keys',
      messageNamespaces: ['access-keys'],
    },
  },
  {
    path: '/monitor',
    name: 'monitor',
    component: lazyView(() => import('@/features/monitor/MonitorView.vue')),
    meta: {
      titleKey: 'shell.monitor',
      requiresAuth: true,
      primaryNav: 'monitor',
      messageNamespaces: ['monitor'],
    },
  },
  {
    path: '/settings',
    name: 'settings',
    component: lazyView(() => import('@/features/settings/SettingsView.vue')),
    meta: {
      titleKey: 'shell.settings',
      requiresAuth: true,
      primaryNav: 'settings',
      messageNamespaces: ['settings', 'model-prices', 'import'],
    },
  },
  {
    path: '/settings/model-prices',
    name: 'model-prices',
    component: lazyView(() => import('@/features/model-prices/ModelPricesView.vue')),
    meta: {
      titleKey: 'modelPrices.title',
      requiresAuth: true,
      primaryNav: 'settings',
      messageNamespaces: ['model-prices'],
    },
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: lazyView(() => import('@/features/not-found/NotFoundView.vue')),
    meta: {
      titleKey: 'notFound.title',
      requiresAuth: true,
    },
  },
]

export interface RouterAuth {
  hasCredential(): boolean
}

export interface RouterMessages {
  loadNamespaces(namespaces: readonly MessageNamespace[]): Promise<void>
}

export function createAppRouter(
  auth: RouterAuth,
  history: RouterHistory = createWebHistory(),
  messages?: RouterMessages,
) {
  const router = createRouter({ history, routes })
  router.beforeEach((to) => {
    if (!to.meta.requiresAuth || auth.hasCredential()) {
      return true
    }
    return {
      name: 'login',
      query: { redirect: to.fullPath },
    }
  })
  router.beforeResolve(async (to) => {
    const namespaces = (to.meta.messageNamespaces ?? []) as MessageNamespace[]
    await messages?.loadNamespaces(namespaces)
    return true
  })
  return router
}

export function safeRedirect(raw: unknown, router: Router): string {
  if (
    typeof raw !== 'string' ||
    !raw.startsWith('/') ||
    raw.startsWith('//') ||
    raw.includes('\\')
  ) {
    return '/'
  }

  let decodedRaw: string
  try {
    decodedRaw = decodeURIComponent(raw)
  } catch {
    return '/'
  }
  if (decodedRaw.startsWith('//') || decodedRaw.includes('\\')) {
    return '/'
  }

  const resolved = router.resolve(raw)
  if (
    resolved.matched.length === 0 ||
    resolved.name === 'login' ||
    resolved.name === 'not-found' ||
    resolved.meta.requiresAuth !== true
  ) {
    return '/'
  }
  return resolved.fullPath
}
