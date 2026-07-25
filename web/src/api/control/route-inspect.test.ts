import type { ApiClient } from '@/api/client'

import { inspectRoute } from './route-inspect'

describe('Route inspect control API', () => {
  it('posts the exact route-inspection request and forwards AbortSignal', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue({}) as ApiClient['request']
    const client: ApiClient = { request }
    const body = { protocol: 'openai' as const, external_model: 'gpt-real', access_key_id: 12 }

    await inspectRoute(client, body, signal)

    expect(request).toHaveBeenCalledWith('/api/route/inspect', {
      method: 'POST',
      json: { protocol: 'openai', external_model: 'gpt-real', access_key_id: 12 },
      signal,
    })
  })
})
