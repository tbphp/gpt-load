import type { HealthAccessKey, HealthCredential, HealthReport } from '@modern/api/health'
import type { GroupRow } from '@modern/api/groups'
import type { RouteLocationRaw } from 'vue-router'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import type { HealthKind } from './health-state'

export interface HealthIssue {
  key: string
  kind: HealthKind
  name: string
  identityType: 'group' | 'credential' | 'access_key'
  groupID?: number
  groupName?: string
  credentialID?: number
  accessKeyID?: number
  severity: 'danger' | 'warning'
  priority: number
  reason: string
  impact: string
  recovery: string
  recoveryAt: number | null
  remaining?: number
  creditCount?: number
  credential?: HealthCredential
  accessKey?: HealthAccessKey
}
type Translate = (key: string, values?: Record<string, string | number>) => string
export function healthTime(value: number | null, locale: string, full = false): string {
  return value
    ? dateFormatter(locale, {
        ...(full ? { year: 'numeric' as const } : {}),
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hourCycle: 'h23',
      }).format(value)
    : '—'
}
export function healthIssues(
  report: HealthReport,
  groups: readonly GroupRow[],
  t: Translate,
): HealthIssue[] {
  const issues: HealthIssue[] = []
  const unavailable = new Set(
    groups
      .filter((row) => ['no_credentials', 'no_models', 'unavailable'].includes(row.availability))
      .map((row) => row.id),
  )
  for (const group of groups.filter((row) => unavailable.has(row.id))) {
    issues.push({
      key: `group:${group.id}`,
      kind: 'group',
      identityType: 'group',
      name: group.name,
      groupID: group.id,
      groupName: group.name,
      severity: 'danger',
      priority: 0,
      reason: t('health.groupReasons.' + group.availability),
      impact: t('health.impacts.group'),
      recovery: t('health.recovery.inspectGroup'),
      recoveryAt: null,
    })
  }
  for (const [kind, rows] of [
    ['isolated', report.isolated],
    ['cooldown', report.cooldown],
  ] as const) {
    for (const row of rows)
      issues.push({
        key: `${kind}:${row.id}`,
        kind,
        identityType: 'credential',
        name: row.identity || t('health.account'),
        groupID: row.groupID,
        groupName: row.groupName,
        credentialID: row.id,
        severity: kind === 'isolated' ? 'danger' : 'warning',
        priority: unavailable.has(row.groupID) ? 1 : kind === 'isolated' ? 3 : 4,
        reason: t('health.failures.' + row.failureCategory),
        impact: t('health.impacts.credential'),
        recovery: t('health.recovery.' + row.recovery),
        recoveryAt: row.recoveryAt,
        credential: row,
      })
  }
  for (const row of report.accessKeys)
    issues.push({
      key: `access_key:${row.id}`,
      kind: 'access_key',
      identityType: 'access_key',
      name: row.name,
      accessKeyID: row.id,
      severity: 'danger',
      priority: 2,
      reason: t('health.rulesBlocked', { count: row.rules.length }),
      impact: t('health.impacts.access_key'),
      recovery: t(row.recoverable ? 'health.recovery.window' : 'health.recovery.quota'),
      recoveryAt: row.recoveryAt,
      accessKey: row,
    })
  for (const row of report.quotas)
    issues.push({
      key: `quota:${row.id}`,
      kind: 'quota',
      identityType: 'credential',
      name: row.identity || t('health.subscription'),
      groupID: row.groupID,
      groupName: row.groupName,
      credentialID: row.id,
      severity: 'warning',
      priority: 5,
      reason: t('health.quotaRemaining', { percent: Math.round(row.remaining * 1000) / 10 }),
      impact: t('health.impacts.quota'),
      recovery: t('health.recovery.subscription'),
      recoveryAt: row.resetAt,
      remaining: row.remaining * 100,
    })
  for (const row of report.credits)
    issues.push({
      key: `credit:${row.id}`,
      kind: 'credit',
      identityType: 'credential',
      name: row.identity || t('health.subscription'),
      groupID: row.groupID,
      groupName: row.groupName,
      credentialID: row.id,
      severity: 'warning',
      priority: 6,
      reason: t('health.creditsExpiring', { count: row.count }),
      impact: t('health.impacts.credit'),
      recovery: t('health.recovery.credit'),
      recoveryAt: row.expiresAt,
      creditCount: row.count,
    })
  return issues
}
export function compareHealthIssues(
  left: HealthIssue,
  right: HealthIssue,
  locale: string,
  prioritize = true,
): number {
  return (
    (prioritize ? left.priority - right.priority : 0) ||
    left.name.localeCompare(right.name, locale) ||
    left.key.localeCompare(right.key)
  )
}
export function healthManageLocation(issue: HealthIssue): RouteLocationRaw {
  if (issue.accessKeyID)
    return {
      name: 'modern-access-keys',
      query: { panel: 'detail', access_key: String(issue.accessKeyID) },
    }
  return {
    name: 'modern-group-detail',
    params: { id: String(issue.groupID) },
    query: issue.credentialID ? { credential: String(issue.credentialID) } : {},
  }
}
export function healthLogsLocation(issue: HealthIssue): RouteLocationRaw {
  return {
    name: 'modern-logs',
    query: {
      preset: '24h',
      ...(issue.groupID ? { group_id: String(issue.groupID) } : {}),
      ...(issue.credentialID ? { credential_id: String(issue.credentialID) } : {}),
      ...(issue.accessKeyID ? { access_key_id: String(issue.accessKeyID) } : {}),
    },
  }
}
