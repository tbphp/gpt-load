import type { ApiClient } from '@/api/client'

import type { AccessKeyDto, AccessKeyFiltersDto } from './types'

export interface CreateAccessKeyRequest {
  name: string
  filters: AccessKeyFiltersDto
  rpm_limit: number
}

export type UpdateAccessKeyRequest = Partial<{
  name: string
  status: AccessKeyDto['status']
  filters: AccessKeyFiltersDto
  rpm_limit: number
}>

export function listAccessKeys(client: ApiClient, signal?: AbortSignal): Promise<AccessKeyDto[]> {
  return client.request<AccessKeyDto[]>('/api/access-keys', { method: 'GET', signal })
}

export function createAccessKey(
  client: ApiClient,
  body: CreateAccessKeyRequest,
  signal?: AbortSignal,
): Promise<AccessKeyDto> {
  return client.request<AccessKeyDto>('/api/access-keys', {
    method: 'POST',
    json: body,
    signal,
  })
}

export function updateAccessKey(
  client: ApiClient,
  id: number,
  body: UpdateAccessKeyRequest,
  signal?: AbortSignal,
): Promise<AccessKeyDto> {
  return client.request<AccessKeyDto>(`/api/access-keys/${id}`, {
    method: 'PUT',
    json: body,
    signal,
  })
}

export function deleteAccessKey(
  client: ApiClient,
  id: number,
  signal?: AbortSignal,
): Promise<void> {
  return client.request<void>(`/api/access-keys/${id}`, { method: 'DELETE', signal })
}
