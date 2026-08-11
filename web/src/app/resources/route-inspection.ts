import type { ApiClient } from '@/api/client'
import { enabledDataProtocols } from '@/api/control/protocols'
import type { AccessKeyDto, AccessProtocol } from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'

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

export type RouteInspectReasonCode =
  | 'access_key_disabled'
  | 'protocol_filtered'
  | 'model_filtered'
  | 'model_required_by_filter'
  | 'operation_unsupported'
  | 'native_route_required'
  | 'no_route_target'
  | 'group_disabled'
  | 'group_filtered'
  | 'no_available_group'
  | 'no_credentials'
  | 'group_weight_zero'
  | 'credential_disabled'
  | 'credential_blacklisted'
  | 'credential_cooldown'
  | 'credential_weight_zero'
  | 'credential_not_allowed'
  | 'no_available_credential'

export interface RouteInspectRequest {
  protocol: AccessProtocol
  operation: RouteInspectOperation
  route_requirement: RouteInspectRequirement
  external_model?: string | null
  access_key_id: number
}

export type RouteInspectOperation =
  | 'chat_completion'
  | 'responses_create'
  | 'responses_retrieve'
  | 'responses_delete'
  | 'responses_cancel'
  | 'responses_input_items'
  | 'responses_compact'
  | 'responses_input_tokens'
  | 'responses_passthrough'
export type RouteInspectRequirement = 'any' | 'native'
export type RouteInspectMode = 'native' | 'converted'

export interface RouteInspectCredentialDto {
  credential_id: number
  available: boolean
  reason_code: RouteInspectReasonCode | null
  weight_manual: number | null
  weight_auto: number
  effective_weight: number
  cooldown_until_ms: number | null
}

export interface RouteInspectGroupDto {
  group_id: number
  group_name: string
  channel_id: string
  route_mode: RouteInspectMode
  route_requirement_satisfied: boolean
  upstream_model: string | null
  weight_manual: number | null
  included: boolean
  routable: boolean
  reason_code: RouteInspectReasonCode | null
  credentials: RouteInspectCredentialDto[]
}

export interface RouteInspectResponseDto {
  observed_at_ms: number
  snapshot_revision: number
  protocol: AccessProtocol
  operation: RouteInspectOperation
  route_requirement: RouteInspectRequirement
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
export const routeInspectOperations = [
  'chat_completion',
  'responses_create',
  'responses_retrieve',
  'responses_delete',
  'responses_cancel',
  'responses_input_items',
  'responses_compact',
  'responses_input_tokens',
  'responses_passthrough',
] as const
export const routeInspectRequirements = ['any', 'native'] as const
const routeModes = ['native', 'converted'] as const
const reasonCodes = [
  'access_key_disabled',
  'protocol_filtered',
  'model_filtered',
  'model_required_by_filter',
  'operation_unsupported',
  'native_route_required',
  'no_route_target',
  'group_disabled',
  'group_filtered',
  'no_available_group',
  'no_credentials',
  'group_weight_zero',
  'credential_disabled',
  'credential_blacklisted',
  'credential_cooldown',
  'credential_weight_zero',
  'credential_not_allowed',
  'no_available_credential',
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

function projectRouteCredential(value: unknown): RouteInspectCredentialDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'credential_id',
    'available',
    'reason_code',
    'weight_manual',
    'weight_auto',
    'effective_weight',
    'cooldown_until_ms',
  ])
  return {
    credential_id: projectSafeInteger(record.credential_id, { minimum: 1 }),
    available: projectBoolean(record.available),
    reason_code: projectReason(record.reason_code),
    weight_manual: projectNullableWeight(record.weight_manual),
    weight_auto: projectSafeInteger(record.weight_auto, { minimum: 0, maximum: 100 }),
    effective_weight: projectSafeInteger(record.effective_weight, { minimum: 0 }),
    cooldown_until_ms: projectNullableEpochMilliseconds(record.cooldown_until_ms),
  }
}

function projectRouteGroup(value: unknown): RouteInspectGroupDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, [
    'group_id',
    'group_name',
    'channel_id',
    'route_mode',
    'route_requirement_satisfied',
    'upstream_model',
    'weight_manual',
    'included',
    'routable',
    'reason_code',
    'credentials',
  ])
  return {
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    group_name: projectNonBlankString(record.group_name),
    channel_id: projectNonBlankString(record.channel_id),
    route_mode: projectEnum(record.route_mode, routeModes),
    route_requirement_satisfied: projectBoolean(record.route_requirement_satisfied),
    upstream_model: projectNullableNonBlankString(record.upstream_model),
    weight_manual: projectNullableWeight(record.weight_manual),
    included: projectBoolean(record.included),
    routable: projectBoolean(record.routable),
    reason_code: projectReason(record.reason_code),
    credentials: projectArray(record.credentials, projectRouteCredential),
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
    'observed_at_ms',
    'snapshot_revision',
    'protocol',
    'operation',
    'route_requirement',
    'external_model',
    'access_key',
    'routable',
    'reason_code',
    'groups',
  ])
  return {
    observed_at_ms: projectEpochMilliseconds(record.observed_at_ms),
    snapshot_revision: projectSafeInteger(record.snapshot_revision, { minimum: 1 }),
    protocol: projectEnum(record.protocol, enabledDataProtocols),
    operation: projectEnum(record.operation, routeInspectOperations),
    route_requirement: projectEnum(record.route_requirement, routeInspectRequirements),
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
