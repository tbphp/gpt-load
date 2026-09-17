import type { LocationQuery } from 'vue-router'
import { groupSorts, groupViews, type GroupFilters } from '@modern/api/groups'
import { protocolOrder } from '@modern/i18n/protocols'

export function parseGroupFilters(query: LocationQuery): GroupFilters {
  const page = typeof query.page === 'string' ? Number(query.page) : 1
  const size = typeof query.page_size === 'string' ? Number(query.page_size) : 20
  return {
    page: Number.isSafeInteger(page) && page > 0 ? page : 1,
    pageSize: [20, 50, 100].includes(size) ? size : 20,
    q: typeof query.q === 'string' ? Array.from(query.q.trim()).slice(0, 200).join('') : '',
    view: groupViews.find((value) => value === query.view) ?? 'all',
    channel: typeof query.channel === 'string' ? query.channel : '',
    connection:
      query.connection === 'api_key' || query.connection === 'subscription' ? query.connection : '',
    model: typeof query.model === 'string' ? query.model.trim() : '',
    credential:
      typeof query.credential_key === 'string' && /^[a-f0-9]{64}$/u.test(query.credential_key)
        ? query.credential_key
        : '',
    protocol: protocolOrder.find((value) => value === query.protocol) ?? '',
    sort: groupSorts.find((value) => value === query.sort) ?? 'recent',
  }
}
export function groupFilterQuery(filters: GroupFilters): Record<string, string> {
  const query: Record<string, string> = {}
  if (filters.page > 1) query.page = String(filters.page)
  if (filters.pageSize !== 20) query.page_size = String(filters.pageSize)
  if (filters.q) query.q = filters.q
  if (filters.view !== 'all') query.view = filters.view
  if (filters.channel) query.channel = filters.channel
  if (filters.connection) query.connection = filters.connection
  if (filters.model) query.model = filters.model
  if (filters.credential) query.credential_key = filters.credential
  if (filters.protocol) query.protocol = filters.protocol
  if (filters.sort !== 'recent') query.sort = filters.sort
  return query
}
