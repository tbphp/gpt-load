import { createRouter, createWebHistory } from 'vue-router'

export function createModernRouter() {
  return createRouter({
    history: createWebHistory(),
    routes: [
      {
        path: '/',
        name: 'modern-home',
        component: () => import('./features/home/HomeView.vue'),
      },
      {
        path: '/settings',
        name: 'modern-settings',
        component: () => import('./features/settings/SettingsView.vue'),
      },
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
