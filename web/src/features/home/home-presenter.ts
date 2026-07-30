import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, ref, shallowRef, watch, type ComputedRef, type Ref, type ShallowRef } from 'vue'

import type { ApiClient } from '@/api/client'
import type { GroupSummary } from '@/api/control/types'
import { controlQueryKeys } from '@/app/query-keys'
import {
  createEmptyHomeStatistics,
  homeStatisticsQueryOptions,
  type HomeRange,
  type HomeStatisticsDto,
} from '@/app/resources/home'
import type { HealthProblemKeyDto, RuntimeHealthDto } from '@/app/resources/health'
import type { UsageReportDto } from '@/app/resources/usage'
import { groupDetailLocation, monitorLocation } from '@/app/route-locations'
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
  isFetching: Readonly<Ref<boolean>>
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
      return current.kind === 'initial' ? current.requestedRange : current.selectedRange
    }),
    targetRange: computed(() =>
      state.value.kind === 'switching' ? state.value.targetRange : null,
    ),
    lastSuccessfulObservedAtMS,
    isFetching: statisticsQuery.isFetching,
    selectRange,
    retry,
  }
}

// Transitional compatibility for the pre-Ledger Home view. Task 13 replaces
// that view and its health/usage presentation with the state machine above.
export type HomeQueryResult<T> =
  | { status: 'loading' }
  | { status: 'success'; data: T }
  | { status: 'error'; failedAt: string; data?: T }

export interface HomeProblemGroup {
  groupId: number
  groupName: string
  cooldownKeys: HealthProblemKeyDto[]
  blacklistedKeys: HealthProblemKeyDto[]
}

export type HomeInventoryState =
  | { kind: 'loading' }
  | { kind: 'ready'; groups: GroupSummary[] }
  | { kind: 'empty' }
  | { kind: 'stale'; groups: GroupSummary[] }
  | { kind: 'error'; retryable: true }

export type HomeHealthState =
  | { kind: 'loading' }
  | { kind: 'normal'; health: RuntimeHealthDto }
  | { kind: 'problem'; health: RuntimeHealthDto; groups: HomeProblemGroup[] }
  | { kind: 'stale'; health: RuntimeHealthDto; failedAt: string }
  | { kind: 'unknown'; retryable: true }

export type HomeUsageState =
  | { kind: 'loading' }
  | { kind: 'data'; report: UsageReportDto }
  | { kind: 'empty'; report: UsageReportDto }
  | { kind: 'stale'; report: UsageReportDto }
  | { kind: 'error'; retryable: true }

export interface HomePipelineWarning {
  droppedTotal: number
  writeFailureTotal: number
}

export interface HomePresentation {
  inventory: HomeInventoryState
  health: HomeHealthState
  usage: HomeUsageState
  zeroGroups: boolean
  problemGroups: HomeProblemGroup[]
  pipelineWarning: HomePipelineWarning | null
  successRate: number | null
  costRanking: UsageReportDto['breakdown']
}

export interface HomePresenterInput {
  inventory: HomeQueryResult<GroupSummary[]>
  health: HomeQueryResult<RuntimeHealthDto>
  usage: HomeQueryResult<UsageReportDto>
}

export function presentHomeInventory(result: HomeQueryResult<GroupSummary[]>): HomeInventoryState {
  if (result.status === 'loading') return { kind: 'loading' }
  if (result.status === 'error') {
    return result.data === undefined
      ? { kind: 'error', retryable: true }
      : { kind: 'stale', groups: [...result.data] }
  }
  return result.data.length === 0 ? { kind: 'empty' } : { kind: 'ready', groups: [...result.data] }
}

