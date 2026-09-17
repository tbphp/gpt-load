import type { QueryClient } from '@tanstack/vue-query'
import { inject, reactive, readonly, type InjectionKey } from 'vue'

import { getAuthSession } from '@modern/api/auth'
import { clearFrontendPreference } from '@shared/frontend/preference'
import type { ApiClient } from '@shared/http/client'
import { RequestCancelledError } from '@shared/http/errors'
import type { AuthPrincipalType } from '@shared/http/types'
import { authFailure, authRetrySeconds } from './auth-errors'

type AuthPhase =
  | 'anonymous'
  | 'unvalidated'
  | 'validating'
  | 'validated'
  | 'locked'
  | 'network-error'
  | 'invalid-response'

const authStorageKey = 'gpt-load.auth-key'

function readCredential(storage?: Storage): string {
  try {
    return storage?.getItem(authStorageKey) ?? ''
  } catch {
    // 一种存储不可用时，仍可读取另一种存储。
    return ''
  }
}

function storeCredential(storage: Storage | undefined, value: string): void {
  try {
    if (value) storage?.setItem(authStorageKey, value)
    else storage?.removeItem(authStorageKey)
  } catch {
    // 内存会话始终生效；存储不可用不能阻止登录或退出。
  }
}

export function createAuthSession(deps: {
  client: ApiClient
  queryClient: QueryClient
  localStorage?: Storage
  sessionStorage?: Storage
}) {
  let credential = readCredential(deps.sessionStorage) || readCredential(deps.localStorage)
  let revision = 0
  let loginController: AbortController | undefined
  let validation: { controller: AbortController; promise: Promise<void> } | undefined
  const state = reactive({
    phase: (credential ? 'unvalidated' : 'anonymous') as AuthPhase,
    principalType: null as AuthPrincipalType | null,
    retryAfterSeconds: 0,
  })

  function cancelValidation(): void {
    validation?.controller.abort()
    validation = undefined
  }

  function clear(): void {
    revision += 1
    cancelValidation()
    loginController?.abort()
    loginController = undefined
    credential = ''
    storeCredential(deps.localStorage, '')
    storeCredential(deps.sessionStorage, '')
    clearFrontendPreference()
    state.phase = 'anonymous'
    state.principalType = null
    state.retryAfterSeconds = 0
    // 新版 QueryClient 独立于经典版：取消查询，并清空当前身份的查询和 mutation 缓存。
    deps.queryClient.clear()
  }

  function ensureValidated(): Promise<void> {
    if (!credential || state.phase === 'validated') return Promise.resolve()
    if (validation) return validation.promise
    const currentRevision = revision
    const candidate = credential
    const controller = new AbortController()
    state.phase = 'validating'
    state.principalType = null
    state.retryAfterSeconds = 0

    const promise = getAuthSession(deps.client, candidate, controller.signal, true)
      .then((session) => {
        if (currentRevision !== revision || controller.signal.aborted) return
        state.principalType = session.principal_type
        state.phase = 'validated'
      })
      .catch((error: unknown) => {
        if (currentRevision !== revision || controller.signal.aborted) return
        if (error instanceof RequestCancelledError) {
          state.phase = 'unvalidated'
          return
        }
        const failure = authFailure(error)
        if (failure === 'invalid') {
          clear()
        } else if (failure === 'locked') {
          state.phase = 'locked'
          state.retryAfterSeconds = authRetrySeconds(error)
        } else {
          state.phase = failure === 'network' ? 'network-error' : 'invalid-response'
        }
      })
      .finally(() => {
        if (validation?.controller === controller) validation = undefined
      })
    validation = { controller, promise }
    return promise
  }

  async function login(candidate: string, remember: boolean, signal: AbortSignal): Promise<void> {
    cancelValidation()
    if (state.phase === 'validating') state.phase = 'unvalidated'
    loginController?.abort()
    const controller = new AbortController()
    loginController = controller
    const currentRevision = revision
    const cancel = () => controller.abort()
    signal.addEventListener('abort', cancel, { once: true })
    if (signal.aborted) controller.abort()
    try {
      const session = await getAuthSession(deps.client, candidate, controller.signal)
      if (currentRevision !== revision || controller.signal.aborted) {
        throw new RequestCancelledError()
      }
      revision += 1
      cancelValidation()
      deps.queryClient.clear()
      credential = candidate
      // 仅在认证成功后切换存储；不记住时绝不回退到持久存储。
      storeCredential(remember ? deps.sessionStorage : deps.localStorage, '')
      storeCredential(remember ? deps.localStorage : deps.sessionStorage, candidate)
      state.principalType = session.principal_type
      state.retryAfterSeconds = 0
      state.phase = 'validated'
    } finally {
      signal.removeEventListener('abort', cancel)
      if (loginController === controller) loginController = undefined
    }
  }

  return {
    state: readonly(state),
    getAuthKey: () => credential,
    getRevision: () => revision,
    hasCredential: () => credential.length > 0,
    getPrincipalType: () => state.principalType,
    ensureValidated,
    login,
    clear,
  }
}

export type AuthSession = ReturnType<typeof createAuthSession>
export const authSessionKey: InjectionKey<AuthSession> = Symbol('modern-auth-session')

export function useAuthSession(): AuthSession {
  const session = inject(authSessionKey)
  if (!session) throw new Error('MODERN_AUTH_SESSION_NOT_PROVIDED')
  return session
}
