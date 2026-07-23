import { createMemoryHistory } from 'vue-router'

import { createAppRouter } from './router'

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
    const router = createAppRouter(createMemoryHistory())

    expect(router.resolve(path).matched).not.toHaveLength(0)
  })

  it('does not install a client-side catch-all route', () => {
    const router = createAppRouter(createMemoryHistory())

    expect(router.resolve('/api/unknown').matched).toHaveLength(0)
  })
})
