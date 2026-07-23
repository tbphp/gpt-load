import type { Router, RouterHistory, RouteRecordRaw } from 'vue-router'
import { createRouter, createWebHistory } from 'vue-router'

import LoginView from '@/features/auth/LoginView.vue'
import HomeView from '@/views/HomeView.vue'
import PlaceholderView from '@/views/PlaceholderView.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'home',
    component: HomeView,
    meta: { requiresAuth: true },
  },
  { path: '/login', name: 'login', component: LoginView },
  {
    path: '/import',
    name: 'import',
    component: PlaceholderView,
    meta: { title: '导入密钥', requiresAuth: true },
  },
  {
    path: '/groups/:id',
    name: 'group-detail',
    component: PlaceholderView,
    meta: { title: '分组详情', requiresAuth: true },
  },
  {
    path: '/access-keys',
    name: 'access-keys',
    component: PlaceholderView,
    meta: { title: '访问密钥', requiresAuth: true },
  },
  {
    path: '/monitor',
    name: 'monitor',
    component: PlaceholderView,
    meta: { title: '监控', requiresAuth: true },
  },
  {
    path: '/settings',
    name: 'settings',
    component: PlaceholderView,
    meta: { title: '设置', requiresAuth: true },
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
