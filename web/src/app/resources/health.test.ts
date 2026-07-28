import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'

import { getRuntimeHealth, projectRuntimeHealth } from './health'

const zeroCounts = {
  total: 0,
  available: 0,
  cooldown: 0,
  blacklisted: 0,
  disabled: 0,
}

const requestLog = {
  enqueued_total: 1,
  persisted_total: 1,
  dropped_not_running_total: 0,
  dropped_queue_full_total: 0,
  dropped_stopping_total: 0,
  dropped_persist_failed_total: 0,
  dropped_shutdown_total: 0,
  dropped_total: 0,
  write_failure_total: 0,
  retention_delete_failure_total: 0,
  queue_depth: 0,
  queue_capacity: 128,
  last_write_failure_at: null,
  last_retention_failure_at: null,
}

const health = {
  observed_at: '2026-07-29T01:02:03Z',
  snapshot_revision: 9,
  stats_window_seconds: 300,
  counts: { total: 1, available: 0, cooldown: 1, blacklisted: 0, disabled: 0 },
  groups: [
    {
      id: 7,
      name: 'Primary',
      enabled: true,
      counts: { total: 1, available: 0, cooldown: 1, blacklisted: 0, disabled: 0 },
    },
  ],
  cooldown_keys: [
    {
      key_id: 11,
      group_id: 7,
      group_name: 'Primary',
      cooldown_until: '2026-07-29T01:03:03Z',
      failure_count: 2,
      recent_success_count: 3,
      recent_failure_count: 2,
      consecutive_failure_count: 1,
      weight_manual: null,
      weight_auto: 80,
      recovery: {
        automatic: true,
        mode: 'cooldown_expiry',
        at: '2026-07-29T01:03:03Z',
      },
    },
  ],
  blacklisted_keys: [],
  request_log: requestLog,
}

describe('Runtime Health resource', () => {
  it('projects the complete operational observation', () => {
    expect(projectRuntimeHealth(health)).toEqual(health)
    expect(
      projectRuntimeHealth({ ...health, counts: zeroCounts, groups: [], cooldown_keys: [] }),
    ).toMatchObject({ counts: zeroCounts, groups: [], cooldown_keys: [] })
  })

  it.each([
    { ...health, observed_at: 'not-an-instant' },
    { ...health, snapshot_revision: Number.MAX_SAFE_INTEGER + 1 },
    {
      ...health,
      counts: { total: 2, available: 0, cooldown: 1, blacklisted: 0, disabled: 0 },
    },
    {
      ...health,
      cooldown_keys: [
        { ...health.cooldown_keys[0], recovery: { automatic: true, mode: 'manual', at: null } },
      ],
    },
    {
      ...health,
      request_log: { ...requestLog, queue_depth: Number.POSITIVE_INFINITY },
    },
    { ...health, encryption_key: 'plaintext' },
  ])('rejects an unsafe health response %#j', (unsafe) => {
    expect(() => projectRuntimeHealth(unsafe)).toThrow(InvalidResponseError)
  })

  it('projects transport data before returning', async () => {
    const request = vi.fn().mockResolvedValue(health) as ApiClient['request']
    await expect(getRuntimeHealth({ request })).resolves.toEqual(health)
    expect(request).toHaveBeenCalledWith('/api/health', { method: 'GET', signal: undefined })
  })
})
