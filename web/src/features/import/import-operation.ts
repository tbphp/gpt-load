import { computed, getCurrentInstance, onBeforeUnmount, ref, shallowRef } from 'vue'

import { classifyMutationOutcome, type MutationOutcome } from '@/app/mutation-outcome'

export interface StableImportOperation<TPayload> {
  idempotencyKey: string
  payload: TPayload
}

export interface StableImportOperationOptions<TPayload> {
  clonePayload?(payload: TPayload): TPayload
  now?(): number
  randomUUID?(): string
  setTimer?(callback: () => void, delayMs: number): ReturnType<typeof setTimeout>
  clearTimer?(timer: ReturnType<typeof setTimeout>): void
}

export function useStableImportOperation<TPayload, TResult>(
  options: StableImportOperationOptions<TPayload> = {},
) {
  const clonePayload = options.clonePayload ?? ((payload: TPayload) => structuredClone(payload))
  const now = options.now ?? Date.now
  const randomUUID = options.randomUUID ?? (() => crypto.randomUUID())
  const setTimer = options.setTimer ?? setTimeout
  const clearTimer = options.clearTimer ?? clearTimeout

  const operation = shallowRef<StableImportOperation<TPayload> | null>(null)
  const outcome = shallowRef<MutationOutcome<TResult> | null>(null)
  const lastError = shallowRef<unknown>()
  const pending = ref(false)
  const retryReadyAt = ref(0)
  const clock = ref(now())
  let activeController: AbortController | undefined
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let owner = 0

  function clearRetryTimer(): void {
    if (retryTimer !== undefined) {
      clearTimer(retryTimer)
      retryTimer = undefined
    }
  }

  function scheduleRetry(delayMs: number): void {
    clearRetryTimer()
    clock.value = now()
    retryReadyAt.value = clock.value + delayMs
    if (delayMs === 0) return
    retryTimer = setTimer(() => {
      retryTimer = undefined
      clock.value = now()
    }, delayMs)
  }

  function begin(payload: TPayload): StableImportOperation<TPayload> {
    if (operation.value) return operation.value
    operation.value = {
      idempotencyKey: randomUUID(),
      payload: clonePayload(payload),
    }
    outcome.value = null
    lastError.value = undefined
    retryReadyAt.value = 0
    return operation.value
  }

  function reset(): void {
    owner += 1
    activeController?.abort()
    activeController = undefined
    clearRetryTimer()
    operation.value = null
    outcome.value = null
    lastError.value = undefined
    retryReadyAt.value = 0
    pending.value = false
  }

  const canRetry = computed(
    () => !pending.value && operation.value !== null && clock.value >= retryReadyAt.value,
  )

  async function execute(
    send: (operation: StableImportOperation<TPayload>, signal: AbortSignal) => Promise<TResult>,
  ): Promise<MutationOutcome<TResult> | null> {
    const current = operation.value
    if (!current || !canRetry.value) return null

    const controller = new AbortController()
    activeController = controller
    const executionOwner = ++owner
    pending.value = true
    lastError.value = undefined
    try {
      const value = await send(current, controller.signal)
      if (owner !== executionOwner || activeController !== controller) return null
      const classified = classifyMutationOutcome<TResult>({ kind: 'success', value })
      outcome.value = classified
      return classified
    } catch (error: unknown) {
      if (owner !== executionOwner || activeController !== controller) return null
      lastError.value = error
      const classified = classifyMutationOutcome<TResult>({
        kind: 'error',
        error,
        requestSent: true,
      })
      outcome.value = classified
      if (classified.kind === 'failed' && classified.reason === 'retryable-precondition') {
        scheduleRetry(classified.retry_after_ms)
      }
      return classified
    } finally {
      if (owner === executionOwner && activeController === controller) {
        activeController = undefined
        pending.value = false
      }
    }
  }

  function dispose(): void {
    owner += 1
    activeController?.abort()
    activeController = undefined
    clearRetryTimer()
    pending.value = false
  }

  if (getCurrentInstance()) onBeforeUnmount(dispose)

  return {
    operation,
    outcome,
    lastError,
    pending,
    retryReadyAt,
    canRetry,
    begin,
    execute,
    reset,
    dispose,
  }
}
