import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import type { AuthSessionPayload } from '@shared/http/types'
import { integer, record } from './response'

function readRequestBodyRecording(
  value: unknown,
): AuthSessionPayload['request_body_recording'] | undefined {
  if (value == null) return undefined
  return { retention_days: integer(record(value).retention_days, 1) }
}

export async function getAuthSession(
  client: ApiClient,
  credential: string,
  signal: AbortSignal,
  handleUnauthorized = false,
): Promise<AuthSessionPayload> {
  const data = await client.request<unknown>('/api/auth/session', {
    authKey: credential,
    cache: 'no-store',
    signal,
    // 候选密钥验证失败不清除已有会话；恢复已保存身份时继续执行全局 401 清理。
    handleUnauthorized,
  })
  if (
    typeof data !== 'object' ||
    data === null ||
    !('authenticated' in data) ||
    data.authenticated !== true ||
    !('principal_type' in data) ||
    (data.principal_type !== 'admin' && data.principal_type !== 'access_key')
  ) {
    throw new InvalidResponseError()
  }
  const recording = readRequestBodyRecording(
    'request_body_recording' in data ? data.request_body_recording : undefined,
  )
  return {
    authenticated: true,
    principal_type: data.principal_type,
    ...(recording ? { request_body_recording: recording } : {}),
  }
}
