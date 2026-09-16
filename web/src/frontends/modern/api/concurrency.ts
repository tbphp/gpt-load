import { queryOptions } from '@tanstack/vue-query'

import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record } from './response'
export const concurrencyScopes = [
  'global',
  'upstream',
  'default_group',
  'default_access_key',
  'default_credential',
  'group',
  'access_key',
  'credential',
] as const
export type ConcurrencyScope = (typeof concurrencyScopes)[number]
export interface ConcurrencyView {
  scope: ConcurrencyScope
  id: number
  current_concurrency: number
  max_concurrency: number | null
  effective_max_concurrency: number
  source: 'default' | 'override'
  shared: boolean
}

function limit(value: unknown): number {
  const result = integer(value, 0)
  if (result > 1_000_000) throw new InvalidResponseError()
  return result
}
function projectView(value: unknown): ConcurrencyView {
  const row = record(value)
  return {
    scope: oneOf(row.scope, concurrencyScopes),
    id: integer(row.id, 0),
    current_concurrency: integer(row.current_concurrency, 0),
    max_concurrency: row.max_concurrency === null ? null : limit(row.max_concurrency),
    effective_max_concurrency: limit(row.effective_max_concurrency),
    source: oneOf(row.source, ['default', 'override']),
    shared: boolean(row.shared),
  }
}

interface PendingRead {
  target: string
  signal: AbortSignal
  resolve: (value: ConcurrencyView) => void
  reject: (error: unknown) => void
}
const batches = new WeakMap<ApiClient, PendingRead[]>()

// Rows keep independent query-cache entries, but reads from the same render or
// polling tick share one lightweight HTTP request (at most 100 targets).
function readBatched(
  client: ApiClient,
  scope: ConcurrencyScope,
  id: number,
  signal: AbortSignal,
): Promise<ConcurrencyView> {
  return new Promise((resolve, reject) => {
    let batch = batches.get(client)
    if (!batch) {
      batch = []
      batches.set(client, batch)
      window.setTimeout(() => {
        const pending = batches.get(client) ?? []
        batches.delete(client)
        const active = pending.filter((read) => {
          if (!read.signal.aborted) return true
          read.reject(new DOMException('Aborted', 'AbortError'))
          return false
        })
        for (let offset = 0; offset < active.length; offset += 100) {
          void flushBatch(client, active.slice(offset, offset + 100))
        }
      }, 10)
    }
    batch.push({ target: `${scope}:${id}`, signal, resolve, reject })
  })
}

async function flushBatch(client: ApiClient, batch: PendingRead[]): Promise<void> {
  const controller = new AbortController()
  const cancel = () => {
    if (batch.every((read) => read.signal.aborted)) controller.abort()
  }
  for (const read of batch) read.signal.addEventListener('abort', cancel, { once: true })
  try {
    const targets = [...new Set(batch.map((read) => read.target))].join(',')
    const response = record(
      await client.request(`/api/concurrency?targets=${encodeURIComponent(targets)}`, {
        signal: controller.signal,
      }),
    )
    integer(response.observed_at_ms, 0)
    const items = list(response.items).map(projectView)
    const views = new Map(items.map((item) => [`${item.scope}:${item.id}`, item]))
    for (const read of batch) {
      if (read.signal.aborted) read.reject(new DOMException('Aborted', 'AbortError'))
      else {
        const view = views.get(read.target)
        if (view) read.resolve(view)
        else read.reject(new InvalidResponseError())
      }
    }
  } catch (error) {
    for (const read of batch) read.reject(error)
  } finally {
    for (const read of batch) read.signal.removeEventListener('abort', cancel)
  }
}

export const concurrencyQueryKey = ['modern', 'concurrency'] as const
export function concurrencyQueryOptions(client: ApiClient, scope: ConcurrencyScope, id: number) {
  return queryOptions({
    queryKey: [...concurrencyQueryKey, scope, id],
    queryFn: ({ signal }) => readBatched(client, scope, id, signal),
    staleTime: 1000,
    retry: false,
  })
}

export const settingsConcurrencyScopes = [
  'global',
  'default_group',
  'default_access_key',
  'default_credential',
] as const
export type SettingsConcurrencyPatch = Partial<
  Record<(typeof settingsConcurrencyScopes)[number], number | null>
>
