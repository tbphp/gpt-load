import { onScopeDispose } from 'vue'

export interface AbortControllerPool {
  create(): AbortController
  release(controller: AbortController): void
  abortAll(): void
}

export function useAbortControllerPool(): AbortControllerPool {
  const controllers = new Set<AbortController>()

  function create(): AbortController {
    const controller = new AbortController()
    controllers.add(controller)
    return controller
  }

  function release(controller: AbortController): void {
    controllers.delete(controller)
  }

  function abortAll(): void {
    for (const controller of controllers) controller.abort()
    controllers.clear()
  }

  onScopeDispose(abortAll)
  return { create, release, abortAll }
}
