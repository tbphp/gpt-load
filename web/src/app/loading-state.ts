import {
  computed,
  onScopeDispose,
  readonly,
  ref,
  toValue,
  watch,
  type MaybeRefOrGetter,
  type Ref,
} from 'vue'

export const loadingTimings = {
  delayMs: 140,
  minimumVisibleMs: 280,
} as const

interface StableLoadingOptions {
  delayMs?: number
  minimumVisibleMs?: number
}

export function useStableLoading(
  active: MaybeRefOrGetter<boolean>,
  options: StableLoadingOptions = {},
): Readonly<Ref<boolean>> {
  const delayMs = options.delayMs ?? loadingTimings.delayMs
  const minimumVisibleMs = options.minimumVisibleMs ?? loadingTimings.minimumVisibleMs
  const visible = ref(false)
  let shownAt = 0
  let showTimer: ReturnType<typeof setTimeout> | undefined
  let hideTimer: ReturnType<typeof setTimeout> | undefined

  function clearShowTimer(): void {
    if (showTimer === undefined) return
    clearTimeout(showTimer)
    showTimer = undefined
  }

  function clearHideTimer(): void {
    if (hideTimer === undefined) return
    clearTimeout(hideTimer)
    hideTimer = undefined
  }

  function hide(): void {
    hideTimer = undefined
    visible.value = false
    shownAt = 0
  }

  function sync(nextActive: boolean): void {
    if (nextActive) {
      clearHideTimer()
      if (visible.value || showTimer !== undefined) return
      showTimer = setTimeout(() => {
        showTimer = undefined
        if (!toValue(active)) return
        visible.value = true
        shownAt = Date.now()
      }, delayMs)
      return
    }

    clearShowTimer()
    if (!visible.value || hideTimer !== undefined) return
    const remaining = Math.max(0, minimumVisibleMs - (Date.now() - shownAt))
    if (remaining === 0) {
      hide()
      return
    }
    hideTimer = setTimeout(hide, remaining)
  }

  watch(() => Boolean(toValue(active)), sync, { immediate: true })
  onScopeDispose(() => {
    clearShowTimer()
    clearHideTimer()
  })

  return readonly(visible)
}

interface CollectionLoadingInput {
  pending: MaybeRefOrGetter<boolean>
  placeholder: MaybeRefOrGetter<boolean>
  fetching: MaybeRefOrGetter<boolean>
  hasData: MaybeRefOrGetter<boolean>
  itemCount: MaybeRefOrGetter<number>
}

interface CollectionLoadingOptions {
  fallbackRows?: number
  maximumRows?: number
}

export function useCollectionLoading(
  input: CollectionLoadingInput,
  options: CollectionLoadingOptions = {},
) {
  const fallbackRows = options.fallbackRows ?? 5
  const maximumRows = options.maximumRows ?? 100
  const transitionActive = computed(
    () => Boolean(toValue(input.placeholder)) && Boolean(toValue(input.hasData)),
  )
  const rows = ref(fallbackRows)

  watch(
    transitionActive,
    (active, previous) => {
      if (!active || previous) return
      const current = Math.trunc(toValue(input.itemCount))
      rows.value = Math.min(maximumRows, Math.max(1, current || fallbackRows))
    },
    { immediate: true },
  )

  return {
    initial: useStableLoading(input.pending),
    transition: useStableLoading(transitionActive),
    refreshing: computed(
      () =>
        Boolean(toValue(input.hasData)) &&
        Boolean(toValue(input.fetching)) &&
        !Boolean(toValue(input.placeholder)),
    ),
    rows: readonly(rows),
  }
}
