/// <reference types="vite/client" />

export {}

declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth?: boolean
    titleKey?: string
  }
}
