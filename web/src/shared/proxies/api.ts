import type { ApiClient } from '../http/client'

export interface ProxyItem {
  id: number
  name: string
  display_url: string
  scheme: 'http' | 'socks5'
  has_auth: boolean
  enabled: boolean
  group_count: number
  credential_count: number
  global: boolean
  last_test_url: string
  last_test_at_ms: number | null
  last_test_duration_ms: number | null
  last_test_status_code: number | null
  last_test_error: string
}

export interface ProxyFilters {
  q: string
  state: string
  scheme: string
  used: string
  test: string
  sort: string
  page: number
  page_size: number
}

export interface ProxyList {
  items: ProxyItem[]
  total: number
  page: number
  page_size: number
  test_url: string
}

export interface ProxyReference {
  group_id: number
  group_name: string
  credential_id?: number
  label?: string
  connection_type?: string
}

export interface ProxyImpact {
  groups: ProxyReference[]
  credentials: ProxyReference[]
  global: boolean
}

export const proxyListKey = ['proxies'] as const

export function listProxies(
  client: ApiClient,
  filters: Partial<ProxyFilters> = {},
  signal?: AbortSignal,
  all = false,
) {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(filters)) {
    if (value !== '') query.set(key, String(value))
  }
  if (all) query.set('all', 'true')
  return client.request<ProxyList>(`/api/proxies?${query}`, { signal })
}

export function saveProxy(client: ApiClient, value: { name: string; url: string }, id?: number) {
  return client.request<ProxyItem>(id ? `/api/proxies/${id}` : '/api/proxies', {
    method: id ? 'PUT' : 'POST',
    json: value,
  })
}

export function importProxies(client: ApiClient, entries: string) {
  return client.request<{ imported: number; duplicates: number; invalid_lines: number[] }>(
    '/api/proxies/import',
    {
      method: 'POST',
      json: { entries },
    },
  )
}

export function proxyImpact(client: ApiClient, ids: number[]) {
  return client.request<ProxyImpact>('/api/proxies/impact', { method: 'POST', json: { ids } })
}

export function batchProxies(
  client: ApiClient,
  ids: number[],
  action: 'enable' | 'disable' | 'delete',
) {
  return client.request('/api/proxies/batch', { method: 'POST', json: { ids, action } })
}

export function saveProxyTestURL(client: ApiClient, url: string, signal?: AbortSignal) {
  return client.request('/api/proxies/test-url', { method: 'PUT', json: { url }, signal })
}

export function testProxy(client: ApiClient, id: number, url: string, signal?: AbortSignal) {
  return client.request<ProxyItem>(`/api/proxies/${id}/test`, {
    method: 'POST',
    json: { url },
    signal,
  })
}

export function proxyTestSucceeded(proxy: ProxyItem) {
  return (
    proxy.last_test_at_ms !== null &&
    !proxy.last_test_error &&
    proxy.last_test_status_code !== null &&
    proxy.last_test_status_code >= 200 &&
    proxy.last_test_status_code < 300
  )
}

export function validProxySelection(value: string) {
  return /^[1-9]\d*$/.test(value) && Number.isSafeInteger(Number(value))
}

export function validTestURL(value: string) {
  try {
    const url = new URL(value)
    return (
      ['http:', 'https:'].includes(url.protocol) &&
      !url.username &&
      !url.password &&
      !url.hash &&
      value.length <= 2048
    )
  } catch {
    return false
  }
}

/** 固定本次目标集合与 URL；取消只停止本次队列，不触发代理状态变更。 */
export async function runProxyTests(
  client: ApiClient,
  ids: number[],
  url: string,
  signal: AbortSignal,
  completed: (id: number, result?: ProxyItem) => void,
) {
  let cursor = 0
  const targets = [...new Set(ids)]
  await Promise.all(
    Array.from({ length: Math.min(4, targets.length) }, async () => {
      while (!signal.aborted && cursor < targets.length) {
        const id = targets[cursor++]!
        try {
          const result = await testProxy(client, id, url, signal)
          if (!signal.aborted) completed(id, result)
        } catch {
          if (!signal.aborted) completed(id)
        }
      }
    }),
  )
}
