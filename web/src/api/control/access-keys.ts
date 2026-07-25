import type { ApiClient } from '@/api/client'

import type { AccessKeyDto } from './types'

export function listAccessKeys(client: ApiClient, signal?: AbortSignal): Promise<AccessKeyDto[]> {
  return client.request<AccessKeyDto[]>('/api/access-keys', { method: 'GET', signal })
}
