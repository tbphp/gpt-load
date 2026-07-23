import type { RouterHistory, RouteRecordRaw } from 'vue-router'
import { createRouter, createWebHistory } from 'vue-router'

import HomeView from '@/views/HomeView.vue'
import LoginView from '@/views/LoginView.vue'
import PlaceholderView from '@/views/PlaceholderView.vue'

const routes: RouteRecordRaw[] = [
  { path: '/', name: 'home', component: HomeView },
  { path: '/login', name: 'login', component: LoginView },
  {
    path: '/import',
    name: 'import',
    component: PlaceholderView,
    meta: { title: '导入密钥' },
  },
  {
    path: '/groups/:id',
    name: 'group-detail',
    component: PlaceholderView,
    meta: { title: '分组详情' },
  },
  {
    path: '/access-keys',
    name: 'access-keys',
    component: PlaceholderView,
    meta: { title: '访问密钥' },
  },
  {
    path: '/monitor',
    name: 'monitor',
    component: PlaceholderView,
    meta: { title: '监控' },
  },
  {
    path: '/settings',
    name: 'settings',
    component: PlaceholderView,
    meta: { title: '设置' },
  },
]

export function createAppRouter(history: RouterHistory = createWebHistory()) {
  return createRouter({ history, routes })
}
