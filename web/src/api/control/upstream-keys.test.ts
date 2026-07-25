import type { ApiClient } from '@/api/client'

import {
  deleteGroupKey,
  listGroupKeys,
  updateGroupKey,
  type UpstreamKeyPatch,
} from './upstream-keys'

describe('upstream-key control API', () => {
  it('uses the exact Group-key list, partial update, and delete contracts', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue([]) as ApiClient['request']
    const client = { request }

    await listGroupKeys(client, 7, signal)
    await updateGroupKey(client, 7, 11, { status: 'disabled' }, signal)
    await updateGroupKey(client, 7, 11, { weight_manual: null }, signal)
    await deleteGroupKey(client, 7, 11, signal)

    expect(request).toHaveBeenNthCalledWith(1, '/api/groups/7/keys', {
      method: 'GET',
      signal,
    })
    expect(request).toHaveBeenNthCalledWith(2, '/api/groups/7/keys/11', {
      method: 'PUT',
      json: { status: 'disabled' },
      signal,
    })
    expect(request).toHaveBeenNthCalledWith(3, '/api/groups/7/keys/11', {
      method: 'PUT',
      json: { weight_manual: null },
      signal,
    })
    expect(request).toHaveBeenNthCalledWith(4, '/api/groups/7/keys/11', {
      method: 'DELETE',
      signal,
    })
  })

  it.each([
    {},
    { weight_manual: 0 },
    { weight_manual: 101 },
    { status: 'unknown' },
    { status: 'active', plaintext: 'UPSTREAM_KEY_CANARY' },
  ])('rejects unsafe or empty update body %j before transport', async (patch) => {
    const request = vi.fn() as ApiClient['request']

    await expect(updateGroupKey({ request }, 7, 11, patch as UpstreamKeyPatch)).rejects.toThrow()
    expect(request).not.toHaveBeenCalled()
  })
})
