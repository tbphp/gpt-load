import type { LocationQueryRaw, RouteLocationRaw } from 'vue-router'

import { pageRouteEntries } from './page-routes'

const sharedPageRouteNames = {
  home: 'home',
  login: 'login',
  import: 'import',
  groupDetail: 'group-detail',
  accessKeys: 'access-keys',
  monitor: 'monitor',
  settings: 'settings',
  modelPrices: 'model-prices',
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
  return namedLocation(pageRouteNames.import, query)
}

export function groupDetailLocation(
  id: string | number,
  query?: LocationQueryRaw,
): RouteLocationRaw {
  return query === undefined
    ? { name: pageRouteNames.groupDetail, params: { id } }
    : { name: pageRouteNames.groupDetail, params: { id }, query }
}

export function accessKeysLocation(): RouteLocationRaw {
  return namedLocation(pageRouteNames.accessKeys)
}

export function monitorLocation(query?: LocationQueryRaw): RouteLocationRaw {
  return namedLocation(pageRouteNames.monitor, query)
}

export function settingsLocation(): RouteLocationRaw {
  return namedLocation(pageRouteNames.settings)
}

export function modelPricesLocation(): RouteLocationRaw {
  return namedLocation(pageRouteNames.modelPrices)
}

export function notFoundLocation(pathMatch: string[]): RouteLocationRaw {
  return {
    name: pageRouteNames.notFound,
    params: { pathMatch },
    replace: true,
  }
}
