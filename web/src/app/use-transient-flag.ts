import { computed, onScopeDispose, ref, type ComputedRef } from 'vue'

export interface TransientFlag {
  value: ComputedRef<boolean>
  show(): void
  clear(): void
}

export function useTransientFlag(durationMs: number): TransientFlag {
  const value = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined

  function clear(): void {
    if (timer !== undefined) clearTimeout(timer)
    timer = undefined
    value.value = false
  }

  function show(): void {
    clear()
    value.value = true
    timer = setTimeout(() => {
      timer = undefined
      value.value = false
    }, durationMs)
  }

  onScopeDispose(clear)
  return { value: computed(() => value.value), show, clear }
}
