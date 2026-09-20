import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { integer, list, record, text } from './response'

export interface QuotaHistoryPoint {
  observedAt: number
  usedBasisPoints: number
}
export interface QuotaHistoryWindow {
  key: string
  id: string
  sourceId: string
  label: string
  labelKey: string
  scope: string
  windowSeconds: number
  points: QuotaHistoryPoint[]
}
export interface QuotaHistoryReport {
  from: number
  to: number
  windows: QuotaHistoryWindow[]
}

export async function getCredentialQuotaHistory(
  client: ApiClient,
  group: number,
  credential: number,
  range: { from_ms: string; to_ms: string },
  signal: AbortSignal,
): Promise<QuotaHistoryReport> {
  const row = record(
    await client.request(
      `/api/groups/${group}/credentials/${credential}/quota-history?${new URLSearchParams(range)}`,
      { signal },
    ),
  )
  const from = integer(row.from_ms),
    to = integer(row.to_ms)
  if (from !== Number(range.from_ms) || to !== Number(range.to_ms)) throw new InvalidResponseError()
  return {
    from,
    to,
    windows: list(row.windows)
      .map(record)
      .map((window) => {
        let previous = -1
        return {
          key: text(window.key),
          id: text(window.id),
          sourceId: text(window.source_id),
          label: text(window.label),
          labelKey: text(window.label_key),
          scope: text(window.scope),
          windowSeconds: integer(window.window_seconds, 1),
          points: list(window.points).map((value) => {
            const point = record(value),
              at = integer(point.observed_at_ms),
              used = integer(point.used_basis_points)
            if (at < from || at >= to || at <= previous || used > 10_000)
              throw new InvalidResponseError()
            previous = at
            return {
              observedAt: at,
              usedBasisPoints: used,
            }
          }),
        }
      }),
  }
}
