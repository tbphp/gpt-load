import { dateFormatter } from '@modern/components/ui/intl-formatters'
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
    ? dateFormatter(locale, {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }).format(value)
    : '—'
}
export function quotaCycleTime(value: number | null | undefined): string {
  if (!value || !Number.isSafeInteger(value)) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
export function quotaRemaining(window: CredentialQuota): number | undefined {
  const remaining =
    window.utilization !== undefined
      ? (1 - window.utilization) * 100
      : window.remaining !== undefined && window.limit
        ? (window.remaining / window.limit) * 100
        : window.used !== undefined && window.limit
          ? (1 - window.used / window.limit) * 100
          : undefined
  return remaining === undefined ? undefined : Math.max(0, Math.min(100, remaining))
}
export function quotaTone(window: CredentialQuota): 'neutral' | 'success' | 'warning' | 'danger' {
  const percent = quotaRemaining(window)
  if (window.state === 'exhausted') return 'danger'
  return percent === undefined
    ? 'neutral'
    : percent < 30
      ? 'danger'
      : percent < 70
        ? 'warning'
        : 'success'
}
export function quotaPeriod(seconds?: number): string {
  if (!seconds) return ''
  if (seconds % 86400 === 0) return `${seconds / 86400}d`
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}min`
  return `${seconds}s`
}
export function quotaWindowRange(
  window: CredentialQuota,
): { start: number; end: number } | undefined {
  if (!window.resetsAt || !window.windowSeconds) return undefined
  const duration = window.windowSeconds * 1000
  if (!Number.isSafeInteger(duration) || duration > window.resetsAt) return undefined
  return { start: window.resetsAt - duration, end: window.resetsAt }
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

export function quotaWindowTitle(window: CredentialQuota, subject = window.label): string {
  const period = quotaPeriod(window.windowSeconds)
  if (window.scope === 'account' && window.labelKey === 'session') return period
  if (window.scope === 'account' && period)
    return subject && !subject.toLowerCase().includes(period.toLowerCase())
      ? `${subject} · ${period}`
      : subject || period
  return subject
}
