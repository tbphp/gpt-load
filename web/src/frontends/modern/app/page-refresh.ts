import {
  computed,
  inject,
  onScopeDispose,
  provide,
  ref,
  shallowRef,
  toValue,
  type InjectionKey,
  type MaybeRefOrGetter,
  type ShallowRef,
} from 'vue'
import { useLoadingFeedback } from '../components/ui/loading'

// 顶栏的刷新入口是公共结构，页面只登记自己的刷新动作与数据时间。
export interface PageRefreshSource {
  refresh: () => unknown
  pending?: MaybeRefOrGetter<boolean>
  updatedAt?: MaybeRefOrGetter<number | undefined>
}

const pageRefreshKey: InjectionKey<ShallowRef<PageRefreshSource | undefined>> =
  Symbol('modern-page-refresh')

export function providePageRefresh() {
  const source = shallowRef<PageRefreshSource | undefined>()
  const running = ref(false)
  const pending = useLoadingFeedback(
    () => running.value || (toValue(source.value?.pending) ?? false),
  )
  provide(pageRefreshKey, source)
  return {
    available: computed(() => source.value !== undefined),
    pending,
    updatedAt: computed(() => toValue(source.value?.updatedAt)),
    run: async () => {
      if (running.value || !source.value) return
      running.value = true
      try {
        await source.value.refresh()
      } finally {
        running.value = false
      }
    },
  }
}

export function usePageRefresh(source: PageRefreshSource): void {
  const host = inject(pageRefreshKey)
  if (!host) throw new Error('MODERN_PAGE_REFRESH_NOT_PROVIDED')
  host.value = source
  onScopeDispose(() => {
    if (host.value === source) host.value = undefined
  })
}
