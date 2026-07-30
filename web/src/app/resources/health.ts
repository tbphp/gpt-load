import { queryOptions } from '@tanstack/vue-query'

import type { ApiClient } from '@/api/client'
import type {
  HealthGroupDto,
  HealthProblemKeyDto,
  HealthRecoveryDto,
  KeyCounts,
  RequestLogHealthDto,
  RuntimeHealthDto,
} from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  assertNoSecretLikeFields,
  projectArray,
  projectBoolean,
  projectEnum,
  projectISOInstant,
  projectRecord,
  projectSafeInteger,
  projectString,
} from './projector'

export type {
  HealthGroupDto,
  HealthProblemKeyDto,
  HealthRecoveryDto,
  KeyCounts,
  RequestLogHealthDto,
  RuntimeHealthDto,
} from '@/api/control/types'

const countFields = ['total', 'available', 'cooldown', 'blacklisted', 'disabled'] as const
const healthFields = [
  'observed_at',
  'version',
  'uptime_seconds',
  'snapshot_revision',
  'stats_window_seconds',
  'counts',
  'groups',
  'cooldown_keys',
  'blacklisted_keys',
  'request_log',
] as const
const problemKeyFields = [
  'key_id',
  'group_id',
  'group_name',
  'cooldown_until',
  'failure_count',
  'recent_success_count',
  'recent_failure_count',
  'consecutive_failure_count',
  'weight_manual',
  'weight_auto',
  'recovery',
  'mask',
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
  'retention_delete_failure_total',
  'queue_depth',
  'queue_capacity',
  'last_write_failure_at',
  'last_retention_failure_at',
] as const
const recoveryModes = ['cooldown_expiry', 'validation_probe'] as const
const problemFailureCategories = [
  'rate_limited',
  'model_unavailable',
  'invalid_key',
  'upstream_host_error',
  'client_error',
  'downstream_cancel',
  'ambiguous',
] as const
const longMaskPattern = /^.{4}\*{4}.{4}$/u

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectNonBlankString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0) invalidResponse()
  return result
}

function projectNullableInstant(value: unknown): string | null {
  return value === null ? null : projectISOInstant(value)
}

function projectHealthKeyMask(value: unknown): string {
  const mask = projectString(value)
  if (mask !== '****' && !longMaskPattern.test(mask)) invalidResponse()
  return mask
}

export function projectHealthCounts(value: unknown): KeyCounts {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, countFields)
  const result = {
    total: projectSafeInteger(record.total, { minimum: 0 }),
    available: projectSafeInteger(record.available, { minimum: 0 }),
    cooldown: projectSafeInteger(record.cooldown, { minimum: 0 }),
    blacklisted: projectSafeInteger(record.blacklisted, { minimum: 0 }),
    disabled: projectSafeInteger(record.disabled, { minimum: 0 }),
  }
  if (result.total !== result.available + result.cooldown + result.blacklisted + result.disabled) {
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
  assertNoSecretLikeFields(record, ['automatic', 'mode', 'at'])
  return {
    automatic: projectBoolean(record.automatic),
    mode: projectEnum(record.mode, recoveryModes),
    at: projectNullableInstant(record.at),
  }
}

function projectProblemKey(value: unknown): HealthProblemKeyDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, problemKeyFields)
  const recovery = projectRecovery(record.recovery)
  const cooldownUntil =
    record.cooldown_until === undefined ? undefined : projectISOInstant(record.cooldown_until)

  if (!recovery.automatic) invalidResponse()
  if (
    recovery.mode === 'cooldown_expiry' &&
    (cooldownUntil === undefined || recovery.at !== cooldownUntil)
  ) {
    invalidResponse()
  }
  if (recovery.mode === 'validation_probe' && (cooldownUntil != null || recovery.at !== null)) {
    invalidResponse()
  }

  return {
    key_id: projectSafeInteger(record.key_id, { minimum: 1 }),
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectNonBlankString(record.group_name),
    ...(cooldownUntil === undefined ? {} : { cooldown_until: cooldownUntil }),
    failure_count: projectSafeInteger(record.failure_count, { minimum: 0 }),
    recent_success_count: projectSafeInteger(record.recent_success_count, { minimum: 0 }),
    recent_failure_count: projectSafeInteger(record.recent_failure_count, { minimum: 0 }),
    consecutive_failure_count: projectSafeInteger(record.consecutive_failure_count, {
      minimum: 0,
    }),
    weight_manual:
      record.weight_manual === null
        ? null
        : projectSafeInteger(record.weight_manual, { minimum: 0, maximum: 100 }),
    weight_auto: projectSafeInteger(record.weight_auto, { minimum: 0, maximum: 100 }),
    recovery,
    mask: projectHealthKeyMask(record.mask),
    last_failure_category: projectEnum(record.last_failure_category, problemFailureCategories),
    last_status_code:
      record.last_status_code === null
        ? null
        : projectSafeInteger(record.last_status_code, { minimum: 100, maximum: 999 }),
  }
}

function projectRequestLogHealth(value: unknown): RequestLogHealthDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, requestLogFields)
  const result = {} as Record<(typeof requestLogFields)[number], number | string | null>
  for (const field of requestLogFields.slice(0, 12)) {
    result[field] = projectSafeInteger(record[field], { minimum: 0 })
  }
  result.last_write_failure_at = projectNullableInstant(record.last_write_failure_at)
  result.last_retention_failure_at = projectNullableInstant(record.last_retention_failure_at)
  return result as unknown as RequestLogHealthDto
}

export function projectRuntimeHealth(value: unknown): RuntimeHealthDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, healthFields)
  return {
    observed_at: projectISOInstant(record.observed_at),
    version: projectNonBlankString(record.version),
    uptime_seconds: projectSafeInteger(record.uptime_seconds, { minimum: 0 }),
    snapshot_revision: projectSafeInteger(record.snapshot_revision, { minimum: 1 }),
    stats_window_seconds: projectSafeInteger(record.stats_window_seconds, { minimum: 1 }),
    counts: projectHealthCounts(record.counts),
    groups: projectArray(record.groups, projectHealthGroup),
    cooldown_keys: projectArray(record.cooldown_keys, projectProblemKey),
    blacklisted_keys: projectArray(record.blacklisted_keys, projectProblemKey),
    request_log: projectRequestLogHealth(record.request_log),
  }
}

export async function getRuntimeHealth(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<RuntimeHealthDto> {
  return projectRuntimeHealth(await client.request('/api/health', { method: 'GET', signal }))
}

export function healthQueryOptions(client: ApiClient, intervalMs?: number) {
  return queryOptions({
    queryKey: controlQueryKeys.health(),
    queryFn: ({ signal }) => getRuntimeHealth(client, signal),
    ...(intervalMs !== undefined
      ? {
          refetchInterval: intervalMs,
          refetchIntervalInBackground: false,
          refetchOnWindowFocus: false,
        }
      : {}),
  })
}
