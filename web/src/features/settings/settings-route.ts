import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import { isCanonicalRouteQuery, scalarRouteQuery } from '@/app/route-query'

export type SettingsSection =
  'forwarding' | 'affinity' | 'headers' | 'browser-access' | 'logs' | 'system'

const sections = new Set<SettingsSection>([
  'forwarding',
  'affinity',
  'headers',
  'browser-access',
  'logs',
  'system',
])

export function parseSettingsSection(query: LocationQuery): SettingsSection {
  const value = scalarRouteQuery(query.section)
  return value !== undefined && sections.has(value as SettingsSection)
    ? (value as SettingsSection)
    : 'forwarding'
}

export function serializeSettingsRouteQuery(section: SettingsSection): LocationQueryRaw {
  return section === 'forwarding' ? {} : { section }
}

export function isCanonicalSettingsRouteQuery(
  query: LocationQuery,
  section: SettingsSection,
): boolean {
  return isCanonicalRouteQuery(query, serializeSettingsRouteQuery(section))
}
