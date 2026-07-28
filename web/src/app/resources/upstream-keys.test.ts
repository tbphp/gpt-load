import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  listGroupKeys,
  projectUpstreamKey,
  updateGroupKey,
  upstreamKeyMutationInvalidations,
} from './upstream-keys'

const key = {
  id: 11,
  group_id: 7,
  mask: 'sk-p****a1b2',
  status: 'active',
  effective_status: 'cooldown',
  weight_manual: null,
  weight_auto: 72,
  blacklisted: false,
  cooldown_until: '2026-07-29T01:02:03Z',
  failure_count: 2,
}

describe('UpstreamKey resource', () => {
  it('projects metadata without plaintext key material', () => {
    expect(projectUpstreamKey(key)).toEqual(key)
    expect(projectUpstreamKey({ ...key, mask: '****', cooldown_until: null })).toMatchObject({
      mask: '****',
      cooldown_until: null,
    })
  })

  it.each([
    { ...key, id: 0 },
    { ...key, group_id: Number.MAX_SAFE_INTEGER + 1 },
    { ...key, mask: 'plaintext-upstream-key' },
    { ...key, status: 'paused' },
    { ...key, effective_status: 'unknown' },
    { ...key, weight_auto: Number.POSITIVE_INFINITY },
    { ...key, cooldown_until: 'tomorrow' },
    { ...key, plaintext_key: 'UPSTREAM_KEY_CANARY' },
  ])('rejects unsafe key metadata %#j', (unsafe) => {
    expect(() => projectUpstreamKey(unsafe)).toThrow(InvalidResponseError)
  })

  it('projects list and update responses and owns exact mutation invalidations', async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce([key])
      .mockResolvedValueOnce({
        ...key,
        status: 'disabled',
        effective_status: 'disabled',
      }) as ApiClient['request']

    await expect(listGroupKeys({ request }, 7)).resolves.toEqual([key])
    await expect(updateGroupKey({ request }, 7, 11, { status: 'disabled' })).resolves.toMatchObject(
      { id: 11, status: 'disabled', effective_status: 'disabled' },
    )
    expect(upstreamKeyMutationInvalidations.update(7)).toEqual([
      controlQueryKeys.groups.keys(7),
      controlQueryKeys.groups.detail(7),
      controlQueryKeys.groups.list(),
      controlQueryKeys.health(),
    ])
  })
})
