import type { FailureCategory } from '@/api/control/types'

type Translate = (key: string) => string

const failureMessages: Record<FailureCategory, string> = {
  ok: 'group.credentials.failure.ok',
  rate_limited: 'group.credentials.failure.rateLimited',
  model_unavailable: 'group.credentials.failure.modelUnavailable',
  invalid_key: 'group.credentials.failure.invalidCredential',
  upstream_host_error: 'group.credentials.failure.upstreamHostError',
  client_error: 'group.credentials.failure.clientError',
  downstream_cancel: 'group.credentials.failure.downstreamCancel',
  authentication_required: 'group.credentials.failure.authenticationRequired',
  ambiguous: 'group.credentials.failure.ambiguous',
}

export function presentCredentialFailureCategory(t: Translate, category: FailureCategory): string {
  return t(failureMessages[category] ?? 'group.credentials.failure.unknown')
}
