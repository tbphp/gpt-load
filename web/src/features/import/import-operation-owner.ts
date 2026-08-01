import { inject, readonly, ref, watch, type InjectionKey } from 'vue'

import type {
  GroupCreateRequest,
  GroupCreateResult,
  GroupKeyImportResult,
} from '@/app/resources/groups'
import { registerEphemeralStateCleaner } from '@/app/ephemeral-state'

import { useStableImportOperation } from './import-operation'
import type { ImportRecoveryDraft, ImportDraft } from './model-draft'

export interface CreateGroupImportOperationPayload {
  request: GroupCreateRequest
  draft: ImportDraft
}

export interface ImportKeysOperationPayload {
  groupID: number
  keys: string
  draft: ImportRecoveryDraft
}

export function createImportOperationOwner() {
  const createGroup = useStableImportOperation<
    CreateGroupImportOperationPayload,
    GroupCreateResult
  >()
  const importKeys = useStableImportOperation<ImportKeysOperationPayload, GroupKeyImportResult>()
  const operationMode = ref<'new' | 'existing' | null>(null)

  const stopOperationWatch = watch(
    [createGroup.operation, importKeys.operation],
    ([createOperation, importOperation]) => {
      if (!createOperation && !importOperation) operationMode.value = null
    },
    { flush: 'sync' },
  )

  function beginCreate(request: GroupCreateRequest, draft: ImportDraft) {
    if (importKeys.operation.value) return null
    const operation = createGroup.begin({ request, draft })
    operationMode.value = 'new'
    return operation
  }

  function beginImportKeys(
    payload: { groupID: number; keys: string },
    mode: 'new' | 'existing',
    draft: ImportRecoveryDraft,
  ) {
    if (createGroup.operation.value) return null
    if (importKeys.operation.value && operationMode.value !== mode) return null
    const operation = importKeys.begin({ ...payload, draft })
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
