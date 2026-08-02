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
import { RequestCancelledError } from '@/api/errors'
import { classifyMutationOutcome } from '@/app/mutation-outcome'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import type { SearchableMultiSelectOption } from '@/components/ui/SearchableMultiSelect.vue'

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
import AccessKeyFormFields from './AccessKeyFormFields.vue'
import AccessKeyOperationFeedback from './AccessKeyOperationFeedback.vue'
import AccessKeyScopeEditor from './AccessKeyScopeEditor.vue'
import {
  materializeAccessKeyFilters,
  validateAccessKeyScope,
  type AccessKeyScopeDimension,
  type AccessKeyScopeMode,
  type GroupCatalogState,
} from './access-key-scope'
import {
  accessKeyMatchesUpdatePatch,
  buildAccessKeyUpdatePatch,
  buildCreateAccessKeyInput,
  createAccessKeyDraft,
  createAccessKeyDraftFromCreateInput,
  isAccessKeyDraftDirty,
  isAccessKeyDraftValid,
  type AccessKeyDraft,
} from './access-key-patch'

const props = withDefaults(
  defineProps<{
    open: boolean
    accessKey: AccessKeyDto | null
    groups: GroupOptionDto[]
    total: number
    groupCatalogState?: GroupCatalogState
    createOperation?: PendingAccessKeyCreateOperation | null
    editOperation?: PendingAccessKeyEditOperation | null
  }>(),
  { createOperation: null, editOperation: null, groupCatalogState: 'ready' },
)
const emit = defineEmits<{
  'update:open': [open: boolean]
  'update:createOperation': [operation: PendingAccessKeyCreateOperation | null]
  'update:editOperation': [operation: PendingAccessKeyEditOperation | null]
  saved: [kind: 'created' | 'updated', name: string]
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
  buildAccessKeyProtocolCandidates(props.groups, selectedGroupIDs.value),
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
    !groupProtocolMismatch.value,
)
const mutationFeedbackKey = computed(() => {
  if (mutationState.value === 'idle') return ''
  if (editReconciliation.value) {
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
  const carriedCreateOperation = props.accessKey ? null : props.createOperation
  const carriedEditOperation =
    props.accessKey && props.editOperation?.base.id === props.accessKey.id
      ? props.editOperation
      : null
  base.value = carriedEditOperation?.base ?? props.accessKey
  draft.value = carriedCreateOperation
    ? createAccessKeyDraftFromCreateInput(carriedCreateOperation.payload)
    : carriedEditOperation
      ? createAccessKeyDraft({
          ...carriedEditOperation.base,
          ...carriedEditOperation.patch,
          filters: carriedEditOperation.patch.filters ?? carriedEditOperation.base.filters,
        })
      : createAccessKeyDraft(props.accessKey)
  operationID.value = props.accessKey
    ? ''
    : (carriedCreateOperation?.idempotencyKey ?? crypto.randomUUID())
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
  emit('deleted', name)
}

watch(
  () => [props.open, props.accessKey] as const,
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
  failed.value = false
  editNotApplied.value = false
  mutationState.value = 'idle'
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  const activeOperationID = operationID.value
  let savedName = ''
  try {
    if (currentBase) {
      const saved = await updateAccessKey(
        client,
        currentBase.id,
        updateBody!,
        activeController.signal,
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
      createPayload.value = null
      createOperationRetained.value = false
      emit('update:createOperation', null)
    }
    await applyInvalidationPlan(
      queryClient,
      mutationInvalidationPlans.accessKey[currentBase ? 'update' : 'create'],
    )
    emit('saved', currentBase ? 'updated' : 'created', savedName)
  } catch (error: unknown) {
    if (controller !== activeController || !props.open || operationID.value !== activeOperationID) {
      return
    }
    if (error instanceof RequestCancelledError) return
    const outcome = classifyMutationOutcome({
      kind: 'error',
      error,
      requestSent: true,
    })
    failed.value = outcome.kind === 'failed'
    if (!currentBase && outcome.kind === 'failed' && outcome.reason === 'rejected') {
      operationID.value = crypto.randomUUID()
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
      (outcome.kind === 'indeterminate' || outcome.kind === 'reconciling')
    ) {
      const operation: PendingAccessKeyEditOperation = {
        base: currentBase,
        patch: updateBody,
        state: outcome.kind,
      }
      editReconciliation.value = operation
      editOperationRetained.value = true
      emit('update:editOperation', operation)
    }
    mutationState.value =
      outcome.kind === 'reconciling'
        ? 'reconciling'
        : outcome.kind === 'indeterminate'
          ? 'indeterminate'
          : 'idle'
  } finally {
    if (controller === activeController) {
      controller = undefined
      pending.value = false
    }
  }
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
  try {
    const latest = await findAccessKeyForReconciliation(
      client,
      attempt.base.id,
      activeController.signal,
    )
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
    if (accessKeyMatchesUpdatePatch(latest, attempt.patch)) {
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
      if (controller === activeController && props.open) emit('saved', 'updated', latest.name)
      return
    }
    if (
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
    }
    const operation: PendingAccessKeyEditOperation = {
      ...attempt,
      state: 'indeterminate',
    }
    editReconciliation.value = operation
    emit('update:editOperation', operation)
    mutationState.value = operation.state
  } catch (error: unknown) {
    if (
      controller === activeController &&
      editReconciliation.value === attempt &&
      !(error instanceof RequestCancelledError)
    ) {
      const operation: PendingAccessKeyEditOperation = {
        ...attempt,
        state: 'indeterminate',
      }
      editReconciliation.value = operation
      emit('update:editOperation', operation)
      mutationState.value = operation.state
    }
  } finally {
    if (controller === activeController) {
      controller = undefined
      pending.value = false
    }
  }
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

    <form id="access-key-drawer-form" class="access-key-drawer" @submit.prevent="save">
      <AccessKeyOperationFeedback
        :failed="failed"
        :edit-not-applied="editNotApplied"
        :mutation-feedback-key="mutationFeedbackKey"
        :scope-feedback-key="scopeFeedbackKey"
        :show-scope-feedback="!createOperationActive"
      />

      <section class="drawer-section">
        <h3>{{ t('accessKeys.drawer.basicInformation') }}</h3>
        <p>{{ t('accessKeys.drawer.basicInformationDescription') }}</p>
        <AccessKeyFormFields
          ref="formFields"
          :name="draft.name"
          :status="draft.status"
          :rpm-limit="draft.rpm_limit"
          :disabled="formLocked"
          @update:name="draft.name = $event"
          @update:status="draft.status = $event"
          @update:rpm-limit="draft.rpm_limit = $event"
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
      <div v-if="editing && base" class="access-key-drawer__delete">
        <AccessKeyDeleteDialog :access-key="base" :total="total" @deleted="handleDeleted" />
      </div>
      <AppButton variant="secondary" :disabled="closeBlocked" @click="setOpen(false)">
        {{ t('common.cancel') }}
      </AppButton>
      <AppButton
        type="submit"
        form="access-key-drawer-form"
        :busy="pending"
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
</template>

<style scoped>
.access-key-drawer {
  display: block;
  font-size: var(--text-body);
}
.access-key-drawer__delete {
  margin-right: auto;
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
