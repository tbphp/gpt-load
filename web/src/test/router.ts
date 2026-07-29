import type { Router } from 'vue-router'

export function waitForRoute(router: Router, expectedFullPath: string): Promise<void> {
  if (router.currentRoute.value.fullPath === expectedFullPath) {
    return Promise.resolve()
  }

  return new Promise((resolve) => {
    const remove = router.afterEach((to) => {
      if (to.fullPath !== expectedFullPath) return
      remove()
      resolve()
    })
  })
}
