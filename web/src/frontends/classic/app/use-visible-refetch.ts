import { onBeforeUnmount, onMounted } from 'vue'

export function useVisibleRefetch(
  refetchers: ReadonlyArray<() => unknown | Promise<unknown>>,
): void {
  let wasHidden = document.hidden

  function handleVisibilityChange(): void {
    const hidden = document.hidden
    if (wasHidden && !hidden) {
      void Promise.allSettled(refetchers.map((refetch) => Promise.resolve().then(() => refetch())))
    }
    wasHidden = hidden
  }

  onMounted(() => document.addEventListener('visibilitychange', handleVisibilityChange))
  onBeforeUnmount(() => {
    document.removeEventListener('visibilitychange', handleVisibilityChange)
  })
}
