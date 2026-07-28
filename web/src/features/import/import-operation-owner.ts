import { inject, readonly, ref, watch, type InjectionKey } from 'vue'

import type {
  GroupCreateRequest,
  GroupCreateResult,
  GroupKeyImportResult,
} from '@/api/control/groups'
import { registerEphemeralStateCleaner } from '@/app/ephemeral-state'

import { useStableImportOperation } from './import-operation'

export function createImportOperationOwner() {
  const createGroup = useStableImportOperation<GroupCreateRequest, GroupCreateResult>()
  const importKeys = useStableImportOperation<
    { groupID: number; keys: string },
    GroupKeyImportResult
  >()
  const operationMode = ref<'new' | 'existing' | null>(null)

  const stopOperationWatch = watch(
    [createGroup.operation, importKeys.operation],
    ([createOperation, importOperation]) => {
      if (!createOperation && !importOperation) operationMode.value = null
    },
    { flush: 'sync' },
  )

  function beginCreate(payload: GroupCreateRequest) {
    if (importKeys.operation.value) return null
    const operation = createGroup.begin(payload)
    operationMode.value = 'new'
    return operation
  }

  function beginImportKeys(payload: { groupID: number; keys: string }, mode: 'new' | 'existing') {
    if (createGroup.operation.value) return null
    if (importKeys.operation.value && operationMode.value !== mode) return null
    const operation = importKeys.begin(payload)
    operationMode.value = mode
    return operation
  }

  function clear(): void {
    createGroup.reset()
    importKeys.reset()
    operationMode.value = null
  }

  function dispose(): void {
    clear()
    stopOperationWatch()
  }

  return {
    createGroup,
    importKeys,
    operationMode: readonly(operationMode),
    beginCreate,
    beginImportKeys,
    clear,
    dispose,
  }
}

export type ImportOperationOwner = ReturnType<typeof createImportOperationOwner>

export const importOperationOwnerKey: InjectionKey<ImportOperationOwner> =
  Symbol('import-operation-owner')

const defaultImportOperationOwner = createImportOperationOwner()
registerEphemeralStateCleaner(defaultImportOperationOwner.clear)

export function useImportOperationOwner(): ImportOperationOwner {
  return inject(importOperationOwnerKey, defaultImportOperationOwner)
}
