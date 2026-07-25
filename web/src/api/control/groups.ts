import type { ApiClient } from '@/api/client'

import type { GroupSummary } from './types'

export function listGroups(client: ApiClient, signal?: AbortSignal): Promise<GroupSummary[]> {
  return client.request<GroupSummary[]>('/api/groups', { method: 'GET', signal })
}
