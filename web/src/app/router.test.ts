import { createMemoryHistory } from 'vue-router'

import pageRouteManifest from '../../../internal/webui/page_routes.json'

import { createAppRouter, safeRedirect, type RouterAuth } from './router'

function createAuth(hasCredential: boolean): RouterAuth {
  return {
    hasCredential: () => hasCredential,
  }
}

describe('application routes', () => {
  it('matches every non-fallback route name and path to the shared JSON manifest', () => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())
    const actual = Object.fromEntries(
      router
        .getRoutes()
        .filter((route) => route.name !== 'not-found')
        .map((route) => [route.name, route.path]),
    )
    const expected = Object.fromEntries(
      pageRouteManifest.routes.map((route) => [route.name, route.path]),
    )

    expect(actual).toEqual(expected)
  })

  it.each([
    '/',
    '/login',
    '/import',
    '/groups/42',
    '/access-keys',
    '/monitor?tab=logs',
    '/settings',
    '/settings/model-prices',
  ])('resolves the explicit page route %s', (path) => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    expect(router.resolve(path).matched).not.toHaveLength(0)
  })

  it('stores stable localization keys instead of hard-coded page titles', () => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())
    const titles = Object.fromEntries(
      router.getRoutes().map((route) => [route.name, route.meta.titleKey]),
    )

    expect(titles).toMatchObject({
      home: 'home.title',
      import: 'shell.import',
      'access-keys': 'shell.accessKeys',
      monitor: 'shell.monitor',
      settings: 'shell.settings',
      'model-prices': 'modelPrices.title',
      'not-found': 'notFound.title',
    })
    expect(JSON.stringify(titles)).not.toMatch(/导入密钥|访问密钥|监控|设置/)
  })

  it('maps the Model Price sibling route to the Settings primary navigation item', () => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    expect(router.resolve('/settings').meta.primaryNav).toBe('settings')
    expect(router.resolve('/settings/model-prices').meta.primaryNav).toBe('settings')
  })

  it('keeps every route component behind a dynamic import boundary', () => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    for (const route of router.getRoutes()) {
      expect(typeof route.components?.default).toBe('function')
    }
  })

  it('loads only the message namespaces declared by the destination route', async () => {
    const loadNamespaces = vi.fn().mockResolvedValue(undefined)
    const router = createAppRouter(createAuth(true), createMemoryHistory(), { loadNamespaces })

    await router.push('/access-keys')
    expect(loadNamespaces).toHaveBeenLastCalledWith(['access-keys'])

    await router.push('/groups/42')
    expect(loadNamespaces).toHaveBeenLastCalledWith(['group', 'import'])

    await router.push('/settings')
    expect(loadNamespaces).toHaveBeenLastCalledWith(['settings', 'model-prices', 'import'])

    await router.push('/')
    expect(loadNamespaces).toHaveBeenLastCalledWith([])
  })

  it('resolves unknown UI paths to a protected NotFound route', () => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    const resolved = router.resolve('/missing/page')
    expect(resolved.name).toBe('not-found')
    expect(resolved.meta.requiresAuth).toBe(true)
  })

  it.each(['/SETTINGS', '/settings/'])(
    'uses strict case-sensitive matching for non-canonical page path %s',
    (path) => {
      const router = createAppRouter(createAuth(true), createMemoryHistory())

      expect(router.resolve(path).name).toBe('not-found')
    },
  )

  it('redirects an encoded path separator to the not-found route', async () => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    await router.push('/groups/a%2Fb')

    expect(router.currentRoute.value.name).toBe('not-found')
  })

  it('marks every non-login route as protected', () => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    const routes = router.getRoutes()
    expect(routes.find((route) => route.name === 'login')?.meta.requiresAuth).not.toBe(true)
    expect(routes.filter((route) => route.name !== 'login')).not.toHaveLength(0)
    expect(
      routes
        .filter((route) => route.name !== 'login')
        .every((route) => route.meta.requiresAuth === true),
    ).toBe(true)
  })

  it('redirects a protected route without a credential to login', async () => {
    const router = createAppRouter(createAuth(false), createMemoryHistory())

    await router.push('/monitor?tab=logs')

    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query).toEqual({
      redirect: '/monitor?tab=logs',
    })
  })

  it('allows a protected route when a credential exists', async () => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    await router.push('/access-keys')

    expect(router.currentRoute.value.name).toBe('access-keys')
  })

  it('keeps login public', async () => {
    const router = createAppRouter(createAuth(false), createMemoryHistory())

    await router.push('/login')

    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query).toEqual({})
  })

  it.each([
    'https://evil.example/',
    '//evil.example/',
    '/api/unknown',
    '/not-registered',
    '/\\evil.example',
    '/groups/%5Cevil',
    '/groups/%5cevil',
    '/groups/%',
    '/groups/a%2Fb',
    '/SETTINGS',
    '/settings/',
    '/login',
  ])('rejects unsafe redirect %s', (redirect) => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    expect(safeRedirect(redirect, router)).toBe('/')
  })

  it.each(['/', '/access-keys', '/groups/42', '/monitor?tab=logs', '/settings/model-prices'])(
    'accepts registered relative redirect %s',
    (redirect) => {
      const router = createAppRouter(createAuth(true), createMemoryHistory())

      expect(safeRedirect(redirect, router)).toBe(redirect)
    },
  )

  it('never puts the AUTH_KEY in route or history state', async () => {
    const authKeyCanary = 'AUTH_KEY_CANARY'
    const history = createMemoryHistory()
    const auth = {
      hasCredential: () => authKeyCanary.length > 0,
      getAuthKey: () => authKeyCanary,
    }
    const router = createAppRouter(auth, history)

    await router.push(`/groups/42?tab=${encodeURIComponent('details')}`)

    expect(JSON.stringify(router.currentRoute.value)).not.toContain(authKeyCanary)
    expect(JSON.stringify(router.currentRoute.value.query)).not.toContain(authKeyCanary)
    expect(JSON.stringify(history.state)).not.toContain(authKeyCanary)
  })
})
