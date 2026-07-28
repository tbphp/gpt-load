import type { ApiClient } from '@/api/client'

import { getRuntimeHealth } from './health'

describe('getRuntimeHealth', () => {
  it('requests GET /api/health and forwards AbortSignal', async () => {
    const signal = new AbortController().signal
    const request = vi.fn().mockResolvedValue({
      observed_at: '2026-07-29T01:02:03Z',
      snapshot_revision: 1,
      stats_window_seconds: 300,
      counts: { total: 0, available: 0, cooldown: 0, blacklisted: 0, disabled: 0 },
      groups: [],
      cooldown_keys: [],
      blacklisted_keys: [],
      request_log: {
        enqueued_total: 0,
        persisted_total: 0,
        dropped_not_running_total: 0,
        dropped_queue_full_total: 0,
        dropped_stopping_total: 0,
        dropped_persist_failed_total: 0,
        dropped_shutdown_total: 0,
        dropped_total: 0,
        write_failure_total: 0,
        retention_delete_failure_total: 0,
        queue_depth: 0,
        queue_capacity: 0,
        last_write_failure_at: null,
        last_retention_failure_at: null,
      },
    }) as ApiClient['request']
    const client: ApiClient = { request }

    await getRuntimeHealth(client, signal)

    expect(request).toHaveBeenCalledWith('/api/health', { method: 'GET', signal })
  })
})
