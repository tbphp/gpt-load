<script setup lang="ts">
import { Save } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'

import { useApiClient } from '@/api/client-context'
import {
  createAccessKey,
  updateAccessKey,
  type CreateAccessKeyRequest,
} from '@/app/resources/access-keys'
import type { AccessKeyDto, AccessProtocol, GroupOptionDto } from '@/api/control/types'
import type { ChannelDto } from '@/app/resources/channels'
import { ApiError, RequestCancelledError } from '@/api/errors'
import { classifyMutationOutcome } from '@/app/mutation-outcome'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import type { SearchableMultiSelectOption } from '@/components/ui/SearchableMultiSelect.vue'
import { createUUID } from '@/lib/uuid'
import { isValidPriceMultiplier } from '@/lib/price-multiplier'

import {
  accessKeyProtocolOptions,
  buildAccessKeyModelOptions,
  buildAccessKeyProtocolCandidates,
} from './access-key-options'
import {
  cloneAccessKeyCreatePayload,
  type PendingAccessKeyCreateOperation,
} from './access-key-create-operation'
import {
  findAccessKeyForReconciliation,
  type PendingAccessKeyEditOperation,
} from './access-key-edit-operation'
import AccessKeyDeleteDialog from './AccessKeyDeleteDialog.vue'
import AccessKeyCostLimitEditor from './AccessKeyCostLimitEditor.vue'
import AccessKeyFormFields from './AccessKeyFormFields.vue'
import AccessKeyCredentialField from './AccessKeyCredentialField.vue'
import { estimateAccessKeyStrength, isValidCustomAccessKey } from './access-key-strength'
import AccessKeyOperationFeedback from './AccessKeyOperationFeedback.vue'
import AccessKeyPolicyFields from './AccessKeyPolicyFields.vue'
import AccessKeyScopeEditor from './AccessKeyScopeEditor.vue'
import {
  materializeAccessKeyFilters,
  validateAccessKeyScope,
  type AccessKeyScopeDimension,
  type AccessKeyScopeMode,
  type GroupCatalogState,
} from './access-key-scope'
import {
  areAccessKeyCostLimitRulesValid,
  accessKeyMatchesUpdatePatch,
  buildAccessKeyUpdatePatch,
  buildCreateAccessKeyInput,
  createAccessKeyDraft,
  createAccessKeyDraftFromCreateInput,
  createAccessKeyDraftFromUpdate,
  isAccessKeyDraftDirty,
  isAccessKeyDraftValid,
  type AccessKeyDraft,
} from './access-key-patch'

const props = withDefaults(
  defineProps<{
    open: boolean
    accessKey: AccessKeyDto | null
    copyFrom?: AccessKeyDto | null
    groups: GroupOptionDto[]
    channels: ChannelDto[]
    total: number
    groupCatalogState?: GroupCatalogState
    createOperation?: PendingAccessKeyCreateOperation | null
    editOperation?: PendingAccessKeyEditOperation | null
  }>(),
  {
    copyFrom: null,
    createOperation: null,
    editOperation: null,
    groupCatalogState: 'ready',
  },
)
const emit = defineEmits<{
  'update:open': [open: boolean]
  'update:createOperation': [operation: PendingAccessKeyCreateOperation | null]
  'update:editOperation': [operation: PendingAccessKeyEditOperation | null]
  saved: [kind: 'created' | 'updated', name: string, accessKey?: AccessKeyDto]
  deleted: [name: string]
}>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const formFields = ref<InstanceType<typeof AccessKeyFormFields>>()
const base = ref<AccessKeyDto | null>(null)
const draft = ref<AccessKeyDraft>(createAccessKeyDraft())
const operationID = ref('')
const createPayload = ref<CreateAccessKeyRequest | null>(null)
const credentialField = ref<InstanceType<typeof AccessKeyCredentialField>>()
const keyConfirmationOpen = ref(false)
const customKeyError = ref('')
const createOperationRetained = ref(false)
const editOperationRetained = ref(false)
const pending = ref(false)
const failed = ref(false)
const mutationState = ref<'idle' | 'indeterminate' | 'reconciling'>('idle')
const editReconciliation = ref<PendingAccessKeyEditOperation | null>(null)
const editNotApplied = ref(false)
const modelInput = ref('')
let controller: AbortController | undefined

