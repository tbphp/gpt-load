import { ApiError, InvalidResponseError, NetworkError, RequestCancelledError } from '@/api/errors'

import { classifyMutationOutcome } from './mutation-outcome'

describe('mutation outcome classifier', () => {
  it('confirms only a valid success result', () => {
    expect(classifyMutationOutcome({ kind: 'success', value: { id: 7 } })).toEqual({
      kind: 'confirmed',
      value: { id: 7 },
    })
  })

  it('moves committed incomplete operations to reconciliation', () => {
    const error = new ApiError(503, 'CONTROL_OPERATION_INCOMPLETE', 'recovering', {
      operation_id: 'op-1',
      operation_kind: 'group_create',
      last_completed_stage: 'db_committed',
      failed_stage: 'registry_applied',
      can_reconcile: true,
    })

    expect(classifyMutationOutcome({ kind: 'error', error, requestSent: true })).toEqual({
      kind: 'reconciling',
      operation: {
        operation_id: 'op-1',
        operation_kind: 'group_create',
        last_completed_stage: 'db_committed',
        failed_stage: 'registry_applied',
      },
    })
  })

  it('keeps recovery barriers retryable with the same operation identity', () => {
    const error = new ApiError(503, 'CONTROL_RECOVERY_PENDING', 'pending', {
      operation_id: 'op-earlier',
      operation_kind: 'group_key_import',
      failed_stage: 'registry_applied',
      retry_after_ms: 750,
    })

    expect(classifyMutationOutcome({ kind: 'error', error, requestSent: true })).toEqual({
      kind: 'failed',
      reason: 'retryable-precondition',
      retry_after_ms: 750,
      operation_id: 'op-earlier',
    })
  })

  it('treats compacted results as known-expired and exposes only resource identity', () => {
    const error = new ApiError(410, 'IDEMPOTENCY_RESULT_EXPIRED', 'expired', {
      operation_id: 'op-old',
      operation_kind: 'access_key_create',
      resource_identity: 'access-key:42',
      completed_at: '2026-07-20T00:00:00Z',
    })

    expect(classifyMutationOutcome({ kind: 'error', error, requestSent: true })).toEqual({
      kind: 'failed',
      reason: 'expired-known',
      operation_id: 'op-old',
      resource_identity: 'access-key:42',
    })
  })

  it.each([
    new ApiError(500, 'INTERNAL_SERVER_ERROR', 'unknown'),
    new ApiError(502, 'BAD_GATEWAY', 'unknown'),
    new NetworkError(),
    new InvalidResponseError(),
    new RequestCancelledError(),
  ])('does not misreport an unknown transport outcome as failed: %s', (error) => {
    expect(classifyMutationOutcome({ kind: 'error', error, requestSent: true })).toEqual({
      kind: 'indeterminate',
      reason: error instanceof RequestCancelledError ? 'cancelled-after-send' : 'transport',
    })
  })

  it('distinguishes safe pre-send cancellation and explicit rejection', () => {
    expect(
      classifyMutationOutcome({
        kind: 'error',
        error: new RequestCancelledError(),
        requestSent: false,
      }),
    ).toEqual({ kind: 'failed', reason: 'cancelled-before-send' })

    expect(
      classifyMutationOutcome({
        kind: 'error',
        error: new ApiError(400, 'VALIDATION_FAILED', 'invalid'),
        requestSent: true,
      }),
    ).toEqual({ kind: 'failed', reason: 'rejected', code: 'VALIDATION_FAILED' })
  })
})
