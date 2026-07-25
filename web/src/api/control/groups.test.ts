import type { ApiClient } from '@/api/client'

import { listGroups } from './groups'

describe('listGroups', () => {
  it('requests GET /api/groups and forwards AbortSignal', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue([]) as ApiClient['request']
    const client: ApiClient = { request }

    await listGroups(client, signal)

    expect(request).toHaveBeenCalledWith('/api/groups', { method: 'GET', signal })
  })
})
