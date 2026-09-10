import {
  Activity,
  Boxes,
  ChartNoAxesCombined,
  House,
  KeyRound,
  Layers2,
  Route,
  ScrollText,
  Settings2,
} from '@lucide/vue'

import modernPageRoutes from '../../../../../internal/webui/modern_page_routes.json'
import pageRoutes from '../../../../../internal/webui/page_routes.json'

const pageRouteEntries = [...pageRoutes.routes, ...modernPageRoutes.routes]

export function pagePath(name: string): string {
  const route = pageRouteEntries.find((entry) => entry.name === name)
  if (!route) throw new Error(`UNKNOWN_MODERN_PAGE_ROUTE: ${name}`)
  return route.path
}

export const navigationItems = [
  { id: 'home', name: 'modern-home', path: pagePath('home'), section: 'workspace', icon: House },
  {
    id: 'groups',
    name: 'modern-groups',
    path: pagePath('groups'),
    section: 'workspace',
    icon: Layers2,
  },
  {
    id: 'models',
    name: 'modern-models',
    path: pagePath('models'),
    section: 'workspace',
    icon: Boxes,
  },
  {
    id: 'accessKeys',
    name: 'modern-access-keys',
    path: pagePath('access-keys'),
    section: 'workspace',
    icon: KeyRound,
  },
  {
    id: 'usage',
    name: 'modern-usage',
    path: pagePath('monitor-usage'),
    section: 'observe',
    icon: ChartNoAxesCombined,
  },
  {
    id: 'logs',
    name: 'modern-logs',
    path: pagePath('monitor-logs'),
    section: 'observe',
    icon: ScrollText,
  },
  {
    id: 'health',
    name: 'modern-health',
    path: pagePath('monitor-health'),
    section: 'observe',
    icon: Activity,
  },
  {
    id: 'inspector',
    name: 'modern-inspector',
    path: pagePath('monitor-inspector'),
    section: 'observe',
    icon: Route,
  },
  {
    id: 'settings',
    name: 'modern-settings',
    path: pagePath('settings'),
    section: 'system',
    icon: Settings2,
  },
] as const

export type NavigationItem = (typeof navigationItems)[number]
export type WorkspaceID = NavigationItem['id']

export const navigationSections = ['workspace', 'observe', 'system'] as const

export function findNavigationItem(name: unknown): NavigationItem | undefined {
  return navigationItems.find((item) => item.name === name)
}
