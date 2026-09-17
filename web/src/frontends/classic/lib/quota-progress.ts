export type QuotaProgressTone = 'success' | 'warning' | 'danger'

export function quotaProgressTone(remainingPercent: number, exhausted = false): QuotaProgressTone {
  if (exhausted || remainingPercent < 30) return 'danger'
  if (remainingPercent < 70) return 'warning'
  return 'success'
}
