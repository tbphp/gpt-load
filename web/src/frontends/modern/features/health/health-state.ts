import type { LocationQuery } from 'vue-router'
import { positivePage } from '@modern/app/url-state'

export const healthKinds = [
  'group',
  'isolated',
  'cooldown',
  'quota',
  'credit',
  'access_key',
] as const
export type HealthKind = (typeof healthKinds)[number]
export const healthStateKeys = [
  'q',
  'group',
  'kind',
  'severity',
  'sort',
  'page',
  'page_size',
  'detail',
] as const
export function parseHealthState(query: LocationQuery) {
  return {
    q: typeof query.q === 'string' ? query.q.slice(0, 200) : '',
    group:
      typeof query.group === 'string' &&
      /^[1-9]\d*$/.test(query.group) &&
      Number.isSafeInteger(Number(query.group))
        ? query.group
        : '',
    kind: healthKinds.find((kind) => kind === query.kind) ?? '',
    severity: query.severity === 'danger' || query.severity === 'warning' ? query.severity : '',
    sort: query.sort === 'name' ? 'name' : 'priority',
    page: positivePage(query.page),
    pageSize: [20, 50, 100].includes(Number(query.page_size)) ? Number(query.page_size) : 20,
    detail:
      typeof query.detail === 'string' &&
      /^(?:group|isolated|cooldown|quota|credit|access_key):[1-9]\d*$/.test(query.detail)
        ? query.detail
        : '',
  }
}
export type HealthState = ReturnType<typeof parseHealthState>
export function serializeHealthState(state: HealthState) {
  return {
    ...(state.q ? { q: state.q } : {}),
    ...(state.group ? { group: state.group } : {}),
    ...(state.kind ? { kind: state.kind } : {}),
    ...(state.severity ? { severity: state.severity } : {}),
    ...(state.sort !== 'priority' ? { sort: state.sort } : {}),
    ...(state.page > 1 ? { page: String(state.page) } : {}),
    ...(state.pageSize !== 20 ? { page_size: String(state.pageSize) } : {}),
    ...(state.detail ? { detail: state.detail } : {}),
  }
}
