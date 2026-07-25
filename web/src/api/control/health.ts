import type { ApiClient } from '@/api/client'

import type { RuntimeHealthDto } from './types'

export function getRuntimeHealth(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<RuntimeHealthDto> {
  return client.request<RuntimeHealthDto>('/api/health', { method: 'GET', signal })
}
