import { dateFormatter } from '@modern/components/ui/intl-formatters'
import type { AccessKey } from '@modern/api/access-keys'
import { formatNanoUSD } from '@modern/components/ui/format'
import type { SemanticTone } from '@modern/components/ui'
export function accessState(row: AccessKey & { expired?: boolean }): {
  key: string
  tone: SemanticTone
} {
  if (row.status === 'disabled') return { key: 'disabled', tone: 'neutral' }
  if (row.expired ?? (row.expires_at_ms !== null && row.expires_at_ms <= Date.now()))
    return { key: 'expired', tone: 'warning' }
  if (row.cost_limit_status?.allowed === false) return { key: 'exhausted', tone: 'danger' }
  return { key: 'active', tone: 'success' }
}
export function accessTime(value: number | null | undefined, locale: string, time = true): string {
  if (!value) return '—'
  return dateFormatter(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    ...(time ? { hour: '2-digit', minute: '2-digit' } : {}),
  }).format(value)
}
export function accessNanoUSD(value: string): bigint {
  const [whole = '0', fraction = ''] = value.split('.')
  return BigInt(whole) * 1000000000n + BigInt(fraction.padEnd(9, '0'))
}
export function accessUSD(value: string, locale: string): string {
  return formatNanoUSD(String(accessNanoUSD(value)), locale, 'narrowSymbol')
}
