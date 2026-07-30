import { createMemoryHistory } from 'vue-router'

const contractPaths = vi.hoisted(() => ({
  home: '/contract-home',
  login: '/contract-login',
  import: '/contract-import',
  'group-detail': '/contract-groups/:id',
  'access-keys': '/contract-access-keys',
  monitor: '/contract-monitor',
  settings: '/contract-settings',
  'model-prices': '/contract-settings/model-prices',
}))

vi.mock('./page-routes', () => ({
  pageRouteEntries: Object.entries(contractPaths).map(([name, path]) => ({ name, path })),
  pagePath(name: string): string {
    const path = contractPaths[name as keyof typeof contractPaths]
    if (path === undefined) throw new Error(`unexpected page route ${name}`)
    return path
  },
  pagePathMatches: () => true,
}))

import { createAppRouter } from './router'

describe('application router page path source', () => {
  it('derives every explicit page path from the shared page path resolver', () => {
    const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
    const actual = Object.fromEntries(
      router
        .getRoutes()
        .filter((route) => route.name !== 'not-found')
        .map((route) => [route.name, route.path]),
    )

    expect(actual).toEqual(contractPaths)
  })
})
