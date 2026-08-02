import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, ref, shallowRef, watch, type ComputedRef, type Ref, type ShallowRef } from 'vue'

import type { ApiClient } from '@/api/client'
import { controlQueryKeys } from '@/app/query-keys'
import {
  createEmptyHomeStatistics,
  homeStatisticsQueryOptions,
  type HomeRange,
  type HomeStatisticsDto,
} from '@/app/resources/home'
import { useVisibleRefetch } from '@/app/use-visible-refetch'

export type HomeStatisticsState =
  | { kind: 'initial'; requestedRange: HomeRange }
  | { kind: 'ready'; selectedRange: HomeRange; snapshot: HomeStatisticsDto }
  | {
      kind: 'switching'
      selectedRange: HomeRange
      targetRange: HomeRange
      snapshot: HomeStatisticsDto
    }
  | {
      kind: 'stale'
      selectedRange: HomeRange
      snapshot: HomeStatisticsDto
      error: unknown
    }

export interface HomeStatisticsPresenter {
  state: Readonly<ShallowRef<HomeStatisticsState>>
  selectedRange: ComputedRef<HomeRange>
  targetRange: ComputedRef<HomeRange | null>
  lastSuccessfulObservedAtMS: Readonly<Ref<number | null>>
  selectRange(range: HomeRange): void
  retry(): Promise<void>
}

export interface HomeStatisticsPresenterOptions {
  initialRange?: HomeRange
  now?: () => number
}

interface HandledQueryUpdates {
  data: number
  error: number
}

export function beginHomeStatisticsRange(
  current: HomeStatisticsState,
  targetRange: HomeRange,
): HomeStatisticsState {
  switch (current.kind) {
    case 'initial':
      return current.requestedRange === targetRange
        ? current
        : { kind: 'initial', requestedRange: targetRange }
    case 'ready':
    case 'stale':
      return current.selectedRange === targetRange
        ? current
        : {
            kind: 'switching',
            selectedRange: current.selectedRange,
            targetRange,
            snapshot: current.snapshot,
          }
    case 'switching':
      if (current.targetRange === targetRange) return current
      if (current.selectedRange === targetRange) {
        return {
          kind: 'ready',
          selectedRange: current.selectedRange,
          snapshot: current.snapshot,
        }
      }
      return { ...current, targetRange }
  }
}

export function commitHomeStatisticsSnapshot(
  current: HomeStatisticsState,
  snapshot: HomeStatisticsDto,
): HomeStatisticsState {
  const expectedRange =
    current.kind === 'initial'
      ? current.requestedRange
      : current.kind === 'switching'
        ? current.targetRange
        : current.selectedRange
  if (snapshot.range !== expectedRange) return current
  return {
    kind: 'ready',
    selectedRange: expectedRange,
    snapshot,
  }
}

export function rejectHomeStatisticsSnapshot(
  current: HomeStatisticsState,
  error: unknown,
  observedAtMS: number = Date.now(),
): HomeStatisticsState {
  if (current.kind === 'initial') {
    return {
      kind: 'stale',
      selectedRange: current.requestedRange,
      snapshot: createEmptyHomeStatistics(current.requestedRange, observedAtMS),
      error,
    }
  }
  if (current.kind === 'switching') {
    return {
      kind: 'stale',
      selectedRange: current.selectedRange,
      snapshot: current.snapshot,
      error,
    }
  }
  return {
    kind: 'stale',
    selectedRange: current.selectedRange,
    snapshot: current.snapshot,
    error,
  }
}

export function useHomeStatisticsPresenter(
  client: ApiClient,
  options: HomeStatisticsPresenterOptions = {},
): HomeStatisticsPresenter {
  const queryClient = useQueryClient()
  const now = options.now ?? Date.now
  const initialRange = options.initialRange ?? '24h'
  const requestedRange = ref<HomeRange>(initialRange)
  const state = shallowRef<HomeStatisticsState>({
    kind: 'initial',
    requestedRange: initialRange,
  })
  const lastSuccessfulObservedAtMS = ref<number | null>(null)
  const handledUpdates = new Map<HomeRange, HandledQueryUpdates>()
  const statisticsQuery = useQuery(homeStatisticsQueryOptions(client, requestedRange))
  let manualRefetch: Promise<void> | null = null

  function reconcileQueryState(): void {
    const range = requestedRange.value
    const queryState = queryClient.getQueryState<HomeStatisticsDto>(
      controlQueryKeys.home.statistics(range),
    )
    if (!queryState) return
    const handled = handledUpdates.get(range) ?? { data: 0, error: 0 }
    if (
      handled.data === queryState.dataUpdateCount &&
      handled.error === queryState.errorUpdateCount
    ) {
      return
    }
    handledUpdates.set(range, {
      data: queryState.dataUpdateCount,
      error: queryState.errorUpdateCount,
    })

    if (queryState.status === 'success' && queryState.data !== undefined) {
      const next = commitHomeStatisticsSnapshot(state.value, queryState.data)
      if (next !== state.value) {
        state.value = next
        lastSuccessfulObservedAtMS.value = queryState.data.observed_at_ms
      }
      return
    }
    if (queryState.status === 'error' && queryState.error !== null) {
      const next = rejectHomeStatisticsSnapshot(state.value, queryState.error, now())
      state.value = next
      if (next.kind === 'stale' && requestedRange.value !== next.selectedRange) {
        requestedRange.value = next.selectedRange
      }
    }
  }

  watch(
    [
      requestedRange,
      statisticsQuery.dataUpdatedAt,
      statisticsQuery.errorUpdateCount,
      statisticsQuery.status,
    ],
    reconcileQueryState,
    { immediate: true },
  )

  function retry(): Promise<void> {
    if (manualRefetch) return manualRefetch
    if (statisticsQuery.isFetching.value) return Promise.resolve()
    manualRefetch = statisticsQuery
      .refetch({ cancelRefetch: false })
      .then(() => undefined)
      .finally(() => {
        manualRefetch = null
      })
    return manualRefetch
  }

  function selectRange(range: HomeRange): void {
    const currentRange = requestedRange.value
    const next = beginHomeStatisticsRange(state.value, range)
    if (next === state.value) {
      if (state.value.kind === 'stale' && state.value.selectedRange === range) {
        void retry()
      }
      return
    }
    state.value = next
    if (currentRange === range) return
    void queryClient.cancelQueries({
      queryKey: controlQueryKeys.home.statistics(currentRange),
      exact: true,
    })
    requestedRange.value = range
  }

  useVisibleRefetch([retry])

  return {
    state,
    selectedRange: computed(() => {
      const current = state.value
      if (current.kind === 'initial') return current.requestedRange
      if (current.kind === 'switching') return current.targetRange
      return current.selectedRange
    }),
    targetRange: computed(() =>
      state.value.kind === 'switching' ? state.value.targetRange : null,
    ),
    lastSuccessfulObservedAtMS,
    selectRange,
    retry,
  }
}
