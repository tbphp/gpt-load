import { onScopeDispose } from 'vue'

export interface DebouncedAction {
  schedule(action: () => void): void
  cancel(): void
}

export function useDebouncedAction(delayMs: number): DebouncedAction {
  let timer: ReturnType<typeof setTimeout> | undefined

  function cancel(): void {
    if (timer !== undefined) clearTimeout(timer)
    timer = undefined
  }

  function schedule(action: () => void): void {
    cancel()
    timer = setTimeout(() => {
      timer = undefined
      action()
    }, delayMs)
  }

  onScopeDispose(cancel)
  return { schedule, cancel }
}
