<script setup lang="ts">
import { Save } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'

import { useApiClient } from '@/api/client-context'
import {
  createAccessKey,
  listAccessKeys,
  revealAccessKey,
  updateAccessKey,
  type CreateAccessKeyRequest,
} from '@/app/resources/access-keys'
import type { AccessKeyDto, AccessProtocol, GroupSummary } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import { classifyMutationOutcome } from '@/app/mutation-outcome'
import { accessKeyResources } from '@/app/resources/access-keys'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'

import { accessKeyProtocolOptions, buildAccessKeyModelOptions } from './access-key-options'
import type { PendingAccessKeyCreateOperation } from './access-key-create-operation'
import type { PendingAccessKeyEditOperation } from './access-key-edit-operation'
import AccessKeyFormFields from './AccessKeyFormFields.vue'
import AccessKeyOperationFeedback from './AccessKeyOperationFeedback.vue'
import AccessKeyResultPanel from './AccessKeyResultPanel.vue'
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
import { useEphemeralSecret } from './use-ephemeral-secret'

const props = withDefaults(
  defineProps<{
    open: boolean
    accessKey: AccessKeyDto | null
    groups: GroupSummary[]
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
}>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const formFields = ref<InstanceType<typeof AccessKeyFormFields>>()
const base = ref<AccessKeyDto | null>(null)
const draft = ref<AccessKeyDraft>(createAccessKeyDraft())
const result = ref<AccessKeyDto | null>(null)
const operationID = ref('')
const createPayload = ref<CreateAccessKeyRequest | null>(null)
const createOperationRetained = ref(false)
const editOperationRetained = ref(false)
const pending = ref(false)
const failed = ref(false)
const mutationState = ref<'idle' | 'indeterminate' | 'reconciling'>('idle')
const editReconciliation = ref<PendingAccessKeyEditOperation | null>(null)
const editNotApplied = ref(false)
const revealPending = ref(false)
const revealFailed = ref(false)
const modelInput = ref('')
let controller: AbortController | undefined
let revealController: AbortController | undefined
const ephemeralSecret = useEphemeralSecret()

const editing = computed(() => base.value !== null)
const createCompleted = computed(() => !editing.value && result.value !== null)
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
const resultSecret = computed(() =>
  result.value ? ephemeralSecret.read(`access-key:${result.value.id}`) : null,
)
const protocolOptions = computed(() => accessKeyProtocolOptions())
const modelOptions = computed(() =>
  buildAccessKeyModelOptions(props.groups, draft.value.filters.models),
)
const dirty = computed(() => isAccessKeyDraftDirty(draft.value, base.value))
const unsavedDirty = computed(
  () =>
    dirty.value &&
    !createCompleted.value &&
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
const valid = computed(() => isAccessKeyDraftValid(draft.value, base.value, groupCatalog.value))
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
  const options = props.groups.map((group) => ({
    id: group.id,
    label: group.name,
    dangling: false,
  }))
  const known = new Set(options.map(({ id }) => id))
  for (const id of base.value?.filters.groups ?? []) {
    if (!known.has(id)) {
      options.push({ id, label: t('accessKeys.drawer.unknownGroup', { id }), dangling: true })
    }
  }
  return options
})
const unsavedChanges = useUnsavedChanges(unsavedDirty, { blocked: closeBlocked })

function cloneCreatePayload(payload: CreateAccessKeyRequest): CreateAccessKeyRequest {
  return {
    name: payload.name,
    filters: {
      groups: [...payload.filters.groups],
      protocols: [...payload.filters.protocols],
      models: [...payload.filters.models],
    },
    rpm_limit: payload.rpm_limit,
  }
}

