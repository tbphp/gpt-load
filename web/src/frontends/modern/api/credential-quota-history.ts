import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, record, text } from './response'

const optionalInteger = (value: unknown) => (value == null ? undefined : integer(value))

export interface QuotaHistoryPoint {
  observedAt: number
  usedBasisPoints: number
  resetAt?: number
}
export interface QuotaHistoryWindow {
  key: string
  id: string
  sourceID: string
  label: string
  labelKey: string
  scope: string
  seconds?: number
  points: QuotaHistoryPoint[]
}
export interface QuotaHistoryReport {
  from: number
  to: number
  observedAt: number
  bucketWidth: number
  hasHistory: boolean
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
    observedAt: integer(row.observed_at_ms),
    bucketWidth: integer(row.bucket_width_ms, 60_000),
    hasHistory: boolean(row.has_history),
    windows: list(row.windows).map((value) => {
      const window = record(value)
      let previous = -1
      return {
        key: text(window.key),
        id: text(window.id),
        sourceID: text(window.source_id),
        label: text(window.label),
        labelKey: text(window.label_key),
        scope: text(window.scope),
        seconds: optionalInteger(window.window_seconds),
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
            resetAt: optionalInteger(point.reset_at_ms),
          }
        }),
      }
    }),
  }
}
