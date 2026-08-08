import type { AppLocale } from '@/i18n'

export interface SuccessEnvelope<T> {
  code: 0
  message: string
  data?: T
}

export interface ErrorEnvelope<T = unknown> {
  code: string
  message: string
  data?: T
}

export type AuthPrincipalType = 'admin' | 'access_key'

export interface AuthSessionPayload {
  authenticated: boolean
  principal_type: AuthPrincipalType
}

export interface AuthLockedPayload {
  retry_after_seconds: number
}

export type { AppLocale }
