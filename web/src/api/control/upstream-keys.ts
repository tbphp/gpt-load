import type { ApiClient } from '@/api/client'

export type UpstreamKeyStatus = 'active' | 'disabled'
export type UpstreamKeyEffectiveStatus = 'available' | 'cooldown' | 'blacklisted' | 'disabled'

export interface UpstreamKeyDto {
  id: number
  group_id: number
  mask: string
  status: UpstreamKeyStatus
  effective_status: UpstreamKeyEffectiveStatus
  weight_manual: number | null
  weight_auto: number
  blacklisted: boolean
  cooldown_until: string | null
  failure_count: number
}

export interface UpstreamKeyPatch {
  status?: UpstreamKeyStatus
  weight_manual?: number | null
}

function normalizePatch(patch: UpstreamKeyPatch): UpstreamKeyPatch {
  const keys = Object.keys(patch)
  if (keys.length === 0 || keys.some((key) => key !== 'status' && key !== 'weight_manual')) {
    throw new Error('INVALID_UPSTREAM_KEY_PATCH')
  }

  const body: UpstreamKeyPatch = {}
  if (Object.prototype.hasOwnProperty.call(patch, 'status')) {
    if (patch.status !== 'active' && patch.status !== 'disabled') {
      throw new Error('INVALID_UPSTREAM_KEY_STATUS')
    }
    body.status = patch.status
  }
  if (Object.prototype.hasOwnProperty.call(patch, 'weight_manual')) {
    const weight = patch.weight_manual
    if (
      weight === undefined ||
      (weight !== null && (!Number.isInteger(weight) || weight < 1 || weight > 100))
    ) {
      throw new Error('INVALID_UPSTREAM_KEY_WEIGHT')
    }
    body.weight_manual = weight
  }
  return body
}

export function listGroupKeys(
  client: ApiClient,
  groupId: number,
  signal?: AbortSignal,
): Promise<UpstreamKeyDto[]> {
  return client.request<UpstreamKeyDto[]>(`/api/groups/${groupId}/keys`, {
    method: 'GET',
    signal,
  })
}

export async function updateGroupKey(
  client: ApiClient,
  groupId: number,
  keyId: number,
  patch: UpstreamKeyPatch,
  signal?: AbortSignal,
): Promise<UpstreamKeyDto> {
  const body = normalizePatch(patch)
  return await client.request<UpstreamKeyDto>(`/api/groups/${groupId}/keys/${keyId}`, {
    method: 'PUT',
    json: body,
    signal,
  })
}

export function deleteGroupKey(
  client: ApiClient,
  groupId: number,
  keyId: number,
  signal?: AbortSignal,
): Promise<void> {
  return client.request<void>(`/api/groups/${groupId}/keys/${keyId}`, {
    method: 'DELETE',
    signal,
  })
}
