import { queryOptions } from '@tanstack/vue-query'

import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import {
  projectArray,
  projectBoolean,
  projectEnum,
  projectEpochMilliseconds,
  projectRecord,
  projectSafeInteger,
} from './projector'

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

function projectView(value: unknown): ConcurrencyView {
  const record = projectRecord(value)
  return {
    scope: projectEnum(record.scope, concurrencyScopes),
    id: projectSafeInteger(record.id, { minimum: 0 }),
    current_concurrency: projectSafeInteger(record.current_concurrency, { minimum: 0 }),
    max_concurrency:
      record.max_concurrency === null
        ? null
        : projectSafeInteger(record.max_concurrency, { minimum: 0, maximum: 1_000_000 }),
    effective_max_concurrency: projectSafeInteger(record.effective_max_concurrency, {
      minimum: 0,
      maximum: 1_000_000,
    }),
    source: projectEnum(record.source, ['default', 'override'] as const),
    shared: projectBoolean(record.shared),
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
    const record = projectRecord(
      await client.request(`/api/concurrency?targets=${encodeURIComponent(targets)}`, {
        signal: controller.signal,
      }),
    )
    projectEpochMilliseconds(record.observed_at_ms)
    const items = projectArray(record.items, projectView)
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

export const concurrencyQueryKey = ['control', 'concurrency'] as const
export function concurrencyQueryOptions(client: ApiClient, scope: ConcurrencyScope, id: number) {
  return queryOptions({
    queryKey: [...concurrencyQueryKey, scope, id],
    queryFn: ({ signal }) => readBatched(client, scope, id, signal),
    refetchInterval: 3000,
    refetchIntervalInBackground: false,
    staleTime: 1000,
    retry: false,
  })
}

export async function updateConcurrency(
  client: ApiClient,
  scope: ConcurrencyScope,
  id: number,
  maximum: number | null,
): Promise<void> {
  await client.request(`/api/concurrency/${scope}/${id}`, {
    method: 'PUT',
    json: { max_concurrency: maximum },
  })
}
