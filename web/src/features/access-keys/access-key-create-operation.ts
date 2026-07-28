import type { CreateAccessKeyRequest } from '@/api/control/access-keys'

export interface PendingAccessKeyCreateOperation {
  idempotencyKey: string
  payload: CreateAccessKeyRequest
  state: 'indeterminate' | 'reconciling'
}
