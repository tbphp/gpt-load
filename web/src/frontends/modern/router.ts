import { createRouter, createWebHistory, type Router, type RouterHistory } from 'vue-router'

import { navigationItems, pagePath } from './app/navigation'
import type { AuthSession } from './features/auth/auth-session'

export function createModernRouter(
  session: Pick<AuthSession, 'hasCredential' | 'getPrincipalType'>,
  history: RouterHistory = createWebHistory(),
) {
  const router = createRouter({
    history,
    sensitive: true,
    strict: true,
    routes: [
      ...navigationItems.map((item) => ({
        path: item.path,
        name: item.name,
        component:
          item.id === 'home'
            ? () => import('./features/home/HomeView.vue')
            : item.id === 'settings'
              ? () => import('./features/settings/SettingsView.vue')
              : () => import('./features/workspace/WorkspaceView.vue'),
        props: item.id === 'home' || item.id === 'settings' ? undefined : { workspaceId: item.id },
        meta: { requiresAuth: true, adminOnly: item.adminOnly },
      })),
      {
        path: pagePath('monitor'),
        redirect: { name: 'modern-usage' },
        meta: { requiresAuth: true },
      },
      {
        path: pagePath('group-detail'),
        name: 'modern-group-detail',
        component: () => import('./features/workspace/WorkspaceView.vue'),
        props: { workspaceId: 'groups' },
        meta: {
          primaryNav: 'modern-groups',
          titleKey: 'pages.groupDetail.title',
          requiresAuth: true,
          adminOnly: true,
        },
      },
      {
        path: pagePath('import'),
        name: 'modern-import',
        component: () => import('./features/workspace/WorkspaceView.vue'),
        props: { workspaceId: 'groups' },
        meta: {
          primaryNav: 'modern-groups',
          titleKey: 'pages.import.title',
          requiresAuth: true,
          adminOnly: true,
        },
      },
      {
        path: pagePath('login'),
        name: 'modern-login',
        component: () => import('./features/auth/LoginView.vue'),
        meta: { titleKey: 'auth.title' },
      },
      {
        path: '/:pathMatch(.*)*',
        name: 'modern-unavailable',
        component: () => import('./features/home/UnavailableView.vue'),
        meta: { requiresAuth: true },
      },
    ],
    scrollBehavior(to, from, savedPosition) {
      if (savedPosition) return savedPosition
      if (to.name === 'modern-login' && from.name === 'modern-login') return false
      return { left: 0, top: 0 }
    },
  })
  router.beforeEach((to) => {
    if (!to.meta.requiresAuth) return true
    if (!session.hasCredential()) return loginLocation(to.fullPath)
    if (to.meta.adminOnly && session.getPrincipalType() === 'access_key') {
      return { name: 'modern-home' }
    }
    return true
  })
  return router
}

export function loginLocation(redirect?: string) {
  return { name: 'modern-login', query: redirect ? { redirect } : {} }
}

export function safeRedirect(raw: unknown, router: Router): string {
  const fallback = pagePath('home')
  if (
    typeof raw !== 'string' ||
    !raw.startsWith('/') ||
    raw.startsWith('//') ||
    raw.includes('\\')
  ) {
    return fallback
  }
  let decoded: string
  try {
    decoded = decodeURIComponent(raw)
  } catch {
    return fallback
  }
  if (
    decoded.startsWith('//') ||
    decoded.includes('\\') ||
    /[\u0000-\u001f\u007f]/u.test(decoded)
  ) {
    return fallback
  }
  const resolved = router.resolve(raw)
  const matchedPath = resolved.matched.at(-1)?.path
  let segments: string[]
  try {
    segments = decodeURIComponent(resolved.path).split('/')
  } catch {
    return fallback
  }
  const pattern = matchedPath?.split('/') ?? []
  if (
    !resolved.matched.length ||
    segments.length !== pattern.length ||
    !pattern.every((segment, index) =>
      segment.startsWith(':') ? segments[index] !== '' : segment === segments[index],
    ) ||
    resolved.name === 'modern-login' ||
    resolved.name === 'modern-unavailable' ||
    resolved.meta.requiresAuth !== true
  )
    return fallback
  return resolved.fullPath
}
