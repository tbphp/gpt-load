import { defineAsyncComponent, type Component } from 'vue'

import AsyncSurfaceError from '@/components/ui/AsyncSurfaceError.vue'
import AsyncSurfaceLoading from '@/components/ui/AsyncSurfaceLoading.vue'

type LazyComponentModule = Component | { default: Component }

export function lazySurface(loader: () => Promise<LazyComponentModule>) {
  return defineAsyncComponent({
    loader,
    loadingComponent: AsyncSurfaceLoading,
    errorComponent: AsyncSurfaceError,
    delay: 0,
    timeout: 30_000,
    suspensible: false,
  })
}
