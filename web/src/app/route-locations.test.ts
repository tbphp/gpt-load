import { createMemoryHistory } from 'vue-router'

import { pageRouteEntries } from './page-routes'
import {
  accessKeysLocation,
  groupDetailLocation,
  homeLocation,
  importLocation,
  loginLocation,
  modelPricesLocation,
  monitorLocation,
  notFoundLocation,
  pageRouteNames,
  settingsLocation,
} from './route-locations'
import { createAppRouter } from './router'

describe('page route locations', () => {
  it('covers every shared manifest route name exactly once', () => {
    expect(
      Object.values(pageRouteNames).filter((name) => name !== pageRouteNames.notFound),
    ).toEqual(pageRouteEntries.map((route) => route.name))
  })

  it('builds named locations without copying shared page paths', () => {
    expect(homeLocation()).toEqual({ name: 'home' })
    expect(loginLocation('/monitor?tab=logs')).toEqual({
      name: 'login',
      query: { redirect: '/monitor?tab=logs' },
    })
    expect(importLocation({ mode: 'existing', group_id: 42 })).toEqual({
      name: 'import',
      query: { mode: 'existing', group_id: 42 },
    })
    expect(groupDetailLocation(42, { tab: 'keys' })).toEqual({
      name: 'group-detail',
      params: { id: 42 },
      query: { tab: 'keys' },
    })
    expect(accessKeysLocation()).toEqual({ name: 'access-keys' })
    expect(monitorLocation({ tab: 'usage' })).toEqual({
      name: 'monitor',
      query: { tab: 'usage' },
    })
    expect(settingsLocation()).toEqual({ name: 'settings' })
    expect(modelPricesLocation()).toEqual({ name: 'model-prices' })
    expect(notFoundLocation(['missing'])).toEqual({
      name: 'not-found',
      params: { pathMatch: ['missing'] },
      replace: true,
    })
  })

  it('resolves builders through the shared manifest paths', () => {
    const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())

    expect(router.resolve(homeLocation()).path).toBe('/')
    expect(router.resolve(groupDetailLocation(42, { tab: 'keys' })).fullPath).toBe(
      '/groups/42?tab=keys',
    )
    expect(router.resolve(modelPricesLocation()).path).toBe('/settings/model-prices')
  })
})
