import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { integer, list, record } from './response'

export interface GroupUsageTrend {
  points: { from: number; to: number; requests: number }[]
  incomplete: boolean
}

export async function getGroupUsageTrend(
  client: ApiClient,
  group: number,
  signal: AbortSignal,
): Promise<GroupUsageTrend> {
  const to = Date.now()
  const from = to - 24 * 60 * 60 * 1000
  const params = new URLSearchParams({
    group_id: String(group),
    from_ms: String(from),
    to_ms: String(to),
  })
  const data = record(await client.request(`/api/usage?${params}`, { signal }))
  const width = integer(data.bucket_width_ms, 5 * 60 * 1000)
  if (integer(data.from_ms) !== from || integer(data.to_ms) !== to) throw new InvalidResponseError()
  const buckets = new Map<number, number>()
  let previousEnd = from
  for (const value of list(data.series)) {
    const item = record(value)
    const start = integer(item.bucket_start_ms)
    const end = integer(item.bucket_end_ms)
    if (
      start < previousEnd ||
      start < from ||
      end > to ||
      end <= start ||
      start !== Math.max(from, Math.floor(start / width) * width)
    )
      throw new InvalidResponseError()
    buckets.set(start, integer(item.request_count))
    previousEnd = end
  }
  const points: GroupUsageTrend['points'] = []
  // 聚合接口只返回有记录的桶；时间轴中未返回的桶代表没有请求。
  for (let start = Math.floor(from / width) * width; start < to; start += width)
    points.push({
      from: Math.max(start, from),
      to: Math.min(start + width, to),
      requests: buckets.get(Math.max(start, from)) ?? 0,
    })
  const health = record(data.collection_health)
  const summary = record(data.summary)
  return {
    points,
    incomplete:
      integer(health.dropped_total) > 0 ||
      integer(health.write_failure_total) > 0 ||
      integer(summary.partial_count) > 0,
  }
}
