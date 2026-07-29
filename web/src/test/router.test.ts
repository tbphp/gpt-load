import { createMemoryHistory, createRouter } from 'vue-router'

import { waitForRoute } from './router'

function testRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/one', component: {} },
      { path: '/two', component: {} },
      { path: '/target', component: {} },
    ],
  })
}

describe('waitForRoute', () => {
  it('resolves immediately when the target route is current', async () => {
    const router = testRouter()
    await router.push('/target')

    await expect(waitForRoute(router, '/target')).resolves.toBeUndefined()
  })

  it('ignores unrelated navigation and resolves on the target route', async () => {
    const router = testRouter()
    await router.push('/one')
    let resolved = false
    const target = waitForRoute(router, '/target').then(() => {
      resolved = true
    })

    await router.push('/two')
    await Promise.resolve()
    expect(resolved).toBe(false)

    await router.push('/target')
    await target
    expect(resolved).toBe(true)
  })
})
