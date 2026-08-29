import type { AccessKeyDto } from '@/api/control/types'

export interface PendingAccessKeyRotateOperation {
  base: AccessKeyDto
  idempotencyKey: string
  state: 'indeterminate' | 'reconciling'
}
