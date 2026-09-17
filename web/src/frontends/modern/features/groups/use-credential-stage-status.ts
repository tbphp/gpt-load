import { onScopeDispose, ref, watch, type Ref } from 'vue'
import type { ApiClient } from '@shared/http/client'
import {
  checkDeviceAuthorization,
  readCredentialStage,
  type CredentialStage,
} from '@modern/api/credential-stages'

interface AuthorizationPoll {
  timer?: ReturnType<typeof setTimeout>
  controller?: AbortController
  failures: number
}

// 用户确认的唯一轮询例外：仅在订阅授权等待、兑换期间沿用旧版机制。
// 其他列表、已就绪账号及全局 QueryClient 均不轮询。
const POLL_FAILURE_LIMIT = 5

export function useCredentialStageStatus(
  client: ApiClient,
  stages: Ref<CredentialStage[]>,
  paused: (id: string) => boolean,
) {
  const now = ref(Date.now())
  const failures = ref<Record<string, number>>({})
  const polling = new Map<string, AuthorizationPoll>()
  let expiryTimer: ReturnType<typeof setTimeout> | undefined
  let disposed = false
  function problem(id: string): 'stopped' | 'retrying' | undefined {
    const count = failures.value[id] ?? 0
    if (count >= POLL_FAILURE_LIMIT) return 'stopped'
    return count ? 'retrying' : undefined
  }
  function shouldPoll(stage: CredentialStage): boolean {
    return stage.status === 'pending_authorization' || stage.status === 'exchanging'
  }
  function merge(updates: readonly CredentialStage[]): void {
    const remaining = new Map(updates.map((stage) => [stage.id, stage]))
    stages.value = stages.value
      .map((stage) => {
        const update = remaining.get(stage.id)
        remaining.delete(stage.id)
        if (!update) return stage
        return {
          ...update,
          method: update.method ?? stage.method,
          ...(update.status === 'pending_authorization'
            ? {
                authorizationURL: update.authorizationURL ?? stage.authorizationURL,
                redirectURI: update.redirectURI ?? stage.redirectURI,
                userCode: update.userCode ?? stage.userCode,
                nextPollAt: update.nextPollAt ?? stage.nextPollAt,
              }
            : {}),
        }
      })
      .concat([...remaining.values()])
  }
  function stopRequest(state: AuthorizationPoll): void {
    clearTimeout(state.timer)
    state.timer = undefined
    state.controller?.abort()
    state.controller = undefined
  }
  function stopPolling(id: string): void {
    const state = polling.get(id)
    if (state) stopRequest(state)
    polling.delete(id)
    delete failures.value[id]
  }
  function delay(stage: CredentialStage, initial = false): number {
    if (
      stage.method === 'device_oauth' &&
      stage.status === 'pending_authorization' &&
      stage.nextPollAt
    )
      return Math.max(250, stage.nextPollAt - Date.now())
    return initial ? 800 : 1200
  }
  function schedule(stage: CredentialStage, state: AuthorizationPoll, wait: number): void {
    if (disposed || paused(stage.id) || state.failures >= POLL_FAILURE_LIMIT) return
    state.timer = setTimeout(() => void poll(stage.id, state), wait)
  }
  async function poll(id: string, state: AuthorizationPoll): Promise<void> {
    state.timer = undefined
    const current = stages.value.find((stage) => stage.id === id)
    if (disposed || paused(id) || polling.get(id) !== state || !current || !shouldPoll(current))
      return
    // 状态同步可能更新服务商间隔，发请求前再核对一次。
    if (
      current.method === 'device_oauth' &&
      current.status === 'pending_authorization' &&
      (current.nextPollAt ?? 0) > Date.now()
    ) {
      schedule(current, state, delay(current))
      return
    }
    const controller = new AbortController()
    state.controller = controller
    let latest = current
    try {
      const result =
        current.method === 'device_oauth' && current.status === 'pending_authorization'
          ? await checkDeviceAuthorization(client, id, controller.signal)
          : await readCredentialStage(client, id, controller.signal)
      if (disposed || controller.signal.aborted || polling.get(id) !== state) return
      latest = result
      state.failures = 0
      delete failures.value[id]
      merge([result])
      if (!shouldPoll(result)) stopPolling(id)
    } catch {
      if (!controller.signal.aborted && polling.get(id) === state) {
        state.failures++
        failures.value[id] = state.failures
      }
    } finally {
      if (state.controller === controller) state.controller = undefined
      if (!controller.signal.aborted && polling.get(id) === state && shouldPoll(latest)) {
        schedule(
          latest,
          state,
          state.failures ? 1200 * Math.min(state.failures + 1, 4) : delay(latest),
        )
      }
    }
  }
  function syncPolling(): void {
    const active = new Map(stages.value.filter(shouldPoll).map((stage) => [stage.id, stage]))
    for (const id of polling.keys()) if (!active.has(id)) stopPolling(id)
    for (const [id, stage] of active) {
      let state = polling.get(id)
      if (!state) {
        state = { failures: 0 }
        polling.set(id, state)
      }
      if (paused(id)) stopRequest(state)
      else if (!state.controller && state.timer === undefined)
        schedule(stage, state, delay(stage, true))
    }
  }
  watch(
    [
      stages,
      () =>
        stages.value
          .filter((stage) => paused(stage.id))
          .map((stage) => stage.id)
          .join(','),
    ],
    syncPolling,
    { immediate: true },
  )
  function confirm(stage: CredentialStage): void {
    // 提交回调后合并结果，并清理当前会话之前的同步错误。
    stopPolling(stage.id)
    merge([stage])
  }
  // 到期计时只更新本地数据；就绪账号不再发送自动请求。
  watch(
    stages,
    () => {
      clearTimeout(expiryTimer)
      now.value = Date.now()
      const deadlines = stages.value
        .flatMap((stage) => [
          ...(['pending_authorization', 'exchanging', 'ready'].includes(stage.status)
            ? [stage.expiresAt]
            : []),
          ...(stage.nextPollAt && stage.nextPollAt > now.value ? [stage.nextPollAt] : []),
        ])
        .filter((time) => time > now.value)
      const expired = stages.value.filter(
        (stage) => stage.status === 'ready' && stage.expiresAt <= now.value,
      )
      if (expired.length) {
        merge(expired.map((stage) => ({ ...stage, status: 'expired' })))
        return
      }
      const awaiting = stages.value.some(shouldPoll)
      if (deadlines.length || awaiting)
        expiryTimer = setTimeout(
          () => {
            now.value = Date.now()
            stages.value = [...stages.value]
          },
          Math.min(Math.min(...deadlines) - now.value + 1, awaiting ? 1000 : 2147483647),
        )
    },
    { immediate: true },
  )
  function cancelReads(id?: string): void {
    if (id) {
      const state = polling.get(id)
      if (state) stopRequest(state)
    } else {
      for (const state of polling.values()) stopRequest(state)
    }
  }
  onScopeDispose(() => {
    disposed = true
    clearTimeout(expiryTimer)
    cancelReads()
    polling.clear()
  })
  return { now, merge, confirm, problem, cancelReads }
}