const editing = computed(() => base.value !== null)
const keyConfirmationDescription = computed(() => {
  if (!editing.value) return t('accessKeys.customKey.warningDescription')
  const impact = t('accessKeys.customKey.replaceDescription')
  return estimateAccessKeyStrength(draft.value.key) === 'weak'
    ? `${impact} ${t('accessKeys.customKey.replaceWeakWarning')}`
    : impact
})
const keyConfirmationLabel = computed(() =>
  t(
    editing.value
      ? estimateAccessKeyStrength(draft.value.key) === 'weak'
        ? 'accessKeys.customKey.saveAnyway'
        : 'accessKeys.drawer.saveChanges'
      : 'accessKeys.customKey.createAnyway',
  ),
)
const createOperationActive = computed(
  () =>
    !editing.value &&
    createPayload.value !== null &&
    (mutationState.value !== 'idle' || failed.value),
)
const formLocked = computed(
  () => pending.value || createOperationActive.value || editReconciliation.value !== null,
)
const closeBlocked = computed(() => pending.value)
const protocolOptions = computed(() => accessKeyProtocolOptions())
const selectedGroupIDs = computed(() =>
  draft.value.scopeModes.groups === 'restricted' ? draft.value.filters.groups : [],
)
const supportedProtocolOptions = computed(() =>
  buildAccessKeyProtocolCandidates(props.groups, props.channels, selectedGroupIDs.value),
)
const catalogModelOptions = computed(() =>
  buildAccessKeyModelOptions(props.groups, [], selectedGroupIDs.value),
)
const modelOptions = computed<SearchableMultiSelectOption[]>(() => {
  const catalog = new Set(catalogModelOptions.value)
  return buildAccessKeyModelOptions(
    props.groups,
    draft.value.filters.models,
    selectedGroupIDs.value,
  ).map((model) => ({
    value: model,
    label: model,
    description: catalog.has(model) ? undefined : t('accessKeys.drawer.modelCustomUnavailable'),
  }))
})
const groupProtocolMismatch = computed(
  () =>
    props.groupCatalogState !== 'loading' &&
    props.groupCatalogState !== 'error' &&
    draft.value.scopeModes.groups === 'restricted' &&
    draft.value.scopeModes.protocols === 'restricted' &&
    !draft.value.filters.protocols.some((protocol) =>
      supportedProtocolOptions.value.includes(protocol),
    ),
)
const modelMismatch = computed(
  () =>
    draft.value.scopeModes.models === 'restricted' &&
    draft.value.filters.models.some((model) => !catalogModelOptions.value.includes(model)),
)
const dirty = computed(() => isAccessKeyDraftDirty(draft.value, base.value))
const unsavedDirty = computed(
  () =>
    dirty.value &&
    !(createOperationActive.value && createOperationRetained.value) &&
    !(editReconciliation.value && editOperationRetained.value),
)
const groupCatalog = computed(() => ({
  state: props.groupCatalogState,
  ids: props.groups.map(({ id }) => id),
}))
const scopeValid = computed(() =>
  validateAccessKeyScope({
    base: base.value?.filters ?? null,
    filters: draft.value.filters,
    modes: draft.value.scopeModes,
    groupCatalog: groupCatalog.value,
  }),
)
const valid = computed(
  () =>
    isAccessKeyDraftValid(draft.value, base.value, groupCatalog.value) &&
    isValidCustomAccessKey(draft.value.key) &&
    !groupProtocolMismatch.value,
)
const mutationFeedbackKey = computed(() => {
  if (mutationState.value === 'idle') return ''
  if (editReconciliation.value) {
    if (editReconciliation.value.idempotencyKey) {
      return mutationState.value === 'reconciling'
        ? 'accessKeys.customKey.editKeyReconciling'
        : 'accessKeys.customKey.editKeyIndeterminate'
    }
    return mutationState.value === 'reconciling'
      ? 'accessKeys.drawer.editReconciling'
      : 'accessKeys.drawer.editIndeterminate'
  }
  return mutationState.value === 'reconciling'
    ? 'accessKeys.drawer.saveReconciling'
    : 'accessKeys.drawer.saveIndeterminate'
})
const scopeFeedbackKey = computed(() => {
  if (groupProtocolMismatch.value) return 'accessKeys.drawer.groupProtocolMismatch'
  if (scopeValid.value) return ''
  const effective = materializeAccessKeyFilters(draft.value.filters, draft.value.scopeModes)
  if (
    (['groups', 'protocols', 'models'] as const).some(
      (dimension) =>
        draft.value.scopeModes[dimension] === 'restricted' && effective[dimension].length === 0,
    )
  ) {
    return 'accessKeys.drawer.scopeIncomplete'
  }
  if (props.groupCatalogState === 'loading' || props.groupCatalogState === 'error') {
    return 'accessKeys.drawer.groupScopeUnavailable'
  }
  if (props.groupCatalogState === 'stale') {
    return 'accessKeys.drawer.staleGroupScopeInvalid'
  }
  return 'accessKeys.drawer.scopeIncomplete'
})
const scopeSaveBlockerKey = computed(() => {
  switch (scopeFeedbackKey.value) {
    case 'accessKeys.drawer.groupProtocolMismatch':
      return 'accessKeys.drawer.saveBlockedGroupProtocol'
    case 'accessKeys.drawer.groupScopeUnavailable':
      return 'accessKeys.drawer.saveBlockedGroupUnavailable'
    case 'accessKeys.drawer.staleGroupScopeInvalid':
      return 'accessKeys.drawer.saveBlockedStaleScope'
    case 'accessKeys.drawer.scopeIncomplete':
      return 'accessKeys.drawer.saveBlockedScope'
    default:
      return ''
  }
})
const saveBlockerKey = computed(() => {
  if (pending.value) return 'accessKeys.drawer.saveBlockedPending'
  if (editReconciliation.value || createOperationActive.value) return ''
  if (draft.value.name.trim().length === 0) return 'accessKeys.drawer.saveBlockedName'
  if (!isValidCustomAccessKey(draft.value.key)) return 'accessKeys.customKey.invalid'
  if (!Number.isSafeInteger(draft.value.rpm_limit) || draft.value.rpm_limit < 0) {
    return 'accessKeys.drawer.saveBlockedRPM'
  }
  if (!isValidPriceMultiplier(draft.value.price_multiplier)) {
    return 'common.priceMultiplier.invalid'
  }
  if (!areAccessKeyCostLimitRulesValid(draft.value.costLimitRules)) {
    return 'accessKeys.drawer.saveBlockedCostLimits'
  }
  if (scopeSaveBlockerKey.value) return scopeSaveBlockerKey.value
  if (!valid.value) return 'accessKeys.drawer.saveBlockedInvalid'
  if (!dirty.value) return 'accessKeys.drawer.saveBlockedNoChanges'
  return ''
})
const groupOptions = computed(() => {
  const baseGroupIDs = new Set(base.value?.filters.groups ?? [])
  const options = props.groups.map((group) => ({
    id: group.id,
    label: group.name,
    disabled: props.groupCatalogState === 'stale' && !baseGroupIDs.has(group.id),
  }))
  const known = new Set(options.map(({ id }) => id))
  for (const id of draft.value.filters.groups) {
    if (!known.has(id)) {
      options.push({
        id,
        label: t('accessKeys.drawer.unknownGroup'),
        disabled: false,
      })
    }
  }
  return options.map(({ id, ...option }) => ({ value: id, ...option }))
})
const unsavedChanges = useUnsavedChanges(unsavedDirty, { blocked: closeBlocked })

