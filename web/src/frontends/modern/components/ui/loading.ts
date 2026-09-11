import { onScopeDispose, ref, toValue, watch, type MaybeRefOrGetter } from 'vue'

// 只延长视觉反馈，不延迟请求或数据更新，避免快请求的加载状态一闪而过。
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
