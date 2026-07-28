import type { CreateAccessKeyRequest } from '@/app/resources/access-keys'

export interface PendingAccessKeyCreateOperation {
  idempotencyKey: string
  payload: CreateAccessKeyRequest
  state: 'indeterminate' | 'reconciling'
}
