import { dateRangePresets } from './components/ui/date-time'
import { createRouter, createWebHistory, type RouterHistory } from 'vue-router'

import { navigationItems, pagePath } from './app/navigation'
import { loginLocation } from './app/redirect'
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
              : item.id === 'groups'
                ? () => import('./features/groups/GroupsView.vue')
                : item.id === 'accessKeys'
                  ? () => import('./features/access-keys/AccessKeysView.vue')
                  : item.id === 'logs'
                    ? () => import('./features/logs/LogsView.vue')
                    : item.id === 'usage'
                      ? () => import('./features/usage/UsageView.vue')
                      : item.id === 'health'
                        ? () => import('./features/health/HealthView.vue')
                        : () => import('./features/models/ModelsView.vue'),
        meta: { requiresAuth: true, adminOnly: item.adminOnly },
      })),
      {
        path: pagePath('monitor-inspector'),
        name: 'modern-inspector',
        redirect: (to) => ({
          name: 'modern-home',
          hash: '#route-inspector',
          query: Object.fromEntries(
            ['protocol', 'external_model', 'access_key_id', 'run', 'view', 'q', 'page', 'page_size']
              .filter((key) => to.query[key] !== undefined)
              .map((key) => ['inspect_' + key, to.query[key]]),
          ),
        }),
        meta: { requiresAuth: true, adminOnly: true },
      },
      {
        path: pagePath('monitor'),
        redirect: { name: 'modern-usage' },
        meta: { requiresAuth: true },
      },
      {
        path: pagePath('group-detail'),
        name: 'modern-group-detail',
        component: () => import('./features/groups/GroupDetailView.vue'),
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
        redirect: { name: 'modern-groups', query: { import: '1' } },
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
    if (
      dateRangePresets.some((preset) => preset === to.query.preset) &&
      ('from_ms' in to.query || 'to_ms' in to.query)
    ) {
      const query = { ...to.query }
      delete query.from_ms
      delete query.to_ms
      return { path: to.path, query, hash: to.hash, replace: true }
    }
    return true
  })
  return router
}
