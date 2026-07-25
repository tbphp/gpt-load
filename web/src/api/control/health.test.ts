import type { ApiClient } from '@/api/client'

import { getRuntimeHealth } from './health'

describe('getRuntimeHealth', () => {
  it('requests GET /api/health and forwards AbortSignal', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue({}) as ApiClient['request']
    const client: ApiClient = { request }

    await getRuntimeHealth(client, signal)

    expect(request).toHaveBeenCalledWith('/api/health', { method: 'GET', signal })
  })
})
