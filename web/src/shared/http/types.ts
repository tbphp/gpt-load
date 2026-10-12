import type { AppLocale } from '@shared/preferences/locale'

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
  // 仅当登录主体是访问密钥且该密钥开启了请求内容记录时返回。
  request_body_recording?: { retention_days: number }
}

export interface AuthLockedPayload {
  retry_after_seconds: number
}

export type { AppLocale }
