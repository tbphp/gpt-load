import type { QueryClient } from '@tanstack/vue-query'
import { inject, reactive, readonly, type DeepReadonly, type InjectionKey } from 'vue'

import { ApiError, InvalidResponseError, NetworkError, RequestCancelledError } from '@/api/errors'
import type { AuthPrincipalType, AuthSessionPayload } from '@/api/types'
import { controlQueryKeys } from '@/app/query-keys'

export const authSessionQueryKey = ['auth', 'session'] as const
const authStorageKey = 'gpt-load.auth-key'

export type AuthPhase =
  | 'anonymous'
  | 'unvalidated'
  | 'validating'
  | 'validated'
  | 'locked'
  | 'network-error'
  | 'invalid-response'

export interface AuthState {
  phase: AuthPhase
  retryAfterSeconds: number
  principalType: AuthPrincipalType | null
}

export interface AuthSession {
  readonly state: DeepReadonly<AuthState>
  getAuthKey(): string
  getPrincipalType(): AuthPrincipalType | null
  hasCredential(): boolean
  ensureValidated(): Promise<void>
  login(candidate: string): Promise<void>
  retryValidation(): Promise<void>
  clear(): void
}

export interface AuthSessionDependencies {
  storage?: Storage
  queryClient: QueryClient
  onClear?(): void
  validate(
    key: string,
    globalUnauthorized: boolean,
    signal?: AbortSignal,
  ): Promise<AuthSessionPayload>
}

function readStoredCredential(storage?: Storage): string {
  try {
    return storage?.getItem(authStorageKey) || ''
  } catch {
    return ''
  }
}

function writeStoredCredential(storage: Storage | undefined, credential: string): void {
  try {
    storage?.setItem(authStorageKey, credential)
  } catch {
    // The in-memory credential remains usable when browser storage is unavailable.
  }
}

function removeStoredCredential(storage?: Storage): void {
  try {
    storage?.removeItem(authStorageKey)
  } catch {
    // Clearing the in-memory credential remains authoritative.
  }
}

export function clearAuthenticatedClientState(queryClient: QueryClient): Promise<void> {
  const cancellation = queryClient.cancelQueries({ queryKey: controlQueryKeys.all })
  queryClient.removeQueries({ queryKey: controlQueryKeys.all })
  queryClient.removeQueries({ queryKey: authSessionQueryKey })
  queryClient.getMutationCache().clear()
  return cancellation
}

export function createAuthSession(deps: AuthSessionDependencies): AuthSession {
  let credential = readStoredCredential(deps.storage)
  let credentialRevision = 0
  const state = reactive<AuthState>({
    phase: credential ? 'unvalidated' : 'anonymous',
    retryAfterSeconds: 0,
    principalType: null,
  })
  let validationPromise: Promise<void> | undefined

  function clear(): void {
    credential = ''
    credentialRevision += 1
    removeStoredCredential(deps.storage)
    state.phase = 'anonymous'
    state.retryAfterSeconds = 0
    state.principalType = null
    deps.onClear?.()
    void clearAuthenticatedClientState(deps.queryClient)
  }

  function applyValidationError(error: unknown): void {
    if (error instanceof ApiError && error.code === 'UNAUTHORIZED') {
      clear()
      return
    }
    if (error instanceof ApiError && error.code === 'AUTH_LOCKED') {
      state.phase = 'locked'
      state.retryAfterSeconds = Math.max(1, Math.ceil(error.retryAfterSeconds ?? 1))
      return
    }
    if (error instanceof NetworkError) {
      state.phase = 'network-error'
      state.retryAfterSeconds = 0
      return
    }
    if (error instanceof RequestCancelledError) {
      state.phase = 'unvalidated'
      state.retryAfterSeconds = 0
      return
    }
    if (error instanceof InvalidResponseError) {
      state.phase = 'invalid-response'
      state.retryAfterSeconds = 0
    }
  }

  function ensureValidated(): Promise<void> {
    if (!credential) {
      return Promise.reject(new ApiError(401, 'UNAUTHORIZED', 'UNAUTHORIZED'))
    }
    if (state.phase === 'validated') {
      return Promise.resolve()
    }
    if (validationPromise) return validationPromise

    const key = credential
    const revision = credentialRevision
    state.phase = 'validating'
    state.retryAfterSeconds = 0

    const pending = deps.queryClient
      .fetchQuery({
        queryKey: authSessionQueryKey,
        queryFn: ({ signal }) => deps.validate(key, true, signal),
        staleTime: Infinity,
        retry: false,
      })
      .then((payload) => {
        if (!isValidAuthSessionPayload(payload)) {
          throw new InvalidResponseError()
        }
        if (revision === credentialRevision && key === credential) {
          state.phase = 'validated'
          state.retryAfterSeconds = 0
          state.principalType = payload.principal_type
        }
      })
      .catch((error: unknown) => {
        if (revision !== credentialRevision || key !== credential) {
          return
        }
        applyValidationError(error)
        throw error
      })

    const shared = pending.finally(() => {
      if (validationPromise === shared) {
        validationPromise = undefined
      }
    })
    validationPromise = shared
    return shared
  }

  function retryValidation(): Promise<void> {
    deps.queryClient.removeQueries({ queryKey: authSessionQueryKey })
    if (credential) {
      state.phase = 'unvalidated'
    }
    return ensureValidated()
  }

  async function login(candidate: string): Promise<void> {
    const payload = await deps.validate(candidate, false)
    if (!isValidAuthSessionPayload(payload)) {
      throw new InvalidResponseError()
    }

    await clearAuthenticatedClientState(deps.queryClient)
    credentialRevision += 1
    credential = candidate
    writeStoredCredential(deps.storage, candidate)
    state.phase = 'validated'
    state.retryAfterSeconds = 0
    state.principalType = payload.principal_type
    deps.queryClient.setQueryData(authSessionQueryKey, payload)
  }

  return {
    state: readonly(state),
    getAuthKey() {
      return credential
    },
    getPrincipalType() {
      return state.principalType
    },
    hasCredential() {
      return credential.length > 0
    },
    ensureValidated,
    login,
    retryValidation,
    clear,
  }
}

function isValidAuthSessionPayload(payload: AuthSessionPayload | undefined): boolean {
  return (
    payload?.authenticated === true &&
    (payload.principal_type === 'admin' || payload.principal_type === 'access_key')
  )
}

export const authSessionKey: InjectionKey<AuthSession> = Symbol('auth-session')

export function useAuthSession(): AuthSession {
  const session = inject(authSessionKey)
  if (!session) {
    throw new Error('AUTH_SESSION_NOT_PROVIDED')
  }
  return session
}
