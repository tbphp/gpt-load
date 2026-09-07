import { queryOptions } from '@tanstack/vue-query'
import type { MaybeRefOrGetter } from 'vue'

import type { ApiClient } from '@/api/client'
import type {
  HealthGroupDto,
  HealthProblemCredentialDto,
  HealthQuotaCredentialDto,
  HealthExpiringResetCreditDto,
  HealthRecoveryDto,
  HealthCredentialCountsDto,
  HealthAccessKeyCostLimitDto,
  RequestLogHealthDto,
  RuntimeHealthDto,
} from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { projectAccessKeyCostLimitRuleStatus } from './access-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEpochMilliseconds,
  projectEnum,
  projectNullableEpochMilliseconds,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type {
  HealthGroupDto,
  HealthProblemCredentialDto,
  HealthQuotaCredentialDto,
  HealthExpiringResetCreditDto,
  HealthRecoveryDto,
  HealthCredentialCountsDto,
  RequestLogHealthDto,
  RuntimeHealthDto,
} from '@/api/control/types'

const countFields = ['credentials', 'available', 'cooldown', 'blacklisted'] as const
const healthFields = [
  'observed_at_ms',
  'version',
  'uptime_seconds',
  'snapshot_revision',
  'stats_window_seconds',
  'counts',
  'groups',
  'cooldown_credentials',
  'blacklisted_credentials',
  'low_quota_credentials',
  'expiring_reset_credits',
  'blocked_access_keys',
  'request_log',
] as const
const blockedAccessKeyFields = [
  'access_key_id',
  'name',
  'masked_key',
  'recoverable',
  'next_available_at_ms',
  'blocking_rules',
] as const
const quotaCredentialFields = [
  'credential_id',
  'group_id',
  'group_name',
  'remaining',
  'reset_at_ms',
] as const
const expiringResetCreditFields = [
  'credential_id',
  'group_id',
  'group_name',
  'count',
  'nearest_expires_at_ms',
] as const
const problemCredentialFields = [
  'credential_id',
  'group_id',
  'group_name',
  'cooldown_until_ms',
  'failure_count',
  'recent_success_count',
  'recent_problem_count',
  'consecutive_problem_count',
  'weight_manual',
  'weight_auto',
  'recovery',
  'identity',
  'last_failure_category',
  'last_status_code',
] as const
const requestLogFields = [
  'enqueued_total',
  'persisted_total',
  'dropped_not_running_total',
  'dropped_queue_full_total',
  'dropped_stopping_total',
  'dropped_persist_failed_total',
  'dropped_shutdown_total',
  'dropped_total',
  'write_failure_total',
  'access_quota_checkpoint_write_failure_total',
  'access_quota_checkpoint_degraded',
  'retention_delete_failure_total',
  'queue_depth',
  'queue_capacity',
  'last_write_failure_at_ms',
  'last_access_quota_checkpoint_write_failure_at_ms',
  'last_retention_failure_at_ms',
] as const
const requestLogCounterFields = [
  'enqueued_total',
  'persisted_total',
  'dropped_not_running_total',
  'dropped_queue_full_total',
  'dropped_stopping_total',
  'dropped_persist_failed_total',
  'dropped_shutdown_total',
  'dropped_total',
  'write_failure_total',
  'access_quota_checkpoint_write_failure_total',
  'retention_delete_failure_total',
  'queue_depth',
  'queue_capacity',
] as const
const recoveryModes = ['cooldown_expiry', 'validation_probe', 'configuration_required'] as const
const problemFailureCategories = [
  'rate_limited',
  'model_unavailable',
  'invalid_key',
  'upstream_host_error',
  'client_error',
  'downstream_cancel',
  'authentication_required',
  'ambiguous',
] as const
function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectNonBlankString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0) invalidResponse()
  return result
}