function clearLocalState(): void {
  controller?.abort()
  controller = undefined
  base.value = null
  draft.value = createAccessKeyDraft()
  operationID.value = ''
  createPayload.value = null
  keyConfirmationOpen.value = false
  customKeyError.value = ''
  createOperationRetained.value = false
  editOperationRetained.value = false
  pending.value = false
  failed.value = false
  mutationState.value = 'idle'
  editReconciliation.value = null
  editNotApplied.value = false
  modelInput.value = ''
}

async function resetForOpen(): Promise<void> {
  keyConfirmationOpen.value = false
  customKeyError.value = ''
  const carriedCreateOperation = props.accessKey ? null : props.createOperation
  const carriedEditOperation =
    props.accessKey && props.editOperation?.base.id === props.accessKey.id
      ? props.editOperation
      : null
  base.value = carriedEditOperation?.base ?? props.accessKey
  draft.value = carriedCreateOperation
    ? createAccessKeyDraftFromCreateInput(carriedCreateOperation.payload)
    : carriedEditOperation
      ? createAccessKeyDraftFromUpdate(carriedEditOperation.base, carriedEditOperation.patch)
      : createAccessKeyDraft(props.accessKey)
  if (!props.accessKey && props.copyFrom && !carriedCreateOperation) {
    draft.value = createAccessKeyDraft({
      ...props.copyFrom,
      name: t('accessKeys.distribution.copyName', { name: props.copyFrom.name }),
      cost_limit_rules: [],
    })
    draft.value.costLimitRules = props.copyFrom.cost_limit_rules.map((rule) => ({
      clientKey: createUUID(),
      kind: rule.kind,
      limit_usd: rule.limit_usd,
      period_seconds: rule.period_seconds,
    }))
  }
  operationID.value = props.accessKey
    ? (carriedEditOperation?.idempotencyKey ?? createUUID())
    : (carriedCreateOperation?.idempotencyKey ?? createUUID())
  createPayload.value = carriedCreateOperation
    ? cloneAccessKeyCreatePayload(carriedCreateOperation.payload)
    : null
  createOperationRetained.value = carriedCreateOperation !== null
  editOperationRetained.value = carriedEditOperation !== null
  failed.value = false
  mutationState.value = carriedCreateOperation?.state ?? carriedEditOperation?.state ?? 'idle'
  editReconciliation.value = carriedEditOperation
  editNotApplied.value = false
  modelInput.value = ''
  await nextTick()
  await nextTick()
  formFields.value?.focusName()
}

