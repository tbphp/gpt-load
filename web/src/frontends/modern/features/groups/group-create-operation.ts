import { computed, onScopeDispose, ref, shallowRef } from 'vue'
import type { ApiClient } from '@shared/http/client'
import { ApiError } from '@shared/http/errors'
import {
  appendAPIKeyCredentials,
  createGroup,
  type GroupCreateRequest,
  type GroupCreateResult,
} from '@modern/api/group-create'
import { connectCredentialStages } from '@modern/api/credential-stages'

type Submission =
  | { kind: 'create'; request: GroupCreateRequest }
  | { kind: 'append'; group: { id: number; name: string }; credentials: string }
  | { kind: 'connect'; group: { id: number; name: string }; stageIDs: string[] }
type Outcome =
  | { kind: 'success'; result: GroupCreateResult; appended: boolean }
  | { kind: 'rejected'; error: ApiError }
  | { kind: 'unknown' | 'reconciling' | 'waiting' }
  | { kind: 'expired'; groupID?: number }

export function createOperationKey(): string {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  const bytes = new Uint8Array(16)
  globalThis.crypto.getRandomValues(bytes)
  bytes[6] = (bytes[6]! & 15) | 64
  bytes[8] = (bytes[8]! & 63) | 128
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
  return [
    hex.slice(0, 8),
    hex.slice(8, 12),
    hex.slice(12, 16),
    hex.slice(16, 20),
    hex.slice(20),
  ].join('-')
}

export function useGroupCreateOperation(client: ApiClient) {
  const operation = shallowRef<{ key: string; payload: Submission }>()
  const outcome = shallowRef<Outcome>()
  const pending = ref(false)
  const waiting = ref(false)
  let controller: AbortController | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  const canRetry = computed(
    () =>
      Boolean(operation.value) &&
      !pending.value &&
      !waiting.value &&
      outcome.value?.kind !== 'expired',
  )
  function reset(): void {
    controller?.abort()
    clearTimeout(timer)
    operation.value = undefined
    outcome.value = undefined
    pending.value = false
    waiting.value = false
  }
  function begin(payload: Submission): void {
    if (operation.value) return
    operation.value = { key: createOperationKey(), payload: structuredClone(payload) }
    outcome.value = undefined
  }
  async function execute(): Promise<Outcome | undefined> {
    const current = operation.value
    if (!current || !canRetry.value) return
    const request = new AbortController()
    controller = request
    pending.value = true
    outcome.value = undefined
    let result: Outcome
    try {
      const payload = current.payload
      const data =
        payload.kind === 'create'
          ? await createGroup(client, payload.request, current.key, request.signal)
          : payload.kind === 'connect'
            ? await connectCredentialStages(
                client,
                payload.group,
                payload.stageIDs,
                current.key,
                request.signal,
              )
            : await appendAPIKeyCredentials(
                client,
                payload.group,
                payload.credentials,
                current.key,
                request.signal,
              )
      result = { kind: 'success', result: data, appended: payload.kind !== 'create' }
    } catch (error) {
      if (request.signal.aborted || operation.value !== current) return
      const data =
        error instanceof ApiError && error.data && typeof error.data === 'object'
          ? (error.data as Record<string, unknown>)
          : undefined
      if (error instanceof ApiError && error.code === 'IDEMPOTENCY_RESULT_EXPIRED') {
        const match =
          typeof data?.resource_identity === 'string'
            ? /^group:(\d+)$/u.exec(data.resource_identity)
            : null
        const id = match ? Number(match[1]) : 0
        result = { kind: 'expired', groupID: Number.isSafeInteger(id) && id > 0 ? id : undefined }
      } else if (
        error instanceof ApiError &&
        error.status === 503 &&
        error.code === 'CONTROL_RECOVERY_PENDING'
      ) {
        const delay = data?.retry_after_ms
        if (typeof delay === 'number' && Number.isSafeInteger(delay) && delay >= 0) {
          waiting.value = true
          // 只到期开放手动重试，不发送自动请求。
          timer = setTimeout(() => {
            waiting.value = false
          }, delay)
        }
        result = { kind: 'waiting' }
      } else if (error instanceof ApiError && error.status >= 500) {
        result = { kind: error.code === 'CONTROL_OPERATION_INCOMPLETE' ? 'reconciling' : 'unknown' }
      } else if (error instanceof ApiError) {
        result = { kind: 'rejected', error }
      } else {
        result = { kind: 'unknown' }
      }
    } finally {
      if (operation.value === current) pending.value = false
    }
    if (request.signal.aborted || operation.value !== current) return
    outcome.value = result
    return result
  }
  onScopeDispose(reset)
  return { operation, outcome, pending, canRetry, waiting, begin, execute, reset }
}
