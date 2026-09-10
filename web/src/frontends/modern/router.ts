import { createRouter, createWebHistory } from 'vue-router'

import { navigationItems, pagePath } from './app/navigation'

export function createModernRouter() {
  return createRouter({
    history: createWebHistory(),
    routes: [
      {
        path: pagePath('home'),
        name: 'modern-home',
        component: () => import('./features/home/HomeView.vue'),
      },
      {
        path: pagePath('settings'),
        name: 'modern-settings',
        component: () => import('./features/settings/SettingsView.vue'),
      },
      ...navigationItems
        .filter((item) => item.id !== 'home' && item.id !== 'settings')
        .map((item) => ({
          path: item.path,
          name: item.name,
          component: () => import('./features/workspace/WorkspaceView.vue'),
          props: { workspaceId: item.id },
        })),
      { path: pagePath('monitor'), redirect: { name: 'modern-usage' } },
      {
        path: '/:pathMatch(.*)*',
        name: 'modern-unavailable',
        component: () => import('./features/home/UnavailableView.vue'),
      },
    ],
    scrollBehavior(_to, _from, savedPosition) {
      return savedPosition ?? { left: 0, top: 0 }
    },
  })
}
