import type { ApiClient } from '@shared/http/client'
import { integer, list, record } from './response'

export type RPMScope =
  { kind: 'access_key'; id: number } | { kind: 'credential'; id: number; group: number }

export interface RPMReport {
  from: number
  to: number
  observedAt: number
  bucketWidth: number
  current?: { requests: number; rejected: number }
  peak?: number
  requests: number
  rejected: number
  points: { at: number; peak: number; requests: number; rejected: number }[]
}

export function rpmQueryKey(scope: RPMScope) {
  return ['modern', 'rpm', scope.kind, scope.kind === 'credential' ? scope.group : 0, scope.id]
}

export async function getRPM(
  client: ApiClient,
  scope: RPMScope,
  signal: AbortSignal,
): Promise<RPMReport> {
  const path: `/api/${string}` =
    scope.kind === 'access_key'
      ? `/api/access-keys/${scope.id}/rpm`
      : `/api/modern/groups/${scope.group}/credentials/${scope.id}/rpm`
  const data = record(await client.request(path, { signal }))
  const current = data.current == null ? undefined : record(data.current)
  return {
    from: integer(data.from_ms),
    to: integer(data.to_ms),
    observedAt: integer(data.observed_at_ms),
    bucketWidth: integer(data.bucket_width_ms, 1),
    current: current
      ? { requests: integer(current.requests), rejected: integer(current.rejected) }
      : undefined,
    peak: data.peak_rpm == null ? undefined : integer(data.peak_rpm),
    requests: integer(data.requests),
    rejected: integer(data.rejected),
    points: list(data.points).map((value) => {
      const point = record(value)
      return {
        at: integer(point.bucket_start_ms),
        peak: integer(point.peak_rpm),
        requests: integer(point.requests),
        rejected: integer(point.rejected),
      }
    }),
  }
}
