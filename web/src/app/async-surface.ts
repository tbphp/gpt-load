import { defineAsyncComponent, type Component } from 'vue'

import AsyncSurfaceError from '@/components/ui/AsyncSurfaceError.vue'
import AsyncSurfaceLoading from '@/components/ui/AsyncSurfaceLoading.vue'

import { loadingTimings } from './loading-state'

type LazyComponentModule = Component | { default: Component }

export function lazySurface(loader: () => Promise<LazyComponentModule>) {
  return defineAsyncComponent({
    loader,
    loadingComponent: AsyncSurfaceLoading,
    errorComponent: AsyncSurfaceError,
    delay: loadingTimings.delayMs,
    timeout: 30_000,
    suspensible: false,
    onError(_error, retry, fail, attempts) {
      if (attempts <= 2) {
        retry()
        return
      }
      fail()
    },
  })
}
