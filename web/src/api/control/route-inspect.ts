import type { ApiClient } from '@/api/client'

import type { AccessKeyDto, AccessProtocol } from './types'

export type RouteInspectReasonCode =
  | 'access_key_disabled'
  | 'protocol_filtered'
  | 'model_filtered'
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
  external_model: string
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
  upstream_model: string
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
  external_model: string
  access_key: {
    id: number
    name: string
    status: AccessKeyDto['status']
  }
  routable: boolean
  reason_code: RouteInspectReasonCode | null
  groups: RouteInspectGroupDto[]
}

export function inspectRoute(
  client: ApiClient,
  body: RouteInspectRequest,
  signal?: AbortSignal,
): Promise<RouteInspectResponseDto> {
  return client.request<RouteInspectResponseDto>('/api/route/inspect', {
    method: 'POST',
    json: body,
    signal,
  })
}