function clearLocalState(): void {
  controller?.abort()
  revealController?.abort()
  controller = undefined
  revealController = undefined
  ephemeralSecret.clear()
  base.value = null
  draft.value = createAccessKeyDraft()
  result.value = null
  operationID.value = ''
  createPayload.value = null
  createOperationRetained.value = false
  editOperationRetained.value = false
  pending.value = false
  revealPending.value = false
  revealFailed.value = false
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
  result.value = null
  operationID.value = props.accessKey
    ? ''
    : (carriedCreateOperation?.idempotencyKey ?? crypto.randomUUID())
  createPayload.value = carriedCreateOperation
    ? cloneCreatePayload(carriedCreateOperation.payload)
    : null
  createOperationRetained.value = carriedCreateOperation !== null
  editOperationRetained.value = carriedEditOperation !== null
  ephemeralSecret.clear()
  failed.value = false
  mutationState.value = carriedCreateOperation?.state ?? carriedEditOperation?.state ?? 'idle'
  editReconciliation.value = carriedEditOperation
  editNotApplied.value = false
  modelInput.value = ''
  await nextTick()
  await nextTick()
  formFields.value?.focusName()
}

function setOpen(open: boolean): void {
  if (!open && !unsavedChanges.confirmDiscard()) return
  if (!open) clearLocalState()
  emit('update:open', open)
}

watch(
  () => [props.open, props.accessKey] as const,
  ([open]) => {
    if (open) void resetForOpen()
    else clearLocalState()
  },
  { immediate: true },
)

function toggleGroup(groupId: number, checked: boolean): void {
  if (!canChangeScopeValue('groups', groupId, checked)) return
  draft.value.filters.groups = checked
    ? [...new Set([...draft.value.filters.groups, groupId])]
    : draft.value.filters.groups.filter((id) => id !== groupId)
}