async function setOpen(open: boolean): Promise<void> {
  if (!open && !(await unsavedChanges.confirmDiscard())) return
  if (!open) clearLocalState()
  emit('update:open', open)
}

function handleDeleted(name: string): void {
  clearLocalState()
  emit('update:editOperation', null)
  emit('deleted', name)
}

watch(
  () => [props.open, props.accessKey, props.copyFrom?.id] as const,
  ([open]) => {
    if (open) void resetForOpen()
    else clearLocalState()
  },
  { immediate: true },
)

function setGroups(groupIDs: number[]): void {
  const current = new Set(draft.value.filters.groups)
  const next = new Set(groupIDs)
  for (const groupID of next) {
    if (!current.has(groupID) && !canChangeScopeValue('groups', groupID, true)) return
  }
  for (const groupID of current) {
    if (!next.has(groupID) && !canChangeScopeValue('groups', groupID, false)) return
  }
  draft.value.filters.groups = [...next]
}

function setProtocols(protocols: AccessProtocol[]): void {
  const current = new Set(draft.value.filters.protocols)
  const next = new Set(protocols)
  for (const protocol of next) {
    if (!current.has(protocol) && !canChangeScopeValue('protocols', protocol, true)) return
  }
  for (const protocol of current) {
    if (!next.has(protocol) && !canChangeScopeValue('protocols', protocol, false)) return
  }
  draft.value.filters.protocols = [...next]
}

function addModel(): void {
  const model = modelInput.value.trim()
  if (
    !model ||
    draft.value.filters.models.includes(model) ||
    !canChangeScopeValue('models', model, true)
  ) {
    return
  }
  draft.value.filters.models = [...draft.value.filters.models, model]
  modelInput.value = ''
}

function setModels(models: string[]): void {
  const current = new Set(draft.value.filters.models)
  const next = new Set(models.map((model) => model.trim()).filter(Boolean))
  for (const model of next) {
    if (!current.has(model) && !canChangeScopeValue('models', model, true)) return
  }
  for (const model of current) {
    if (!next.has(model) && !canChangeScopeValue('models', model, false)) return
  }
  draft.value.filters.models = [...next]
}

function canChangeScopeValue(
  dimension: AccessKeyScopeDimension,
  value: number | string,
  adding: boolean,
): boolean {
  if (formLocked.value || draft.value.scopeModes[dimension] !== 'restricted') return false
  if (dimension !== 'groups') {
    if (props.groupCatalogState === 'loading' || props.groupCatalogState === 'error') return false
    return true
  }
  if (props.groupCatalogState === 'ready') return true
  if (props.groupCatalogState !== 'stale') return false
  if (!adding) return true
  return base.value?.filters.groups.includes(value as number) ?? false
}

function setScopeMode(dimension: AccessKeyScopeDimension, nextMode: AccessKeyScopeMode): void {
  const catalogBlocksChange =
    dimension === 'groups'
      ? props.groupCatalogState !== 'ready'
      : props.groupCatalogState === 'loading' || props.groupCatalogState === 'error'
  if (
    formLocked.value ||
    catalogBlocksChange ||
    (nextMode !== 'all' && nextMode !== 'restricted')
  ) {
    return
  }
  draft.value.scopeModes[dimension] = nextMode
}

function updateCustomKey(value: string): void {
  draft.value.key = value
  customKeyError.value = ''
}

