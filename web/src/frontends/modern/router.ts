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
        path: pagePath('group-detail'),
        name: 'modern-group-detail',
        component: () => import('./features/workspace/WorkspaceView.vue'),
        props: { workspaceId: 'groups', titleKey: 'pages.groupDetail.title' },
        meta: { primaryNav: 'modern-groups', titleKey: 'pages.groupDetail.title' },
      },
      {
        path: pagePath('import'),
        name: 'modern-import',
        component: () => import('./features/workspace/WorkspaceView.vue'),
        props: { workspaceId: 'groups', titleKey: 'pages.import.title' },
        meta: { primaryNav: 'modern-groups', titleKey: 'pages.import.title' },
      },
      {
        path: pagePath('login'),
        name: 'modern-login',
        component: () => import('./features/workspace/WorkspaceView.vue'),
        props: { workspaceId: 'home', titleKey: 'pages.login.title' },
        meta: { titleKey: 'pages.login.title' },
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