function toggleProtocol(protocol: AccessProtocol, checked: boolean): void {
  if (!canChangeScopeValue('protocols', protocol, checked)) return
  draft.value.filters.protocols = checked
    ? [...new Set([...draft.value.filters.protocols, protocol])]
    : draft.value.filters.protocols.filter((value) => value !== protocol)
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

function removeModel(model: string): void {
  if (!canChangeScopeValue('models', model, false)) return
  draft.value.filters.models = draft.value.filters.models.filter((value) => value !== model)
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
  const currentMode = draft.value.scopeModes[dimension]
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
  if (
    currentMode === 'restricted' &&
    nextMode === 'all' &&
    !window.confirm(t('accessKeys.drawer.expandScopeConfirmation'))
  ) {
    return
  }
  draft.value.scopeModes[dimension] = nextMode
}

async function save(): Promise<void> {
  if (createCompleted.value || pending.value) {
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
    createPayload.value = cloneCreatePayload(activeCreatePayload)
  }
  pending.value = true
  failed.value = false
  editNotApplied.value = false
  mutationState.value = 'idle'
  result.value = null
  ephemeralSecret.clear()
  const expectedSecretEpoch = ephemeralSecret.epoch.value
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  const activeOperationID = operationID.value
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
      result.value = null
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
      const key = saved.key
      const metadata: AccessKeyDto = {
        id: saved.id,
        name: saved.name,
        masked_key: saved.masked_key,
        status: saved.status,
        filters: saved.filters,
        rpm_limit: saved.rpm_limit,
        created_at_ms: saved.created_at_ms,
        updated_at_ms: saved.updated_at_ms,
      }
      result.value = metadata
      createPayload.value = null
      createOperationRetained.value = false
      emit('update:createOperation', null)
      if (key && ephemeralSecret.epoch.value === expectedSecretEpoch) {
        ephemeralSecret.expose(`access-key:${metadata.id}`, key)
      }
    }
    await applyInvalidationPlan(
      queryClient,
      mutationInvalidationPlans.accessKey[currentBase ? 'update' : 'create'],
    )
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
        payload: cloneCreatePayload(activeCreatePayload),
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
    const accessKeys = await listAccessKeys(client, activeController.signal)
    if (controller !== activeController || editReconciliation.value !== attempt || !props.open) {
      return
    }
    queryClient.setQueryData(accessKeyResources.list.queryKey, accessKeys)
    void applyInvalidationPlan(queryClient, mutationInvalidationPlans.accessKey.reconcile)
    const latest = accessKeys.find((accessKey) => accessKey.id === attempt.base.id)
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

async function revealResultSecret(): Promise<void> {
  const current = result.value
  if (!current || revealPending.value) return
  if (resultSecret.value) {
    ephemeralSecret.clear()
    return
  }
  revealController?.abort()
  const controller = new AbortController()
  revealController = controller
  const expectedEpoch = ephemeralSecret.epoch.value
  revealPending.value = true
  revealFailed.value = false
  try {
    const revealed = await revealAccessKey(client, current.id, controller.signal)
    if (
      revealController === controller &&
      result.value?.id === current.id &&
      props.open &&
      ephemeralSecret.epoch.value === expectedEpoch
    ) {
      ephemeralSecret.expose(`access-key:${current.id}`, revealed.key)
    }
  } catch (error: unknown) {
    if (revealController === controller && !(error instanceof RequestCancelledError)) {
      revealFailed.value = true
    }
  } finally {
    if (revealController === controller) {
      revealController = undefined
      revealPending.value = false
    }
  }
}

onBeforeUnmount(clearLocalState)
</script>

<template>
  <AppDrawer
    :open="open"
    :title="editing ? t('accessKeys.drawer.editTitle') : t('accessKeys.drawer.createTitle')"
    :description="t('accessKeys.drawer.description')"
    :close-label="t('accessKeys.drawer.close')"
    :dismissible="!closeBlocked"
    @update:open="setOpen"
  >
    <template #trigger><slot name="trigger" /></template>

    <form class="access-key-drawer" @submit.prevent="save">
      <AccessKeyOperationFeedback
        :failed="failed"
        :edit-not-applied="editNotApplied"
        :reveal-failed="revealFailed"
        :mutation-feedback-key="mutationFeedbackKey"
        :scope-feedback-key="scopeFeedbackKey"
        :show-scope-feedback="!createOperationActive"
      />

      <template v-if="!createCompleted">
        <AccessKeyFormFields
          ref="formFields"
          :name="draft.name"
          :status="draft.status"
          :rpm-limit="draft.rpm_limit"
          :editing="editing"
          :disabled="formLocked"
          @update:name="draft.name = $event"
          @update:status="draft.status = $event"
          @update:rpm-limit="draft.rpm_limit = $event"
        />
        <AccessKeyScopeEditor
          v-model:model-input="modelInput"
          :modes="draft.scopeModes"
          :filters="draft.filters"
          :group-options="groupOptions"
          :group-catalog-state="groupCatalogState"
          :protocol-options="protocolOptions"
          :model-options="modelOptions"
          :base-group-ids="base?.filters.groups ?? []"
          :disabled="formLocked"
          @set-scope-mode="setScopeMode"
          @toggle-group="toggleGroup"
          @toggle-protocol="toggleProtocol"
          @add-model="addModel"
          @remove-model="removeModel"
        />
      </template>

      <AccessKeyResultPanel
        v-if="result"
        :result="result"
        :secret="resultSecret"
        :reveal-pending="revealPending"
        @reveal="revealResultSecret"
        @clear="ephemeralSecret.clear"
      />

      <div class="access-key-drawer__actions">
        <AppButton variant="secondary" :disabled="closeBlocked" @click="setOpen(false)">
          {{ t(createCompleted ? 'common.close' : 'common.cancel') }}
        </AppButton>
        <AppButton
          v-if="!createCompleted"
          type="submit"
          :busy="pending"
          :disabled="!editReconciliation && !createOperationActive && (!valid || !dirty)"
        >
          <Save :size="16" aria-hidden="true" />{{
            t(
              editReconciliation || createOperationActive
                ? 'accessKeys.drawer.checkResult'
                : 'accessKeys.drawer.save',
            )
          }}
        </AppButton>
      </div>
    </form>
  </AppDrawer>
</template>

<style scoped>
.access-key-drawer {
  display: grid;
  gap: var(--space-5);
  font-size: 1rem;
}
.access-key-drawer__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.access-key-drawer__actions {
  justify-content: flex-end;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-4);
}
</style>
