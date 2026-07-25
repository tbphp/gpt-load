import type { ApiClient } from '@/api/client'

import { listRequestLogs } from './request-logs'

describe('Request log control API', () => {
  it('serializes supported filters in the approved order and forwards AbortSignal', async () => {
    const signal = new AbortController().signal
    const request = vi
      .fn()
      .mockResolvedValue({ items: [], next_cursor: null }) as ApiClient['request']
    const client: ApiClient = { request }

    await listRequestLogs(
      client,
      { from: '2026-07-25T10:00:00.000Z', group_id: 7, status: 'error' },
      'opaque',
      signal,
    )

    expect(request).toHaveBeenCalledWith(
      '/api/logs?from=2026-07-25T10%3A00%3A00.000Z&group_id=7&status=error&cursor=opaque',
      { method: 'GET', signal },
    )
  })
})
