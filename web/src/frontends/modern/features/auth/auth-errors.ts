import { ApiError, NetworkError } from '@shared/http/errors'

export type AuthFailure = 'invalid' | 'locked' | 'network' | 'invalid-response'

export function authFailure(error: unknown): AuthFailure {
  if (error instanceof ApiError && error.code === 'UNAUTHORIZED') return 'invalid'
  if (error instanceof ApiError && error.code === 'AUTH_LOCKED') return 'locked'
  if (error instanceof NetworkError) return 'network'
  return 'invalid-response'
}

export function authRetrySeconds(error: unknown): number {
  const seconds = error instanceof ApiError ? error.retryAfterSeconds : undefined
  return Math.max(1, Math.ceil(seconds !== undefined && Number.isFinite(seconds) ? seconds : 1))
}