export function projectHealthCounts(value: unknown): HealthCredentialCountsDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, countFields)
  const result = {
    credentials: projectSafeInteger(record.credentials, { minimum: 0 }),
    available: projectSafeInteger(record.available, { minimum: 0 }),
    cooldown: projectSafeInteger(record.cooldown, { minimum: 0 }),
    blacklisted: projectSafeInteger(record.blacklisted, { minimum: 0 }),
  }
  if (result.credentials !== result.available + result.cooldown + result.blacklisted) {
    invalidResponse()
  }
  return result
}

function projectHealthGroup(value: unknown): HealthGroupDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['id', 'name', 'enabled', 'counts'])
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankString(record.name),
    enabled: projectBoolean(record.enabled),
    counts: projectHealthCounts(record.counts),
  }
}

function projectRecovery(value: unknown): HealthRecoveryDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['automatic', 'mode', 'at_ms'])
  return {
    automatic: projectBoolean(record.automatic),
    mode: projectEnum(record.mode, recoveryModes),
    at_ms: projectNullableEpochMilliseconds(record.at_ms),
  }
}

function projectProblemCredential(value: unknown): HealthProblemCredentialDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, problemCredentialFields)
  const recovery = projectRecovery(record.recovery)
  const cooldownUntilMS = projectNullableEpochMilliseconds(record.cooldown_until_ms)

  if (recovery.mode === 'cooldown_expiry') {
    if (!recovery.automatic || recovery.at_ms !== cooldownUntilMS) invalidResponse()
  } else if (recovery.mode === 'validation_probe') {
    if (!recovery.automatic || cooldownUntilMS !== null || recovery.at_ms !== null) {
      invalidResponse()
    }
  } else if (recovery.automatic || cooldownUntilMS !== null || recovery.at_ms !== null) {
    invalidResponse()
  }

  return {
    credential_id: projectSafeInteger(record.credential_id, { minimum: 1 }),
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectNonBlankString(record.group_name),
    cooldown_until_ms: cooldownUntilMS,
    failure_count: projectSafeInteger(record.failure_count, { minimum: 0 }),
    recent_success_count: projectSafeInteger(record.recent_success_count, { minimum: 0 }),
    recent_problem_count: projectSafeInteger(record.recent_problem_count, { minimum: 0 }),
    consecutive_problem_count: projectSafeInteger(record.consecutive_problem_count, {
      minimum: 0,
    }),
    weight_manual:
      record.weight_manual === null
        ? null
        : projectSafeInteger(record.weight_manual, { minimum: 0, maximum: 100 }),
    weight_auto: projectSafeInteger(record.weight_auto, { minimum: 0, maximum: 100 }),
    recovery,
    identity: projectString(record.identity),
    last_failure_category: projectEnum(record.last_failure_category, problemFailureCategories),
    last_status_code:
      record.last_status_code === null
        ? null
        : projectSafeInteger(record.last_status_code, { minimum: 100, maximum: 999 }),
  }
}

function projectQuotaCredential(value: unknown): HealthQuotaCredentialDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, quotaCredentialFields)
  const remaining = record.remaining
  if (
    typeof remaining !== 'number' ||
    !Number.isFinite(remaining) ||
    remaining < 0 ||
    remaining > 1
  ) {
    invalidResponse()
  }
  return {
    credential_id: projectSafeInteger(record.credential_id, { minimum: 1 }),
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectNonBlankString(record.group_name),
    remaining,
    reset_at_ms: projectEpochMilliseconds(record.reset_at_ms),
  }
}

function projectExpiringResetCredit(value: unknown): HealthExpiringResetCreditDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, expiringResetCreditFields)
  return {
    credential_id: projectSafeInteger(record.credential_id, { minimum: 1 }),
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectNonBlankString(record.group_name),
    count: projectSafeInteger(record.count, { minimum: 1 }),
    nearest_expires_at_ms: projectEpochMilliseconds(record.nearest_expires_at_ms),
  }
}

