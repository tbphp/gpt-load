import { effectScope } from 'vue'

import { ApiError, NetworkError } from '@/api/errors'

import { useStableImportOperation } from './import-operation'

function harness() {
  let now = 1_000
  const timers = new Map<number, () => void>()
  let timerID = 0
  let uuid = 0
  const scope = effectScope()
  const operation = scope.run(() =>
    useStableImportOperation<{ keys: string }, { group_id: number }>({
      now: () => now,
      randomUUID: () => `00000000-0000-4000-8000-${String(++uuid).padStart(12, '0')}`,
      setTimer(callback) {
        const id = ++timerID
        timers.set(id, callback)
        return id as ReturnType<typeof setTimeout>
      },
      clearTimer(timer) {
        timers.delete(timer as number)
      },
    }),
  )!
  return {
    operation,
    scope,
    timers,
    advance(ms: number) {
      now += ms
      for (const callback of [...timers.values()]) callback()
      timers.clear()
    },
  }
}

describe('stable import operation', () => {
  it('freezes one payload and UUID across indeterminate and reconciliation retries', async () => {
    const { operation, scope } = harness()
    const first = operation.begin({ keys: 'first' })
    expect(operation.begin({ keys: 'changed' })).toBe(first)
    const send = vi
      .fn()
      .mockRejectedValueOnce(new NetworkError())
      .mockRejectedValueOnce(
        new ApiError(503, 'CONTROL_OPERATION_INCOMPLETE', 'recovering', {
          operation_id: 'server-operation',
          operation_kind: 'group_key_import',
          last_completed_stage: 'db_committed',
          failed_stage: 'registry_applied',
          can_reconcile: true,
        }),
      )
      .mockResolvedValueOnce({ group_id: 7 })

    expect(await operation.execute(send)).toMatchObject({ kind: 'indeterminate' })
    expect(await operation.execute(send)).toMatchObject({ kind: 'reconciling' })
    expect(await operation.execute(send)).toEqual({
      kind: 'confirmed',
      value: { group_id: 7 },
    })
    expect(send.mock.calls.map(([current]) => current)).toEqual([first, first, first])
    expect(first.payload).toEqual({ keys: 'first' })
    scope.stop()
  })

  it('honors retry_after_ms without automatically sending or allocating another identity', async () => {
    const { advance, operation, scope, timers } = harness()
    const first = operation.begin({ keys: 'first' })
    const send = vi
      .fn()
      .mockRejectedValueOnce(
        new ApiError(503, 'CONTROL_RECOVERY_PENDING', 'pending', {
          operation_id: 'earlier-operation',
          retry_after_ms: 750,
        }),
      )
      .mockResolvedValueOnce({ group_id: 7 })

    expect(await operation.execute(send)).toMatchObject({
      kind: 'failed',
      reason: 'retryable-precondition',
      retry_after_ms: 750,
    })
    expect(operation.canRetry.value).toBe(false)
    expect(timers.size).toBe(1)
    expect(await operation.execute(send)).toBeNull()
    expect(send).toHaveBeenCalledTimes(1)

    advance(750)
    expect(operation.canRetry.value).toBe(true)
    await operation.execute(send)
    expect(send.mock.calls[1]?.[0]).toBe(first)
    scope.stop()
  })

  it('aborts late ownership and reset allocates a fresh operation', async () => {
    const { operation, scope } = harness()
    let resolve!: (value: { group_id: number }) => void
    const late = new Promise<{ group_id: number }>((settle) => {
      resolve = settle
    })
    const first = operation.begin({ keys: 'first' })
    const running = operation.execute(() => late)
    operation.reset()
    const second = operation.begin({ keys: 'second' })
    resolve({ group_id: 7 })

    expect(await running).toBeNull()
    expect(first.idempotencyKey).not.toBe(second.idempotencyKey)
    expect(operation.outcome.value).toBeNull()
    scope.stop()
  })
})
