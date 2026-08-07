import type { LocationQueryRaw, RouteLocationRaw } from 'vue-router'

import { pageRouteEntries } from './page-routes'

const sharedPageRouteNames = {
  home: 'home',
  login: 'login',
  import: 'import',
  groups: 'groups',
  groupDetail: 'group-detail',
  accessKeys: 'access-keys',
  monitor: 'monitor',
  models: 'models',
  settings: 'settings',
} as const

function validateSharedPageRouteNames(): void {
  const manifestNames = new Set(pageRouteEntries.map((route) => route.name))
  const locationNames = Object.values(sharedPageRouteNames)
  if (
    manifestNames.size !== locationNames.length ||
    locationNames.some((name) => !manifestNames.has(name))
  ) {
    throw new Error('Page route locations must cover the shared page route manifest')
  }
}

validateSharedPageRouteNames()

export const pageRouteNames = Object.freeze({
  ...sharedPageRouteNames,
  notFound: 'not-found',
})

function namedLocation(name: string, query?: LocationQueryRaw): RouteLocationRaw {
  return query === undefined ? { name } : { name, query }
}

export function homeLocation(): RouteLocationRaw {
  return namedLocation(pageRouteNames.home)
}

export function loginLocation(redirect?: string): RouteLocationRaw {
  return namedLocation(pageRouteNames.login, redirect === undefined ? undefined : { redirect })
}

export function importLocation(query?: LocationQueryRaw): RouteLocationRaw {
  return namedLocation(pageRouteNames.import, query ?? { mode: 'new' })
}

export function groupsLocation(query?: LocationQueryRaw): RouteLocationRaw {
  return namedLocation(pageRouteNames.groups, query)
}

export function groupDetailLocation(
  id: string | number,
  query?: LocationQueryRaw,
): RouteLocationRaw {
  return query === undefined
    ? { name: pageRouteNames.groupDetail, params: { id } }
    : { name: pageRouteNames.groupDetail, params: { id }, query }
}

export function accessKeysLocation(query?: LocationQueryRaw): RouteLocationRaw {
  return namedLocation(pageRouteNames.accessKeys, query)
}

export function monitorLocation(query?: LocationQueryRaw): RouteLocationRaw {
  return namedLocation(pageRouteNames.monitor, query)
}

export function modelsLocation(query?: LocationQueryRaw): RouteLocationRaw {
  return namedLocation(pageRouteNames.models, query)
}

export function settingsLocation(): RouteLocationRaw {
  return namedLocation(pageRouteNames.settings)
}

export function notFoundLocation(pathMatch: string[]): RouteLocationRaw {
  return {
    name: pageRouteNames.notFound,
    params: { pathMatch },
    replace: true,
  }
}
