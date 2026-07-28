import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import { inspectRoute, projectRouteInspection } from './route-inspection'

const inspection = {
  observed_at: '2026-07-29T01:02:03Z',
  snapshot_revision: 9,
  protocol: 'openai',
  external_model: 'gpt-client',
  access_key: { id: 12, name: 'Client', status: 'active' },
  routable: true,
  reason_code: null,
  groups: [
    {
      group_id: 7,
      group_name: 'Primary',
      upstream_model: 'gpt-upstream',
      weight_manual: null,
      included: true,
      routable: true,
      reason_code: null,
      keys: [
        {
          key_id: 11,
          available: true,
          reason_code: null,
          weight_manual: null,
          weight_auto: 50,
          effective_weight: 2500,
          cooldown_until: null,
        },
      ],
    },
  ],
}

describe('Route Inspection resource', () => {
  it('projects the complete explanation without deriving presentation state', () => {
    expect(projectRouteInspection(inspection)).toEqual(inspection)
  })

  it.each([
    { ...inspection, protocol: 'openai-response' },
    { ...inspection, observed_at: 'later' },
    { ...inspection, access_key: { ...inspection.access_key, status: 'paused' } },
    { ...inspection, reason_code: 'future_reason' },
    {
      ...inspection,
      groups: [{ ...inspection.groups[0], group_id: Number.MAX_SAFE_INTEGER + 1 }],
    },
    {
      ...inspection,
      groups: [
        {
          ...inspection.groups[0],
          keys: [{ ...inspection.groups[0].keys[0], effective_weight: Number.NaN }],
        },
      ],
    },
    {
      ...inspection,
      groups: [
        {
          ...inspection.groups[0],
          keys: [{ ...inspection.groups[0].keys[0], mask: 'sensitive-derived-value' }],
        },
      ],
    },
    { ...inspection, access_token: 'plaintext' },
  ])('rejects an unsafe route explanation %#j', (unsafe) => {
    expect(() => projectRouteInspection(unsafe)).toThrow(InvalidResponseError)
  })

  it('serializes the strict request and projects the response', async () => {
    const request = vi.fn().mockResolvedValue(inspection) as ApiClient['request']
    const body = {
      protocol: 'openai' as const,
      external_model: 'gpt-client',
      access_key_id: 12,
    }
    await expect(inspectRoute({ request }, body)).resolves.toEqual(inspection)
    expect(request).toHaveBeenCalledWith('/api/route/inspect', {
      method: 'POST',
      json: body,
      signal: undefined,
    })
  })
})
