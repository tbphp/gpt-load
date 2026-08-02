import type { CreateAccessKeyRequest } from '@/app/resources/access-keys'

export interface PendingAccessKeyCreateOperation {
  idempotencyKey: string
  payload: CreateAccessKeyRequest
  state: 'indeterminate' | 'reconciling'
}

export function cloneAccessKeyCreatePayload(
  payload: CreateAccessKeyRequest,
): CreateAccessKeyRequest {
  return {
    name: payload.name,
    status: payload.status,
    filters: {
      groups: [...payload.filters.groups],
      protocols: [...payload.filters.protocols],
      models: [...payload.filters.models],
    },
    rpm_limit: payload.rpm_limit,
  }
}
