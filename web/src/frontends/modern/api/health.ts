import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'

export interface HealthCounts {
  credentials: number
  available: number
  cooldown: number
  blacklisted: number
}
export interface HealthGroup {
  id: number
  name: string
  enabled: boolean
  counts: HealthCounts & { modelCooldown: number }
}
export interface HealthCredential {
  id: number
  groupID: number
  groupName: string
  identity: string
  failureCategory: string
  statusCode: number | null
  failures: number
  successes: number
  problems: number
  consecutive: number
  weight: number
  recovery: 'cooldown_expiry' | 'validation_probe' | 'configuration_required'
  recoveryAt: number | null
}
export interface HealthQuota {
  id: number
  groupID: number
  groupName: string
  identity: string
  remaining: number
  resetAt: number | null
}
export interface HealthCredit {
  id: number
  groupID: number
  groupName: string
  identity: string
  count: number
  expiresAt: number
}
export interface HealthBlockingRule {
  id: number
  kind: 'total' | 'periodic'
  limit: string
  used: string
  remaining: string
  period: number
  endsAt: number | null
}
export interface HealthAccessKey {
  id: number
  name: string
  mask: string
  recoverable: boolean
  recoveryAt: number | null
  rules: HealthBlockingRule[]
}
export interface HealthReport {
  observedAt: number
  statsWindow: number
  counts: HealthCounts
  groups: HealthGroup[]
  cooldown: HealthCredential[]
  isolated: HealthCredential[]
  quotas: HealthQuota[]
  credits: HealthCredit[]
  accessKeys: HealthAccessKey[]
}
const timestamp = (value: unknown) => (value == null ? null : integer(value))
// 无邮箱兜底中的数据库 ID 不进入新版展示。
const identity = (value: unknown) => text(value).replace(/^Subscription #\d+$/, '')
function counts(value: unknown): HealthCounts {
  const row = record(value)
  const result = {
    credentials: integer(row.credentials),
    available: integer(row.available),
    cooldown: integer(row.cooldown),
    blacklisted: integer(row.blacklisted),
  }
  if (result.credentials !== result.available + result.cooldown + result.blacklisted)
    throw new InvalidResponseError()
  return result
}
function credential(value: unknown): HealthCredential {
  const row = record(value),
    recovery = record(row.recovery)
  return {
    id: integer(row.credential_id, 1),
    groupID: integer(row.group_id, 1),
    groupName: text(row.group_name),
    identity: identity(row.identity),
    failureCategory: text(row.last_failure_category),
    statusCode: timestamp(row.last_status_code),
    failures: integer(row.failure_count),
    successes: integer(row.recent_success_count),
    problems: integer(row.recent_problem_count),
    consecutive: integer(row.consecutive_problem_count),
    weight: integer(row.weight),
    recovery: oneOf(recovery.mode, [
      'cooldown_expiry',
      'validation_probe',
      'configuration_required',
    ]),
    recoveryAt: timestamp(recovery.at_ms),
  }
}
function decimal(value: unknown): string {
  const result = text(value)
  if (!/^(0|[1-9]\d*)(\.\d{1,9})?$/.test(result)) throw new InvalidResponseError()
  return result
}
export async function getHealth(client: ApiClient, signal: AbortSignal): Promise<HealthReport> {
  const row = record(await client.request('/api/health', { signal }))
  return {
    observedAt: integer(row.observed_at_ms),
    statsWindow: integer(row.stats_window_seconds, 1),
    counts: counts(row.counts),
    groups: list(row.groups).map((value) => {
      const group = record(value)
      return {
        id: integer(group.id, 1),
        name: text(group.name),
        enabled: boolean(group.enabled),
        counts: {
          ...counts(group.counts),
          modelCooldown: integer(record(group.counts).model_cooldown),
        },
      }
    }),
    cooldown: list(row.cooldown_credentials).map(credential),
    isolated: list(row.blacklisted_credentials).map(credential),
    quotas: list(row.low_quota_credentials).map((value) => {
      const item = record(value)
      if (
        typeof item.remaining !== 'number' ||
        !Number.isFinite(item.remaining) ||
        item.remaining < 0 ||
        item.remaining > 1
      )
        throw new InvalidResponseError()
      return {
        id: integer(item.credential_id, 1),
        groupID: integer(item.group_id, 1),
        groupName: text(item.group_name),
        identity: identity(item.identity),
        remaining: item.remaining,
        resetAt: timestamp(item.reset_at_ms),
      }
    }),
    credits: list(row.expiring_reset_credits).map((value) => {
      const item = record(value)
      return {
        id: integer(item.credential_id, 1),
        groupID: integer(item.group_id, 1),
        groupName: text(item.group_name),
        identity: identity(item.identity),
        count: integer(item.count, 1),
        expiresAt: integer(item.nearest_expires_at_ms),
      }
    }),
    accessKeys: list(row.blocked_access_keys).map((value) => {
      const key = record(value)
      return {
        id: integer(key.access_key_id, 1),
        name: text(key.name),
        mask: text(key.masked_key),
        recoverable: boolean(key.recoverable),
        recoveryAt: timestamp(key.next_available_at_ms),
        rules: list(key.blocking_rules).map((value) => {
          const rule = record(value)
          return {
            id: integer(rule.id, 1),
            kind: oneOf(rule.kind, ['total', 'periodic']),
            limit: decimal(rule.limit_usd),
            used: decimal(rule.used_usd),
            remaining: decimal(rule.remaining_usd),
            period: integer(rule.period_seconds ?? 0),
            endsAt: timestamp(rule.window_ends_at_ms),
          }
        }),
      }
    }),
  }
}
