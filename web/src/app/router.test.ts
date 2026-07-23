import { createMemoryHistory } from 'vue-router'

import { createAppRouter, safeRedirect, type RouterAuth } from './router'

function createAuth(hasCredential: boolean): RouterAuth {
  return {
    hasCredential: () => hasCredential,
  }
}

describe('application routes', () => {
  it.each([
    '/',
    '/login',
    '/import',
    '/groups/42',
    '/access-keys',
    '/monitor?tab=requests',
    '/settings',
  ])('resolves the explicit page route %s', (path) => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    expect(router.resolve(path).matched).not.toHaveLength(0)
  })

  it('does not install a client-side catch-all route', () => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    expect(router.resolve('/api/unknown').matched).toHaveLength(0)
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

    await router.push('/monitor?tab=requests')

    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query).toEqual({
      redirect: '/monitor?tab=requests',
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
    '/login',
  ])('rejects unsafe redirect %s', (redirect) => {
    const router = createAppRouter(createAuth(true), createMemoryHistory())

    expect(safeRedirect(redirect, router)).toBe('/')
  })

  it.each(['/', '/access-keys', '/groups/42', '/monitor?tab=requests'])(
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
