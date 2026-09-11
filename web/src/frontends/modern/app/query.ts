import { QueryClient } from '@tanstack/vue-query'

export function createModernQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}