function reportCustomKeyError(error: unknown): void {
  if (!(error instanceof ApiError)) return
  if (error.code === 'DUPLICATE_RESOURCE')
    customKeyError.value = t('accessKeys.customKey.duplicate')
  if (error.code === 'ACCESS_KEY_ADMIN_CONFLICT')
    customKeyError.value = t('accessKeys.customKey.adminConflict')
  if (error.code === 'INVALID_CUSTOM_ACCESS_KEY')
    customKeyError.value = t('accessKeys.customKey.invalid')
}

async function requestSave(): Promise<void> {
  if (pending.value || keyConfirmationOpen.value) return
  if (
    !createOperationActive.value &&
    !editReconciliation.value &&
    valid.value &&
    dirty.value &&
    (editing.value ? draft.value.key !== '' : estimateAccessKeyStrength(draft.value.key) === 'weak')
  ) {
    keyConfirmationOpen.value = true
    return
  }
  await save()
}

async function setKeyConfirmationOpen(open: boolean): Promise<void> {
  keyConfirmationOpen.value = open
  if (!open) {
    await nextTick()
    credentialField.value?.focus()
  }
}

async function confirmKeyChange(): Promise<void> {
  keyConfirmationOpen.value = false
  await save()
}

async function save(): Promise<void> {
  if (pending.value) {
    return
  }
  if (editReconciliation.value) {
    await reconcileEdit()
    return
  }
  if (!createOperationActive.value && (!valid.value || !dirty.value)) return
  const currentBase = base.value
  const updateBody = currentBase ? buildAccessKeyUpdatePatch(currentBase, draft.value) : null
  const activeCreatePayload = currentBase
    ? null
    : (createPayload.value ?? buildCreateAccessKeyInput(draft.value))
  if (updateBody && Object.keys(updateBody).length === 0) return

  if (activeCreatePayload && !createPayload.value) {
    createPayload.value = cloneAccessKeyCreatePayload(activeCreatePayload)
  }
  pending.value = true
  customKeyError.value = ''
  failed.value = false
  editNotApplied.value = false
  mutationState.value = 'idle'
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  const activeOperationID = operationID.value
  let savedName = ''
  let savedKind: 'created' | 'updated' | null = null
  let createdAccessKey: AccessKeyDto | null = null
  try {
    if (currentBase) {
      const saved = await updateAccessKey(
        client,
        currentBase.id,
        updateBody!,
        activeController.signal,
        updateBody?.key ? activeOperationID : undefined,
      )
      if (
        controller !== activeController ||
        !props.open ||
        operationID.value !== activeOperationID
      ) {
        return
      }
      base.value = saved
      draft.value = createAccessKeyDraft(saved)
      savedName = saved.name
      editReconciliation.value = null
      editOperationRetained.value = false
      emit('update:editOperation', null)
    } else {
      const saved = await createAccessKey(
        client,
        activeCreatePayload!,
        activeOperationID,
        activeController.signal,
      )
      if (
        controller !== activeController ||
        !props.open ||
        operationID.value !== activeOperationID
      ) {
        return
      }
      savedName = saved.name
      createdAccessKey = saved
      createPayload.value = null
      createOperationRetained.value = false
      emit('update:createOperation', null)
    }
    await applyInvalidationPlan(
      queryClient,
      mutationInvalidationPlans.accessKey[currentBase ? 'update' : 'create'],
    )
    if (createdAccessKey) {
      base.value = createdAccessKey
      draft.value = createAccessKeyDraft(createdAccessKey)
    }
    savedKind = currentBase ? 'updated' : 'created'
  } catch (error: unknown) {
    if (controller !== activeController || !props.open || operationID.value !== activeOperationID) {
      return
    }
    if (error instanceof RequestCancelledError) return
    if (updateBody?.key || activeCreatePayload?.key) reportCustomKeyError(error)
    const outcome = classifyMutationOutcome({
      kind: 'error',
      error,
      requestSent: true,
    })
    failed.value = outcome.kind === 'failed'
    if (currentBase && outcome.kind === 'failed' && outcome.reason === 'rejected') {
      operationID.value = createUUID()
    }
    if (!currentBase && outcome.kind === 'failed' && outcome.reason === 'rejected') {
      operationID.value = createUUID()
      createPayload.value = null
      createOperationRetained.value = false
      emit('update:createOperation', null)
    } else if (
      !currentBase &&
      activeCreatePayload &&
      (outcome.kind === 'indeterminate' || outcome.kind === 'reconciling')
    ) {
      emit('update:createOperation', {
        idempotencyKey: activeOperationID,
        payload: cloneAccessKeyCreatePayload(activeCreatePayload),
        state: outcome.kind,
      })
      createOperationRetained.value = true
    } else if (
      currentBase &&
      updateBody &&
      (outcome.kind === 'indeterminate' ||
        outcome.kind === 'reconciling' ||
        (!!updateBody.key &&
          outcome.kind === 'failed' &&
          outcome.reason === 'retryable-precondition'))
    ) {
      const operation: PendingAccessKeyEditOperation = {
        base: currentBase,
        patch: updateBody,
        ...(updateBody.key ? { idempotencyKey: activeOperationID } : {}),
        state: outcome.kind === 'failed' ? 'reconciling' : outcome.kind,
      }
      failed.value = false
      editReconciliation.value = operation
      editOperationRetained.value = true
      emit('update:editOperation', operation)
    }
    mutationState.value =
      editReconciliation.value?.state ??
      (outcome.kind === 'reconciling'
        ? 'reconciling'
        : outcome.kind === 'indeterminate'
          ? 'indeterminate'
          : 'idle')
  } finally {
    if (controller === activeController) {
      controller = undefined
      pending.value = false
    }
  }
  if (savedKind) emit('saved', savedKind, savedName, base.value ?? undefined)
}

