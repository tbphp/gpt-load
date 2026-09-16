import {
  computed,
  getCurrentScope,
  isRef,
  onScopeDispose,
  readonly,
  ref,
  watch,
  type ComputedRef,
  type Ref,
} from 'vue'

function normalizeSeconds(value: number): number {
  return Math.max(1, Math.ceil(Number.isFinite(value) ? value : 0))
}

export interface Countdown {
  seconds: Readonly<Ref<number>>
  active: ComputedRef<boolean>
  reset(value: number): void
}

export function useCountdown(initialValue: number | Ref<number>): Countdown {
  const seconds = ref(normalizeSeconds(isRef(initialValue) ? initialValue.value : initialValue))
  let timer: ReturnType<typeof setInterval> | undefined

  function stopTimer(): void {
    if (timer !== undefined) {
      clearInterval(timer)
      timer = undefined
    }
  }

  function startTimer(): void {
    stopTimer()
    if (seconds.value === 0) return
    timer = setInterval(() => {
      seconds.value = Math.max(0, seconds.value - 1)
      if (seconds.value === 0) stopTimer()
    }, 1_000)
  }

  function reset(value: number): void {
    seconds.value = normalizeSeconds(value)
    startTimer()
  }

  startTimer()

  if (isRef(initialValue)) {
    watch(initialValue, (value) => reset(value))
  }
  if (getCurrentScope()) {
    onScopeDispose(stopTimer)
  }

  return {
    seconds: readonly(seconds),
    active: computed(() => seconds.value > 0),
    reset,
  }
}
