import type { Router, RouterHistory, RouteRecordRaw } from 'vue-router'
import { createRouter, createWebHistory } from 'vue-router'

import LoginView from '@/features/auth/LoginView.vue'
import HomeView from '@/features/home/HomeView.vue'
import GroupDetailView from '@/features/groups/GroupDetailView.vue'
import ImportView from '@/features/import/ImportView.vue'
import AccessKeysView from '@/features/access-keys/AccessKeysView.vue'
import MonitorView from '@/features/monitor/MonitorView.vue'
import ModelPricesView from '@/features/model-prices/ModelPricesView.vue'
import SettingsView from '@/features/settings/SettingsView.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'home',
    component: HomeView,
    meta: { titleKey: 'home.title', requiresAuth: true, primaryNav: 'home' },
  },
  { path: '/login', name: 'login', component: LoginView },
  {
    path: '/import',
    name: 'import',
    component: ImportView,
    meta: { titleKey: 'shell.import', requiresAuth: true },
  },
  {
    path: '/groups/:id',
    name: 'group-detail',
    component: GroupDetailView,
    meta: { titleKey: 'shell.groupDetail', requiresAuth: true },
  },
  {
    path: '/access-keys',
    name: 'access-keys',
    component: AccessKeysView,
    meta: { titleKey: 'shell.accessKeys', requiresAuth: true, primaryNav: 'access-keys' },
  },
  {
    path: '/monitor',
    name: 'monitor',
    component: MonitorView,
    meta: { titleKey: 'shell.monitor', requiresAuth: true, primaryNav: 'monitor' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: SettingsView,
    meta: { titleKey: 'shell.settings', requiresAuth: true, primaryNav: 'settings' },
  },
  {
    path: '/settings/model-prices',
    name: 'model-prices',
    component: ModelPricesView,
    meta: { titleKey: 'modelPrices.title', requiresAuth: true, primaryNav: 'settings' },
  },
]

export interface RouterAuth {
  hasCredential(): boolean
}

export function createAppRouter(auth: RouterAuth, history: RouterHistory = createWebHistory()) {
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
    resolved.meta.requiresAuth !== true
  ) {
    return '/'
  }
  return resolved.fullPath
}
