import type { UpdateAccessKeyRequest } from '@/api/control/access-keys'
import type { AccessKeyDto } from '@/api/control/types'

export interface PendingAccessKeyEditOperation {
  base: AccessKeyDto
  patch: UpdateAccessKeyRequest
  state: 'indeterminate' | 'reconciling'
}