async function reconcileEdit(): Promise<void> {
  const attempt = editReconciliation.value
  if (!attempt || pending.value) return
  pending.value = true
  failed.value = false
  editNotApplied.value = false
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  let confirmedName: string | null = null
  try {
    let latest: AccessKeyDto | undefined
    if (attempt.idempotencyKey) {
      try {
        latest = await updateAccessKey(
          client,
          attempt.base.id,
          attempt.patch,
          activeController.signal,
          attempt.idempotencyKey,
        )
      } catch (error: unknown) {
        const outcome = classifyMutationOutcome({ kind: 'error', error, requestSent: true })
        if (
          outcome.kind !== 'failed' ||
          outcome.reason !== 'expired-known' ||
          outcome.resource_identity !== `access-key:${attempt.base.id}`
        )
          throw error
        // 完成记录已过期时只读取当前状态，不能重新执行旧的密钥替换。
        latest = await findAccessKeyForReconciliation(
          client,
          attempt.base.id,
          activeController.signal,
        )
      }
    } else {
      latest = await findAccessKeyForReconciliation(
        client,
        attempt.base.id,
        activeController.signal,
      )
    }
    if (controller !== activeController || editReconciliation.value !== attempt || !props.open) {
      return
    }
    await applyInvalidationPlan(
      queryClient,
      mutationInvalidationPlans.accessKey.reconcile,
      () => controller === activeController && editReconciliation.value === attempt && props.open,
    )
    if (controller !== activeController || editReconciliation.value !== attempt || !props.open) {
      return
    }
    if (!latest) {
      editReconciliation.value = null
      editOperationRetained.value = false
      emit('update:editOperation', null)
      mutationState.value = 'idle'
      failed.value = true
      return
    }
    if (
      attempt.idempotencyKey ||
      accessKeyMatchesUpdatePatch(latest, attempt.patch, attempt.base)
    ) {
      base.value = latest
      draft.value = createAccessKeyDraft(latest)
      editReconciliation.value = null
      editOperationRetained.value = false
      emit('update:editOperation', null)
      mutationState.value = 'idle'
      await applyInvalidationPlan(
        queryClient,
        mutationInvalidationPlans.accessKey.reconcileConfirmed,
        () => controller === activeController && props.open,
      )
      if (controller === activeController && props.open) confirmedName = latest.name
    } else if (
      Object.keys(buildAccessKeyUpdatePatch(attempt.base, createAccessKeyDraft(latest))).length ===
      0
    ) {
      base.value = latest
      editReconciliation.value = null
      editOperationRetained.value = false
      emit('update:editOperation', null)
      mutationState.value = 'idle'
      failed.value = true
      editNotApplied.value = true
      return
    } else {
      const operation: PendingAccessKeyEditOperation = {
        ...attempt,
        state: 'indeterminate',
      }
      editReconciliation.value = operation
      emit('update:editOperation', operation)
      mutationState.value = operation.state
    }
  } catch (error: unknown) {
    if (
      controller === activeController &&
      editReconciliation.value === attempt &&
      !(error instanceof RequestCancelledError)
    ) {
      if (attempt.patch.key) reportCustomKeyError(error)
      const outcome = classifyMutationOutcome({ kind: 'error', error, requestSent: true })
      if (attempt.idempotencyKey && outcome.kind === 'failed' && outcome.reason === 'rejected') {
        editReconciliation.value = null
        editOperationRetained.value = false
        emit('update:editOperation', null)
        operationID.value = createUUID()
        mutationState.value = 'idle'
        failed.value = true
      } else {
        const operation: PendingAccessKeyEditOperation = {
          ...attempt,
          state:
            outcome.kind === 'reconciling' ||
            (outcome.kind === 'failed' && outcome.reason === 'retryable-precondition')
              ? 'reconciling'
              : 'indeterminate',
        }
        editReconciliation.value = operation
        emit('update:editOperation', operation)
        mutationState.value = operation.state
      }
    }
  } finally {
    if (controller === activeController) {
      controller = undefined
      pending.value = false
    }
  }
  if (confirmedName && props.open) emit('saved', 'updated', confirmedName)
}

