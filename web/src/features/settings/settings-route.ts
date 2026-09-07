import type { LocationQuery, LocationQueryRaw } from 'vue-router'

import { isCanonicalRouteQuery, scalarRouteQuery } from '@/app/route-query'

export type SettingsSection =
  'routing' | 'connection' | 'reliability' | 'browser-access' | 'data-maintenance' | 'system'

const sections = new Set<SettingsSection>([
  'routing',
  'connection',
  'reliability',
  'browser-access',
  'data-maintenance',
  'system',
])

export function parseSettingsSection(query: LocationQuery): SettingsSection {
  const value = scalarRouteQuery(query.section)
  return value !== undefined && sections.has(value as SettingsSection)
    ? (value as SettingsSection)
    : 'routing'
}

export function serializeSettingsRouteQuery(section: SettingsSection): LocationQueryRaw {
  return section === 'routing' ? {} : { section }
}

export function isCanonicalSettingsRouteQuery(
  query: LocationQuery,
  section: SettingsSection,
): boolean {
  return isCanonicalRouteQuery(query, serializeSettingsRouteQuery(section))
}
