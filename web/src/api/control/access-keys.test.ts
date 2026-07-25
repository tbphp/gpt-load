import type { ApiClient } from '@/api/client'

import { listAccessKeys } from './access-keys'

describe('listAccessKeys', () => {
  it('requests GET /api/access-keys and forwards AbortSignal', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue([]) as ApiClient['request']
    const client: ApiClient = { request }

    await listAccessKeys(client, signal)

    expect(request).toHaveBeenCalledWith('/api/access-keys', { method: 'GET', signal })
  })
})
