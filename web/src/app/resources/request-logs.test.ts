import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  listRequestLogs,
  projectRequestLogPage,
  requestLogQueryIdentity,
  type RequestLogFilters,
} from './request-logs'

const requestID = 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173'
const item = {
  request_id: requestID,
  completed_at: '2026-07-29T01:02:03Z',
  access_key: { id: 4, name: 'Client', deleted: false },
  protocol: 'openai',
  client_model: 'gpt-client',
  upstream_model: 'gpt-upstream',
  status: 'success',
  status_code: 200,
  duration_ms: 42,
  error_code: '',
  error_summary: '',
  affinity_hit: true,
  attempts: [
    {
      sequence: 1,
      group_id: 7,
      group_name: 'Primary',
      key_id: 11,
      upstream_model: 'gpt-upstream',
      status_code: 200,
      duration_ms: 40,
      failure_category: 'ok',
      action: 'terminate',
      will_retry: false,
      error_code: '',
      error_summary: '',
      committed: true,
    },
  ],
  group_id: 7,
  usage_state: 'complete',
  cost_state: 'priced',
  uncached_input_tokens: 100,
  cache_read_tokens: 20,
  cache_write_5m_tokens: 3,
  cache_write_1h_tokens: 4,
  output_tokens: 50,
  estimated_cost_usd: 0.125,
}

describe('RequestLog resource', () => {
  it('projects every rendered item and attempt field', () => {
    expect(projectRequestLogPage({ items: [item], next_cursor: 'opaque-cursor' })).toEqual({
      items: [item],
      next_cursor: 'opaque-cursor',
    })
  })

  it.each([
    { ...item, request_id: 'not-a-uuid' },
    { ...item, completed_at: 'tomorrow' },
    { ...item, protocol: 'openai-response' },
    { ...item, status: 'unknown' },
    { ...item, duration_ms: Number.POSITIVE_INFINITY },
    { ...item, access_key: { ...item.access_key, key: 'plaintext' } },
    { ...item, attempts: [{ ...item.attempts[0], action: 'future_action' }] },
    { ...item, attempts: [{ ...item.attempts[0], secret_token: 'plaintext' }] },
  ])('rejects an unsafe rendered log field %#j', (unsafe) => {
    expect(() => projectRequestLogPage({ items: [unsafe], next_cursor: null })).toThrow(
      InvalidResponseError,
    )
  })

  it('rejects a legacy upstream key mask instead of rendering it', () => {
    const unsafe = {
      ...item,
      attempts: [{ ...item.attempts[0], key_mask: 'sk-u****safe' }],
    }

    expect(() => projectRequestLogPage({ items: [unsafe], next_cursor: null })).toThrow(
      InvalidResponseError,
    )
  })

  it('keeps drawer selection outside both list identity and transport query', async () => {
    const filters = {
      status: 'error',
      selected_request_id: requestID,
    } as RequestLogFilters & { selected_request_id: string }
    expect(requestLogQueryIdentity(filters)).toEqual(
      controlQueryKeys.logs.list({ status: 'error' }),
    )

    const request = vi
      .fn()
      .mockResolvedValue({ items: [], next_cursor: null }) as ApiClient['request']
    await listRequestLogs({ request }, filters)
    expect(request).toHaveBeenCalledWith('/api/logs?status=error', {
      method: 'GET',
      signal: undefined,
    })
  })
})