export function collectHomeProblemGroups(health: RuntimeHealthDto): HomeProblemGroup[] {
  const groups = new Map<number, HomeProblemGroup>()
  const ensureGroup = (key: HealthProblemKeyDto): HomeProblemGroup => {
    const current = groups.get(key.group_id)
    if (current) return current
    const created: HomeProblemGroup = {
      groupId: key.group_id,
      groupName: key.group_name,
      cooldownKeys: [],
      blacklistedKeys: [],
    }
    groups.set(key.group_id, created)
    return created
  }

  for (const key of health.cooldown_keys) ensureGroup(key).cooldownKeys.push(key)
  for (const key of health.blacklisted_keys) ensureGroup(key).blacklistedKeys.push(key)
  return [...groups.values()].sort((left, right) => left.groupId - right.groupId)
}

export function presentHomeHealth(result: HomeQueryResult<RuntimeHealthDto>): HomeHealthState {
  if (result.status === 'loading') return { kind: 'loading' }
  if (result.status === 'error') {
    return result.data === undefined
      ? { kind: 'unknown', retryable: true }
      : { kind: 'stale', health: result.data, failedAt: result.failedAt }
  }

  const groups = collectHomeProblemGroups(result.data)
  return groups.length === 0
    ? { kind: 'normal', health: result.data }
    : { kind: 'problem', health: result.data, groups }
}

export function presentHomeUsage(result: HomeQueryResult<UsageReportDto>): HomeUsageState {
  if (result.status === 'loading') return { kind: 'loading' }
  if (result.status === 'error') {
    return result.data === undefined
      ? { kind: 'error', retryable: true }
      : { kind: 'stale', report: result.data }
  }
  return result.data.summary.request_count === 0
    ? { kind: 'empty', report: result.data }
    : { kind: 'data', report: result.data }
}

function healthFromState(current: HomeHealthState): RuntimeHealthDto | undefined {
  if (current.kind === 'normal' || current.kind === 'problem' || current.kind === 'stale') {
    return current.health
  }
  return undefined
}

function usageFromState(current: HomeUsageState): UsageReportDto | undefined {
  if (current.kind === 'data' || current.kind === 'empty' || current.kind === 'stale') {
    return current.report
  }
  return undefined
}

function pipelineWarning(health: RuntimeHealthDto | undefined): HomePipelineWarning | null {
  if (
    health === undefined ||
    (health.request_log.dropped_total === 0 && health.request_log.write_failure_total === 0)
  ) {
    return null
  }
  return {
    droppedTotal: health.request_log.dropped_total,
    writeFailureTotal: health.request_log.write_failure_total,
  }
}

export function presentHome(input: HomePresenterInput): HomePresentation {
  const inventory = presentHomeInventory(input.inventory)
  const health = presentHomeHealth(input.health)
  const usage = presentHomeUsage(input.usage)
  const healthReport = healthFromState(health)
  const usageReport = usageFromState(usage)

  return {
    inventory,
    health,
    usage,
    zeroGroups: inventory.kind === 'empty',
    problemGroups:
      health.kind === 'problem'
        ? health.groups
        : healthReport === undefined
          ? []
          : collectHomeProblemGroups(healthReport),
    pipelineWarning: pipelineWarning(healthReport),
    successRate:
      usageReport === undefined || usageReport.summary.request_count === 0
        ? null
        : (usageReport.summary.success_count / usageReport.summary.request_count) * 100,
    costRanking: usageReport?.breakdown.slice(0, 5) ?? [],
  }
}

export function problemKeysLocation(groupId: number) {
  return groupDetailLocation(groupId, { tab: 'keys', key_state: 'problem' })
}

export function failureLogsLocation(report: UsageReportDto) {
  return monitorLocation({
    tab: 'logs',
    status: 'error',
    from_ms: report.from_ms,
    to_ms: report.to_ms,
  })
}

export function usageBreakdownLocation(range: '24h' | '30d', groupId: number, model: string) {
  return monitorLocation({
    tab: 'usage',
    range,
    ...(groupId === 0 ? {} : { group_id: groupId }),
    ...(model === '' ? {} : { model }),
  })
}
