import { ApiError, InvalidResponseError, NetworkError, RequestCancelledError } from '@/api/errors'

type OperationKind = 'access_key_create' | 'group_create' | 'group_key_import'

interface IncompleteOperation {
  operation_id: string
  operation_kind: OperationKind
  last_completed_stage: string
  failed_stage: string
}

export type MutationOutcome<T> =
  | { kind: 'confirmed'; value: T }
  | { kind: 'reconciling'; operation: IncompleteOperation }
  | {
      kind: 'failed'
      reason: 'retryable-precondition'
      retry_after_ms: number
      operation_id: string
    }
  | {
      kind: 'failed'
      reason: 'expired-known'
      operation_id: string
      resource_identity: string
    }
  | { kind: 'failed'; reason: 'cancelled-before-send' }
  | { kind: 'failed'; reason: 'rejected'; code: string }
  | { kind: 'indeterminate'; reason: 'transport' | 'cancelled-after-send' }

export type MutationSettlement<T> =
  { kind: 'success'; value: T } | { kind: 'error'; error: unknown; requestSent: boolean }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isOperationKind(value: unknown): value is OperationKind {
  return ['access_key_create', 'group_create', 'group_key_import'].includes(String(value))
}

function incompleteOperation(data: unknown): IncompleteOperation | undefined {
  if (
    !isRecord(data) ||
    typeof data.operation_id !== 'string' ||
    !isOperationKind(data.operation_kind) ||
    typeof data.last_completed_stage !== 'string' ||
    typeof data.failed_stage !== 'string' ||
    data.can_reconcile !== true
  ) {
    return undefined
  }
  return {
    operation_id: data.operation_id,
    operation_kind: data.operation_kind,
    last_completed_stage: data.last_completed_stage,
    failed_stage: data.failed_stage,
  }
}

export function classifyMutationOutcome<T>(settlement: MutationSettlement<T>): MutationOutcome<T> {
  if (settlement.kind === 'success') {
    return { kind: 'confirmed', value: settlement.value }
  }

  const { error, requestSent } = settlement
  if (error instanceof RequestCancelledError) {
    return requestSent
      ? { kind: 'indeterminate', reason: 'cancelled-after-send' }
      : { kind: 'failed', reason: 'cancelled-before-send' }
  }
  if (error instanceof NetworkError || error instanceof InvalidResponseError) {
    return { kind: 'indeterminate', reason: 'transport' }
  }
  if (!(error instanceof ApiError)) {
    return { kind: 'indeterminate', reason: 'transport' }
  }

  if (error.status === 503 && error.code === 'CONTROL_OPERATION_INCOMPLETE') {
    const operation = incompleteOperation(error.data)
    return operation
      ? { kind: 'reconciling', operation }
      : { kind: 'indeterminate', reason: 'transport' }
  }
  if (
    error.status === 503 &&
    error.code === 'CONTROL_RECOVERY_PENDING' &&
    isRecord(error.data) &&
    typeof error.data.operation_id === 'string' &&
    typeof error.data.retry_after_ms === 'number' &&
    Number.isSafeInteger(error.data.retry_after_ms) &&
    error.data.retry_after_ms >= 0
  ) {
    return {
      kind: 'failed',
      reason: 'retryable-precondition',
      retry_after_ms: error.data.retry_after_ms,
      operation_id: error.data.operation_id,
    }
  }
  if (
    error.status === 410 &&
    error.code === 'IDEMPOTENCY_RESULT_EXPIRED' &&
    isRecord(error.data) &&
    typeof error.data.operation_id === 'string' &&
    typeof error.data.resource_identity === 'string' &&
    error.data.resource_identity !== ''
  ) {
    return {
      kind: 'failed',
      reason: 'expired-known',
      operation_id: error.data.operation_id,
      resource_identity: error.data.resource_identity,
    }
  }
  if (error.status >= 500) {
    return { kind: 'indeterminate', reason: 'transport' }
  }
  return { kind: 'failed', reason: 'rejected', code: error.code }
}
