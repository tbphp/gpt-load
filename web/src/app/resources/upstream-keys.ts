import type { ApiClient } from '@/api/client'
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

const upstreamKeyFields = [
  'id',
  'group_id',
  'mask',
  'status',
  'effective_status',
  'weight_manual',
  'weight_auto',
  'blacklisted',
  'cooldown_until',
  'failure_count',
] as const
const upstreamKeyStatuses = ['active', 'disabled'] as const
const effectiveStatuses = ['available', 'cooldown', 'blacklisted', 'disabled'] as const
const canonicalMask = /^(?:\*{4}|.{4}\*{4}.{4})$/u

function projectMask(value: unknown): string {
  const mask = projectString(value)
  if (!canonicalMask.test(mask)) throw new InvalidResponseError()
  return mask
}

export function projectUpstreamKey(value: unknown): UpstreamKeyDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, upstreamKeyFields)
  return {
    id: projectSafeInteger(record.id, { minimum: 1 }),
    group_id: projectSafeInteger(record.group_id, { minimum: 1 }),
    mask: projectMask(record.mask),
    status: projectEnum(record.status, upstreamKeyStatuses),
    effective_status: projectEnum(record.effective_status, effectiveStatuses),
    weight_manual:
      record.weight_manual === null
        ? null
        : projectSafeInteger(record.weight_manual, { minimum: 0, maximum: 100 }),
    weight_auto: projectSafeInteger(record.weight_auto, { minimum: 0, maximum: 100 }),
    blacklisted: projectBoolean(record.blacklisted),
    cooldown_until:
      record.cooldown_until === null ? null : projectISOInstant(record.cooldown_until),
    failure_count: projectSafeInteger(record.failure_count, { minimum: 0 }),
  }
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

export async function listGroupKeys(
  client: ApiClient,
  groupId: number,
  signal?: AbortSignal,
): Promise<UpstreamKeyDto[]> {
  return projectArray(
    await client.request(`/api/groups/${groupId}/keys`, {
      method: 'GET',
      signal,
    }),
    projectUpstreamKey,
  )
}

export async function updateGroupKey(
  client: ApiClient,
  groupId: number,
  keyId: number,
  patch: UpstreamKeyPatch,
  signal?: AbortSignal,
): Promise<UpstreamKeyDto> {
  const body = normalizePatch(patch)
  return projectUpstreamKey(
    await client.request(`/api/groups/${groupId}/keys/${keyId}`, {
      method: 'PUT',
      json: body,
      signal,
    }),
  )
}

export async function deleteGroupKey(
  client: ApiClient,
  groupId: number,
  keyId: number,
  signal?: AbortSignal,
): Promise<void> {
  await client.request(`/api/groups/${groupId}/keys/${keyId}`, {
    method: 'DELETE',
    signal,
  })
}

export const upstreamKeyMutationInvalidations = {
  update: (groupID: number) => [
    controlQueryKeys.groups.keys(groupID),
    controlQueryKeys.groups.detail(groupID),
    controlQueryKeys.groups.list(),
    controlQueryKeys.health(),
  ],
  delete: (groupID: number) => [
    controlQueryKeys.groups.keys(groupID),
    controlQueryKeys.groups.detail(groupID),
    controlQueryKeys.groups.list(),
    controlQueryKeys.health(),
  ],
} as const
