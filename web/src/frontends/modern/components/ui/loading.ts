import {
  computed,
  inject,
  provide,
  shallowReactive,
  onScopeDispose,
  ref,
  toValue,
  watch,
  type InjectionKey,
  type MaybeRefOrGetter,
} from 'vue'

const activityKey: InjectionKey<Set<MaybeRefOrGetter<boolean>>> = Symbol('modern-loading-activity')
export function provideLoadingActivity() {
  const sources = shallowReactive(new Set<MaybeRefOrGetter<boolean>>())
  provide(activityKey, sources)
  return computed(() => [...sources].some((source) => toValue(source)))
}
export function useLoadingActivity(source: MaybeRefOrGetter<boolean>): void {
  const sources = inject(activityKey, undefined)
  if (!sources) return
  sources.add(source)
  onScopeDispose(() => sources.delete(source))
}

// 仅供非阻塞提示延长视觉反馈；列表遮罩、禁用状态应直接使用真实 pending。
export function useLoadingFeedback(source: MaybeRefOrGetter<boolean>) {
  const visible = ref(false)
  let startedAt = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  watch(
    () => toValue(source),
    (pending) => {
      clearTimeout(timer)
      if (pending) {
        if (!visible.value) startedAt = Date.now()
        visible.value = true
      } else if (visible.value) {
        const remaining = Math.max(0, 100 - (Date.now() - startedAt))
        timer = setTimeout(() => {
          visible.value = false
        }, remaining)
      }
    },
    { immediate: true, flush: 'sync' },
  )
  onScopeDispose(() => clearTimeout(timer))
  return visible
}
