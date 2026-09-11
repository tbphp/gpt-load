import type { LocationQuery } from 'vue-router'
import { groupSorts, groupViews, type GroupFilters } from '@modern/api/groups'

export function parseGroupFilters(query: LocationQuery): GroupFilters {
  return {
    q: typeof query.q === 'string' ? Array.from(query.q.trim()).slice(0, 200).join('') : '',
    view: groupViews.find((value) => value === query.view) ?? 'all',
    channel: typeof query.channel === 'string' ? query.channel : '',
    sort: groupSorts.find((value) => value === query.sort) ?? 'priority',
  }
}
export function groupFilterQuery(filters: GroupFilters): Record<string, string> {
  const query: Record<string, string> = {}
  if (filters.q) query.q = filters.q
  if (filters.view !== 'all') query.view = filters.view
  if (filters.channel) query.channel = filters.channel
  if (filters.sort !== 'priority') query.sort = filters.sort
  return query
}
