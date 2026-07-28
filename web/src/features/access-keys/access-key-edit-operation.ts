import type { UpdateAccessKeyRequest } from '@/app/resources/access-keys'
import type { AccessKeyDto } from '@/api/control/types'

export interface PendingAccessKeyEditOperation {
  base: AccessKeyDto
  patch: UpdateAccessKeyRequest
  state: 'indeterminate' | 'reconciling'
}
