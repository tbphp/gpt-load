import type { CredentialRow } from '@modern/api/group-detail'
import type { CredentialQuota } from '@modern/api/credential-observation'
import type { SemanticTone } from '@modern/components/ui'

export function credentialStatus(row: CredentialRow): { key: string; tone: SemanticTone } {
  if (!row.enabled) return { key: 'groups.credentials.disabled', tone: 'neutral' }
  if (row.authState !== 'ready')
    return {
      key: 'groupDetail.authState.' + row.authState,
      tone: row.authState === 'refreshing' ? 'neutral' : 'danger',
    }
  const tones: Record<CredentialRow['state'], SemanticTone> = {
    available: 'success',
    cooldown: 'warning',
    blacklisted: 'danger',
    disabled: 'neutral',
  }
  return { key: 'groups.credentials.' + row.state, tone: tones[row.state] }
}
export function credentialTime(value: number | null | undefined, locale: string): string {
  return value
    ? new Intl.DateTimeFormat(locale, {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }).format(value)
    : '—'
}
export function quotaRemaining(window: CredentialQuota): number | undefined {
  const remaining =
    window.utilization !== undefined
      ? (1 - window.utilization) * 100
      : window.remaining !== undefined && window.limit
        ? (window.remaining / window.limit) * 100
        : undefined
  return remaining === undefined ? undefined : Math.max(0, Math.min(100, remaining))
}
export function quotaPeriod(seconds?: number): string {
  if (!seconds) return ''
  if (seconds % 86400 === 0) return `${seconds / 86400}d`
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}min`
  return `${seconds}s`
}
export function sortedQuotaWindows(windows: readonly CredentialQuota[]): CredentialQuota[] {
  const groups = new Map<string, number>()
  function key(window: CredentialQuota): string {
    return window.models.length ? [...window.models].sort().join('\u0000') : window.scope
  }
  windows.forEach((window, index) => {
    if (!groups.has(key(window))) groups.set(key(window), index)
  })
  return [...windows].sort(
    (a, b) =>
      Number(a.scope !== 'account') - Number(b.scope !== 'account') ||
      (groups.get(key(a)) ?? 0) - (groups.get(key(b)) ?? 0) ||
      (a.windowSeconds ?? Infinity) - (b.windowSeconds ?? Infinity),
  )
}
