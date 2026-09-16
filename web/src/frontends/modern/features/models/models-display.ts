import type { ModelPrice, RequestModel } from '@modern/api/models'
import { numberFormatter } from '@modern/components/ui/intl-formatters'

export function modelGroupCount(model: RequestModel): number {
  return new Set(model.sources.flatMap((source) => source.groups.map((group) => group.id))).size
}
export function priceStatus(price: ModelPrice): 'pending' | 'manual' | 'automatic' | 'unpriced' {
  if (price.method === 'user_marked_unpriced') return 'unpriced'
  if (price.status === 'pending') return 'pending'
  return price.method === 'auto_sync' ? 'automatic' : 'manual'
}
export function modelUnitPrice(value: string | null, locale: string): string {
  return value === null
    ? '—'
    : numberFormatter(locale, {
        style: 'currency',
        currency: 'USD',
        currencyDisplay: 'narrowSymbol',
        minimumFractionDigits: 0,
        maximumFractionDigits: 9,
      }).format(Number(value))
}
