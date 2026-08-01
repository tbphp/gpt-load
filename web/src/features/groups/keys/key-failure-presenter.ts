import type { FailureCategory } from '@/api/control/types'

type Translate = (key: string) => string

const failureKeys: Record<FailureCategory, string> = {
  ok: 'group.keys.failure.ok',
  rate_limited: 'group.keys.failure.rateLimited',
  model_unavailable: 'group.keys.failure.modelUnavailable',
  invalid_key: 'group.keys.failure.invalidKey',
  upstream_host_error: 'group.keys.failure.upstreamHostError',
  client_error: 'group.keys.failure.clientError',
  downstream_cancel: 'group.keys.failure.downstreamCancel',
  ambiguous: 'group.keys.failure.ambiguous',
}

export function presentKeyFailureCategory(t: Translate, category: FailureCategory): string {
  return t(failureKeys[category] ?? 'group.keys.failure.unknown')
}
