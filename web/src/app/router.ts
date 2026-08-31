import type { Component } from 'vue'
import type { Router, RouterHistory, RouteRecordRaw, RouteRecordSingleView } from 'vue-router'
import { createRouter, createWebHistory } from 'vue-router'

import type { MessageNamespace } from '@/i18n'

import { pagePath, pagePathMatches } from './page-routes'
import { loginLocation, notFoundLocation, pageRouteNames } from './route-locations'

function lazyView(loader: () => Promise<{ default: Component }>) {
  return () => loader().then((module) => module.default)
}

function pageRoute(
  name: string,
  definition: Omit<RouteRecordSingleView, 'name' | 'path'>,
): RouteRecordRaw {
  return {
    ...definition,
    name,
    path: pagePath(name),
  }
}

const routes: RouteRecordRaw[] = [
  pageRoute(pageRouteNames.home, {
    component: lazyView(() => import('@/features/home/HomeView.vue')),
    meta: {
      titleKey: 'home.ledger.title',
      requiresAuth: true,
      primaryNav: 'home',
      messageNamespaces: ['access-keys', 'group'],
    },
  }),
  pageRoute(pageRouteNames.login, {
    component: lazyView(() => import('@/features/auth/LoginView.vue')),
  }),
  pageRoute(pageRouteNames.import, {
    component: lazyView(() => import('@/features/import/ImportView.vue')),
    meta: {
      titleKey: 'shell.import',
      requiresAuth: true,
      adminOnly: true,
      primaryNav: 'groups',
      messageNamespaces: ['import'],
    },
  }),
  pageRoute(pageRouteNames.groups, {
    component: lazyView(() => import('@/features/groups/GroupsView.vue')),
    meta: {
      titleKey: 'groups.title',
      requiresAuth: true,
      adminOnly: true,
      primaryNav: 'groups',
      messageNamespaces: ['group'],
    },
  }),
  pageRoute(pageRouteNames.groupDetail, {
    component: lazyView(() => import('@/features/groups/GroupDetailView.vue')),
    meta: {
      titleKey: 'shell.groupDetail',
      requiresAuth: true,
      adminOnly: true,
      primaryNav: 'groups',
      messageNamespaces: ['group', 'import'],
    },
  }),
  pageRoute(pageRouteNames.accessKeys, {
    component: lazyView(() => import('@/features/access-keys/AccessKeysView.vue')),
    meta: {
      titleKey: 'shell.accessKeys',
      requiresAuth: true,
      adminOnly: true,
      primaryNav: 'access-keys',
      messageNamespaces: ['access-keys'],
    },
  }),
  pageRoute(pageRouteNames.monitor, {
    component: lazyView(() => import('@/features/monitor/MonitorView.vue')),
    meta: {
      titleKey: 'shell.monitor',
      requiresAuth: true,
      primaryNav: 'monitor',
      messageNamespaces: ['monitor'],
    },
  }),
  pageRoute(pageRouteNames.models, {
    component: lazyView(() => import('@/features/models/ModelsView.vue')),
    meta: {
      titleKey: 'models.title',
      requiresAuth: true,
      primaryNav: 'models',
      messageNamespaces: ['models', 'model-prices'],
    },
  }),
  pageRoute(pageRouteNames.settings, {
    component: lazyView(() => import('@/features/settings/SettingsView.vue')),
    meta: {
      titleKey: 'shell.settings',
      requiresAuth: true,
      adminOnly: true,
      primaryNav: 'settings',
      messageNamespaces: ['settings', 'model-prices', 'import'],
    },
  }),
  {
    path: '/:pathMatch(.*)*',
    name: pageRouteNames.notFound,
    component: lazyView(() => import('@/features/not-found/NotFoundView.vue')),
    meta: {
      titleKey: 'notFound.title',
      requiresAuth: true,
    },
  },
]

export interface RouterAuth {
  hasCredential(): boolean
  getPrincipalType(): 'admin' | 'access_key' | null
}

export interface RouterMessages {
  loadNamespaces(namespaces: readonly MessageNamespace[]): Promise<void>
}

export function createAppRouter(
  auth: RouterAuth,
  history: RouterHistory = createWebHistory(),
  messages?: RouterMessages,
) {
  const router = createRouter({
    history,
    routes,
    sensitive: true,
    strict: true,
    scrollBehavior(to, from, savedPosition) {
      if (savedPosition) return savedPosition

      const pageViewChanged =
        to.path !== from.path ||
        normalizedQueryValue(to.query.tab) !== normalizedQueryValue(from.query.tab) ||
        normalizedQueryValue(to.query.mode) !== normalizedQueryValue(from.query.mode)
      return pageViewChanged ? { left: 0, top: 0 } : false
    },
  })
  router.beforeEach((to) => {
    if (
      typeof to.name === 'string' &&
      to.name !== pageRouteNames.notFound &&
      !pagePathMatches(to.name, to.path)
    ) {
      return notFoundLocation(decodedPathSegments(to.path))
    }
    if (to.meta.adminOnly && auth.getPrincipalType() === 'access_key') {
      return { name: pageRouteNames.home }
    }
    if (!to.meta.requiresAuth || auth.hasCredential()) {
      return true
    }
    return loginLocation(to.fullPath)
  })
  router.beforeResolve(async (to) => {
    const namespaces = (to.meta.messageNamespaces ?? []) as MessageNamespace[]
    await messages?.loadNamespaces(namespaces)
    return true
  })
  return router
}

function normalizedQueryValue(value: string | null | (string | null)[] | undefined): string {
  if (Array.isArray(value)) return value[0] ?? ''
  return value ?? ''
}

function decodedPathSegments(path: string): string[] {
  try {
    const segments = decodeURIComponent(path).split('/').filter(Boolean)
    return segments.length > 0 ? segments : ['invalid-path']
  } catch {
    return ['invalid-path']
  }
}

export function safeRedirect(raw: unknown, router: Router): string {
  const fallback = pagePath(pageRouteNames.home)
  if (
    typeof raw !== 'string' ||
    !raw.startsWith('/') ||
    raw.startsWith('//') ||
    raw.includes('\\')
  ) {
    return fallback
  }

  let decodedRaw: string
  try {
    decodedRaw = decodeURIComponent(raw)
  } catch {
    return fallback
  }
  if (decodedRaw.startsWith('//') || decodedRaw.includes('\\')) {
    return fallback
  }

  const resolved = router.resolve(raw)
  if (
    resolved.matched.length === 0 ||
    typeof resolved.name !== 'string' ||
    !pagePathMatches(resolved.name, resolved.path) ||
    resolved.name === pageRouteNames.login ||
    resolved.name === pageRouteNames.notFound ||
    resolved.meta.requiresAuth !== true
  ) {
    return fallback
  }
  return resolved.fullPath
}