function projectBlockedAccessKey(value: unknown): HealthAccessKeyCostLimitDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, blockedAccessKeyFields)
  const recoverable = projectBoolean(record.recoverable)
  const nextAvailable = projectNullableEpochMilliseconds(record.next_available_at_ms)
  const blockingRules = projectArray(record.blocking_rules, projectAccessKeyCostLimitRuleStatus)
  if (
    blockingRules.length === 0 ||
    blockingRules.some((rule) => rule.status !== 'exhausted') ||
    (recoverable &&
      (nextAvailable === null || blockingRules.some((rule) => rule.kind !== 'periodic'))) ||
    (!recoverable &&
      (nextAvailable !== null || !blockingRules.some((rule) => rule.kind === 'total')))
  ) {
    invalidResponse()
  }
  return {
    access_key_id: projectSafeInteger(record.access_key_id, { minimum: 1 }),
    name: projectNonBlankString(record.name),
    masked_key: projectString(record.masked_key),
    recoverable,
    next_available_at_ms: nextAvailable,
    blocking_rules: blockingRules,
  }
}

function projectRequestLogHealth(value: unknown): RequestLogHealthDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, requestLogFields)
  const counters = Object.fromEntries(
    requestLogCounterFields.map((field) => [
      field,
      projectSafeInteger(record[field], { minimum: 0 }),
    ]),
  ) as Pick<
    RequestLogHealthDto,
    Exclude<
      keyof RequestLogHealthDto,
      | 'last_write_failure_at_ms'
      | 'last_access_quota_checkpoint_write_failure_at_ms'
      | 'last_retention_failure_at_ms'
      | 'access_quota_checkpoint_degraded'
    >
  >
  return {
    ...counters,
    access_quota_checkpoint_degraded: projectBoolean(record.access_quota_checkpoint_degraded),
    last_write_failure_at_ms: projectNullableEpochMilliseconds(record.last_write_failure_at_ms),
    last_access_quota_checkpoint_write_failure_at_ms: projectNullableEpochMilliseconds(
      record.last_access_quota_checkpoint_write_failure_at_ms,
    ),
    last_retention_failure_at_ms: projectNullableEpochMilliseconds(
      record.last_retention_failure_at_ms,
    ),
  }
}

export function projectRuntimeHealth(value: unknown): RuntimeHealthDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, healthFields)
  return {
    observed_at_ms: projectEpochMilliseconds(record.observed_at_ms),
    version: projectNonBlankString(record.version),
    uptime_seconds: projectSafeInteger(record.uptime_seconds, { minimum: 0 }),
    snapshot_revision: projectSafeInteger(record.snapshot_revision, { minimum: 1 }),
    stats_window_seconds: projectSafeInteger(record.stats_window_seconds, { minimum: 1 }),
    counts: projectHealthCounts(record.counts),
    groups: projectArray(record.groups, projectHealthGroup),
    cooldown_credentials: projectArray(record.cooldown_credentials, projectProblemCredential),
    blacklisted_credentials: projectArray(record.blacklisted_credentials, projectProblemCredential),
    low_quota_credentials: projectArray(record.low_quota_credentials, projectQuotaCredential),
    expiring_reset_credits: projectArray(record.expiring_reset_credits, projectExpiringResetCredit),
    blocked_access_keys: projectArray(record.blocked_access_keys, projectBlockedAccessKey),
    request_log: projectRequestLogHealth(record.request_log),
  }
}

export async function getRuntimeHealth(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<RuntimeHealthDto> {
  return projectRuntimeHealth(await client.request('/api/health', { method: 'GET', signal }))
}

export function healthQueryOptions(
  client: ApiClient,
  intervalMs?: number,
  enabled?: MaybeRefOrGetter<boolean>,
) {
  return queryOptions({
    queryKey: controlQueryKeys.health(),
    queryFn: ({ signal }) => getRuntimeHealth(client, signal),
    refetchOnWindowFocus: false,
    ...(intervalMs !== undefined
      ? {
          refetchInterval: intervalMs,
          refetchIntervalInBackground: false,
        }
      : {}),
    // /api/health 不在 AccessKey 白名单里，调用方需要能按身份关掉这个查询。
    ...(enabled !== undefined ? { enabled } : {}),
  })
}