onBeforeUnmount(clearLocalState)
</script>

<template>
  <AppDrawer
    :open="open"
    :title="editing ? t('accessKeys.drawer.editTitle') : t('accessKeys.drawer.createTitle')"
    :description="
      t(editing ? 'accessKeys.drawer.editDescription' : 'accessKeys.drawer.createDescription')
    "
    :close-label="t('accessKeys.drawer.close')"
    :dismissible="!closeBlocked"
    show-description
    @update:open="setOpen"
  >
    <template #trigger><slot name="trigger" /></template>

    <form id="access-key-drawer-form" class="access-key-drawer" @submit.prevent="requestSave">
      <AccessKeyOperationFeedback
        :failed="failed"
        :edit-not-applied="editNotApplied"
        :mutation-feedback-key="mutationFeedbackKey"
      />

      <section class="drawer-section">
        <h3>{{ t('accessKeys.drawer.basicInformation') }}</h3>
        <p>{{ t('accessKeys.drawer.basicInformationDescription') }}</p>
        <AccessKeyFormFields
          ref="formFields"
          :name="draft.name"
          :status="draft.status"
          :rpm-limit="draft.rpm_limit"
          :price-multiplier="draft.price_multiplier"
          :disabled="formLocked"
          @update:name="draft.name = $event"
          @update:status="draft.status = $event"
          @update:rpm-limit="draft.rpm_limit = $event"
          @update:price-multiplier="draft.price_multiplier = $event"
        >
          <template #credential>
            <AccessKeyCredentialField
              ref="credentialField"
              :model-value="draft.key"
              :editing="editing"
              :current-mask="base?.masked_key"
              :disabled="formLocked"
              :error="customKeyError"
              @update:model-value="updateCustomKey"
            />
          </template>
        </AccessKeyFormFields>
      </section>

      <section class="drawer-section">
        <AccessKeyCostLimitEditor
          :model-value="draft.costLimitRules"
          :runtime-status="base?.cost_limit_status ?? null"
          :disabled="formLocked"
          @update:model-value="draft.costLimitRules = $event"
        />
      </section>

      <section class="drawer-section">
        <h3>{{ t('accessKeys.drawer.accessPolicy') }}</h3>
        <p>{{ t('accessKeys.drawer.accessPolicyDescription') }}</p>
        <AccessKeyPolicyFields
          :expiration-mode="draft.expirationMode"
          :expires-at="draft.expires_at_ms"
          :base-expires-at="base?.expires_at_ms"
          :source-mode="draft.sourceMode"
          :allowed-cidrs="draft.filters.allowed_cidrs"
          :disabled="formLocked"
          @update:expiration-mode="draft.expirationMode = $event"
          @update:expires-at="draft.expires_at_ms = $event"
          @update:source-mode="draft.sourceMode = $event"
          @update:allowed-cidrs="draft.filters.allowed_cidrs = $event"
        />
      </section>

      <section class="drawer-section">
        <h3>{{ t('accessKeys.drawer.permissionScope') }}</h3>
        <p>{{ t('accessKeys.drawer.permissionScopeDescription') }}</p>
        <div class="access-key-scope-logic" :aria-label="t('accessKeys.drawer.scopeLogic')">
          <span>{{ t('accessKeys.drawer.scopeLogicGroups') }}</span>
          <b>AND</b>
          <span>{{ t('accessKeys.drawer.scopeLogicProtocols') }}</span>
          <b>AND</b>
          <span>{{ t('accessKeys.drawer.scopeLogicModels') }}</span>
        </div>
        <div class="access-key-scope-editors">
          <AccessKeyScopeEditor
            v-model:model-input="modelInput"
            :modes="draft.scopeModes"
            :filters="draft.filters"
            :group-options="groupOptions"
            :group-catalog-state="groupCatalogState"
            :protocol-options="protocolOptions"
            :model-options="modelOptions"
            :disabled="formLocked"
            :model-mismatch="modelMismatch"
            @set-scope-mode="setScopeMode"
            @update:groups="setGroups"
            @update:protocols="setProtocols"
            @update:models="setModels"
            @add-model="addModel"
          />
        </div>
        <div class="access-key-scope-warning">
          <span aria-hidden="true">!</span>
          <p>{{ t('accessKeys.drawer.scopeExpansionWarning') }}</p>
        </div>
      </section>
    </form>

    <template #footer>
      <div v-if="editing && base" class="access-key-drawer__management">
        <AccessKeyDeleteDialog
          :access-key="base"
          :total="total"
          :disabled="formLocked"
          @deleted="handleDeleted"
        />
      </div>
      <p
        id="access-key-save-blocker"
        class="access-key-drawer__save-blocker"
        role="status"
        aria-live="polite"
        :title="saveBlockerKey ? t(saveBlockerKey) : undefined"
      >
        {{ saveBlockerKey ? t(saveBlockerKey) : '' }}
      </p>
      <AppButton
        variant="secondary"
        size="compact"
        :disabled="closeBlocked"
        @click="setOpen(false)"
      >
        {{ t('common.cancel') }}
      </AppButton>
      <AppButton
        type="submit"
        form="access-key-drawer-form"
        size="compact"
        :busy="pending"
        :aria-describedby="saveBlockerKey ? 'access-key-save-blocker' : undefined"
        :disabled="!editReconciliation && !createOperationActive && (!valid || !dirty)"
      >
        <Save :size="16" aria-hidden="true" />{{
          t(
            editReconciliation || createOperationActive
              ? 'accessKeys.drawer.checkResult'
              : editing
                ? 'accessKeys.drawer.saveChanges'
                : 'accessKeys.drawer.createKey',
          )
        }}
      </AppButton>
    </template>
  </AppDrawer>
  <AppConfirmDialog
    :open="keyConfirmationOpen"
    :title="t(editing ? 'accessKeys.customKey.replaceTitle' : 'accessKeys.customKey.warningTitle')"
    :description="keyConfirmationDescription"
    :close-label="t('accessKeys.customKey.warningClose')"
    :cancel-label="t('accessKeys.customKey.returnToEdit')"
    :confirm-label="keyConfirmationLabel"
    description-tone="warning"
    focus-cancel
    prevent-close-auto-focus
    @update:open="setKeyConfirmationOpen"
    @confirm="confirmKeyChange"
  />
