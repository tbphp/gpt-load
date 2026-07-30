import type { ApiClient } from '@/api/client'
import { enabledDataProtocols } from '@/api/control/protocols'
import type { AccessKeyDto, AccessProtocol } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'

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

export type RouteInspectReasonCode =
  | 'access_key_disabled'
  | 'protocol_filtered'
  | 'model_filtered'
  | 'model_required_by_filter'
  | 'no_route_target'
  | 'group_disabled'
  | 'group_filtered'
  | 'no_available_group'
  | 'no_keys'
  | 'group_weight_zero'
  | 'key_disabled'
  | 'key_blacklisted'
  | 'key_cooldown'
  | 'key_weight_zero'
  | 'no_available_key'

export interface RouteInspectRequest {
  protocol: AccessProtocol
  external_model?: string | null
  access_key_id: number
}

export interface RouteInspectKeyDto {
  key_id: number
  available: boolean
  reason_code: RouteInspectReasonCode | null
  weight_manual: number | null
  weight_auto: number
  effective_weight: number
  cooldown_until: string | null
}

export interface RouteInspectGroupDto {
  group_id: number
  group_name: string
  upstream_model: string | null
  weight_manual: number | null
  included: boolean
  routable: boolean
  reason_code: RouteInspectReasonCode | null
  keys: RouteInspectKeyDto[]
}

export interface RouteInspectResponseDto {
  observed_at: string
  snapshot_revision: number
  protocol: AccessProtocol
  external_model: string | null
  access_key: {
    id: number
    name: string
    status: AccessKeyDto['status']
  }
  routable: boolean
  reason_code: RouteInspectReasonCode | null
  groups: RouteInspectGroupDto[]
}

const accessKeyStatuses = ['active', 'disabled'] as const
const reasonCodes = [
  'access_key_disabled',
  'protocol_filtered',
  'model_filtered',
  'model_required_by_filter',
  'no_route_target',
  'group_disabled',
  'group_filtered',
  'no_available_group',
  'no_keys',
  'group_weight_zero',
  'key_disabled',
  'key_blacklisted',
  'key_cooldown',
  'key_weight_zero',
  'no_available_key',
] as const

function invalidResponse(): never {
  throw new InvalidResponseError()
}

function projectNonBlankString(value: unknown): string {
  const result = projectString(value)
  if (result.trim().length === 0 || result !== result.trim()) invalidResponse()
  return result
}

function projectNullableNonBlankString(value: unknown): string | null {
  return value === null ? null : projectNonBlankString(value)
}

function projectReason(value: unknown): RouteInspectReasonCode | null {
  return value === null ? null : projectEnum(value, reasonCodes)
}

function projectNullableWeight(value: unknown): number | null {
  return value === null ? null : projectSafeInteger(value, { minimum: 0, maximum: 100 })
}

function projectRouteKey(value: unknown): RouteInspectKeyDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'key_id',
    'available',
    'reason_code',
    'weight_manual',
    'weight_auto',
    'effective_weight',
    'cooldown_until',
  ])
  return {
    key_id: projectSafeInteger(record.key_id, { minimum: 1 }),
    available: projectBoolean(record.available),
    reason_code: projectReason(record.reason_code),
    weight_manual: projectNullableWeight(record.weight_manual),
    weight_auto: projectSafeInteger(record.weight_auto, { minimum: 0, maximum: 100 }),
    effective_weight: projectSafeInteger(record.effective_weight, { minimum: 0 }),
    cooldown_until:
      record.cooldown_until === null ? null : projectISOInstant(record.cooldown_until),
  }
}

function projectRouteGroup(value: unknown): RouteInspectGroupDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'group_id',
    'group_name',
    'upstream_model',
    'weight_manual',
    'included',
    'routable',
    'reason_code',
    'keys',
  ])
  return {
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectNonBlankString(record.group_name),
    upstream_model: projectNullableNonBlankString(record.upstream_model),
    weight_manual: projectNullableWeight(record.weight_manual),
    included: projectBoolean(record.included),
    routable: projectBoolean(record.routable),
    reason_code: projectReason(record.reason_code),
    keys: projectArray(record.keys, projectRouteKey),
  }
}

function projectAccessKey(value: unknown): RouteInspectResponseDto['access_key'] {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, ['id', 'name', 'status'])
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    name: projectNonBlankString(record.name),
    status: projectEnum(record.status, accessKeyStatuses),
  }
}

export function projectRouteInspection(value: unknown): RouteInspectResponseDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'observed_at',
    'snapshot_revision',
    'protocol',
    'external_model',
    'access_key',
    'routable',
    'reason_code',
    'groups',
  ])
  return {
    observed_at: projectISOInstant(record.observed_at),
    snapshot_revision: projectSafeInteger(record.snapshot_revision, { minimum: 1 }),
    protocol: projectEnum(record.protocol, enabledDataProtocols),
    external_model: projectNullableNonBlankString(record.external_model),
    access_key: projectAccessKey(record.access_key),
    routable: projectBoolean(record.routable),
    reason_code: projectReason(record.reason_code),
    groups: projectArray(record.groups, projectRouteGroup),
  }
}

export async function inspectRoute(
  client: ApiClient,
  body: RouteInspectRequest,
  signal?: AbortSignal,
): Promise<RouteInspectResponseDto> {
  return projectRouteInspection(
    await client.request('/api/route/inspect', {
      method: 'POST',
      json: body,
      signal,
    }),
  )
}
