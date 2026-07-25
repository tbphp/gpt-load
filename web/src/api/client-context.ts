import { inject, type InjectionKey } from 'vue'

import type { ApiClient } from './client'

export const apiClientKey: InjectionKey<ApiClient> = Symbol('api-client')

export function useApiClient(): ApiClient {
  const client = inject(apiClientKey)
  if (!client) throw new Error('API_CLIENT_NOT_PROVIDED')
  return client
}