</template>

<style scoped>
.access-key-drawer {
  display: block;
  font-size: var(--text-body);
}
.access-key-drawer__management {
  display: flex;
  flex: none;
  gap: var(--space-2);
}
.access-key-drawer__save-blocker {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  margin: 0;
  color: var(--color-danger);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.drawer-section + .drawer-section {
  margin-top: 22px;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 20px;
}
.drawer-section h3 {
  margin: 0 0 4px;
  font-size: var(--text-meta);
}
.drawer-section > p {
  margin: 0 0 12px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.access-key-scope-logic {
  display: grid;
  grid-template-columns: 1fr auto 1fr auto 1fr;
  align-items: center;
  gap: 6px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 9px;
  text-align: center;
}
.access-key-scope-logic span {
  border-radius: var(--radius-tag);
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 6px;
  font-size: var(--text-label-xs);
}
.access-key-scope-logic b {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: 9px;
}
.access-key-scope-editors {
  margin-top: 12px;
}
.access-key-scope-warning {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-top: 12px;
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-control);
  background: var(--color-warning-bg);
  color: var(--color-warning);
  padding: 10px 12px;
  font-size: var(--text-sm);
}
.access-key-scope-warning > span {
  display: grid;
  width: 17px;
  height: 17px;
  flex: none;
  place-items: center;
  border: 1px solid currentColor;
  border-radius: 50%;
  font-family: var(--font-serif);
  font-size: var(--text-label-xs);
  font-weight: 700;
}
.access-key-scope-warning p {
  margin: 0;
}
@media (max-width: 480px) {
  .access-key-scope-logic {
    grid-template-columns: 1fr;
  }
  .access-key-scope-logic b::after {
    content: ' · ';
  }
}
</style>
