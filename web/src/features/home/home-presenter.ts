import type { GroupSummary } from '@/api/control/types'
import type { HealthProblemKeyDto, RuntimeHealthDto } from '@/app/resources/health'
import type { UsageReportDto } from '@/app/resources/usage'
import { groupDetailLocation, monitorLocation } from '@/app/route-locations'

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

function healthFromState(state: HomeHealthState): RuntimeHealthDto | undefined {
  if (state.kind === 'normal' || state.kind === 'problem' || state.kind === 'stale') {
    return state.health
  }
  return undefined
}

function usageFromState(state: HomeUsageState): UsageReportDto | undefined {
  if (state.kind === 'data' || state.kind === 'empty' || state.kind === 'stale') {
    return state.report
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
