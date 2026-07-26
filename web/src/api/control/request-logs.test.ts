import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import { listRequestLogs, projectRequestLogPage } from './request-logs'

const item = {
  request_id: 'request-1',
  completed_at: '2026-07-27T12:00:00Z',
  access_key: { id: 4, name: null, deleted: false },
  protocol: 'openai',
  client_model: 'gpt-5.6',
  upstream_model: 'provider-gpt-5.6',
  status: 'success',
  status_code: 200,
  duration_ms: 42,
  error_code: '',
  error_summary: '',
  affinity_hit: true,
  attempts: [],
  group_id: 7,
  usage_state: 'complete',
  cost_state: 'priced',
  uncached_input_tokens: 100,
  cache_read_tokens: 20,
  cache_write_5m_tokens: 3,
  cache_write_1h_tokens: 4,
  output_tokens: 50,
  estimated_cost_usd: 0,
} as const

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

  it.each([
    ['complete priced', 'complete', 'priced', 0],
    ['complete unpriced', 'complete', 'unpriced', 0],
    ['partial priced', 'partial', 'priced', 0.123],
    ['partial unpriced', 'partial', 'unpriced', 0],
    ['missing unpriced', 'missing', 'unpriced', 0],
    ['not applicable', 'not_applicable', 'not_applicable', 0],
  ] as const)('projects %s usage/cost fields', (_name, usageState, costState, estimatedCost) => {
    expect(
      projectRequestLogPage({
        items: [
          {
            ...item,
            usage_state: usageState,
            cost_state: costState,
            estimated_cost_usd: estimatedCost,
          },
        ],
        next_cursor: null,
      }),
    ).toEqual({
      items: [
        {
          ...item,
          usage_state: usageState,
          cost_state: costState,
          estimated_cost_usd: estimatedCost,
        },
      ],
      next_cursor: null,
    })
  })

  it.each([
    ['missing usage state', { ...item, usage_state: undefined }],
    ['missing cost state', { ...item, cost_state: undefined }],
    ['missing token field', { ...item, output_tokens: undefined }],
    ['missing estimated cost', { ...item, estimated_cost_usd: undefined }],
    ['unsafe group id', { ...item, group_id: Number.MAX_SAFE_INTEGER + 1 }],
    ['unsafe token', { ...item, output_tokens: Number.MAX_SAFE_INTEGER + 1 }],
    ['negative token', { ...item, cache_read_tokens: -1 }],
    ['invalid usage state', { ...item, usage_state: 'unknown' }],
    ['invalid cost state', { ...item, cost_state: 'unknown' }],
    ['negative estimated cost', { ...item, estimated_cost_usd: -0.01 }],
  ])('rejects %s', (_name, invalidItem) => {
    expect(() => projectRequestLogPage({ items: [invalidItem], next_cursor: null })).toThrow(
      InvalidResponseError,
    )
  })
})
