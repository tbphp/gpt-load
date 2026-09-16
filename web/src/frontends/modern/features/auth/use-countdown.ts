import { computed, onScopeDispose, ref } from 'vue'

export function useCountdown() {
  const seconds = ref(0)
  let deadline = 0
  let timer: ReturnType<typeof setInterval> | undefined

  function stop(): void {
    if (timer !== undefined) clearInterval(timer)
    timer = undefined
  }

  function tick(): void {
    seconds.value = Math.max(0, Math.ceil((deadline - Date.now()) / 1_000))
    if (seconds.value === 0) stop()
  }

  function start(value: number): void {
    stop()
    deadline = Date.now() + Math.max(1, Math.ceil(Number.isFinite(value) ? value : 1)) * 1_000
    tick()
    timer = setInterval(tick, 1_000)
  }

  onScopeDispose(stop)
  return {
    seconds: computed(() => seconds.value),
    active: computed(() => seconds.value > 0),
    start,
  }
}
