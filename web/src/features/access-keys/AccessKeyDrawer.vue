<script setup lang="ts">
import { KeyRound, Plus, Save, X } from 'lucide-vue-next'
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
  type UpdateAccessKeyRequest,
} from '@/api/control/access-keys'
import type { AccessKeyDto, AccessProtocol, GroupSummary } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import { classifyMutationOutcome } from '@/app/mutation-outcome'
import { accessKeyMutationInvalidations, accessKeyResources } from '@/app/resources/access-keys'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SecretValue from '@/components/ui/SecretValue.vue'

import { accessKeyProtocolOptions, buildAccessKeyModelOptions } from './access-key-options'
import type { PendingAccessKeyCreateOperation } from './access-key-create-operation'
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

interface PendingAccessKeyEditReconciliation {
  base: AccessKeyDto
  patch: UpdateAccessKeyRequest
}

const props = withDefaults(
  defineProps<{
    open: boolean
    accessKey: AccessKeyDto | null
    groups: GroupSummary[]
    groupCatalogState?: GroupCatalogState
    createOperation?: PendingAccessKeyCreateOperation | null
  }>(),
  { createOperation: null, groupCatalogState: 'ready' },
)
const emit = defineEmits<{
  'update:open': [open: boolean]
  'update:createOperation': [operation: PendingAccessKeyCreateOperation | null]
}>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const nameInput = ref<HTMLInputElement>()
const base = ref<AccessKeyDto | null>(null)
const draft = ref<AccessKeyDraft>(createAccessKeyDraft())
const result = ref<AccessKeyDto | null>(null)
const operationID = ref('')
const createPayload = ref<CreateAccessKeyRequest | null>(null)
const createOperationRetained = ref(false)
const pending = ref(false)
const failed = ref(false)
const mutationState = ref<'idle' | 'indeterminate' | 'reconciling'>('idle')
const editReconciliation = ref<PendingAccessKeyEditReconciliation | null>(null)
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
const closeBlocked = computed(() => pending.value || editReconciliation.value !== null)
const resultSecret = computed(() =>
  result.value ? ephemeralSecret.read(`access-key:${result.value.id}`) : null,
)
const protocolOptions = computed(() => accessKeyProtocolOptions(base.value?.filters.protocols))
const modelOptions = computed(() =>
  buildAccessKeyModelOptions(props.groups, draft.value.filters.models),
)
const dirty = computed(() => isAccessKeyDraftDirty(draft.value, base.value))
const unsavedDirty = computed(
  () =>
    dirty.value &&
    !createCompleted.value &&
    !(createOperationActive.value && createOperationRetained.value),
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
  const carriedOperation = props.accessKey ? null : props.createOperation
  base.value = props.accessKey
  draft.value = carriedOperation
    ? createAccessKeyDraftFromCreateInput(carriedOperation.payload)
    : createAccessKeyDraft(props.accessKey)
  result.value = null
  operationID.value = props.accessKey
    ? ''
    : (carriedOperation?.idempotencyKey ?? crypto.randomUUID())
  createPayload.value = carriedOperation ? cloneCreatePayload(carriedOperation.payload) : null
  createOperationRetained.value = carriedOperation !== null
  ephemeralSecret.clear()
  failed.value = false
  mutationState.value = carriedOperation?.state ?? 'idle'
  editReconciliation.value = null
  editNotApplied.value = false
  modelInput.value = ''
  await nextTick()
  await nextTick()
  nameInput.value?.focus()
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

function setScopeMode(dimension: AccessKeyScopeDimension, event: Event): void {
  const target = event.target as HTMLSelectElement
  const nextMode = target.value as AccessKeyScopeMode
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
    target.value = currentMode
    return
  }
  if (
    currentMode === 'restricted' &&
    nextMode === 'all' &&
    !window.confirm(t('accessKeys.drawer.expandScopeConfirmation'))
  ) {
    target.value = currentMode
    return
  }
  draft.value.scopeModes[dimension] = nextMode
}

function setRPM(event: Event): void {
  draft.value.rpm_limit = Number((event.target as HTMLInputElement).value)
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
        created_at: saved.created_at,
        updated_at: saved.updated_at,
      }
      result.value = metadata
      createPayload.value = null
      createOperationRetained.value = false
      emit('update:createOperation', null)
      if (key && ephemeralSecret.epoch.value === expectedSecretEpoch) {
        ephemeralSecret.expose(`access-key:${metadata.id}`, key)
      }
    }
    await Promise.all(
      accessKeyMutationInvalidations[currentBase ? 'update' : 'create'].map((queryKey) =>
        queryClient.invalidateQueries({ queryKey }),
      ),
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
      editReconciliation.value = {
        base: currentBase,
        patch: updateBody,
      }
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
    void queryClient.invalidateQueries({ queryKey: accessKeyResources.options.queryKey })
    const latest = accessKeys.find((accessKey) => accessKey.id === attempt.base.id)
    if (!latest) {
      editReconciliation.value = null
      mutationState.value = 'idle'
      failed.value = true
      return
    }
    if (accessKeyMatchesUpdatePatch(latest, attempt.patch)) {
      base.value = latest
      draft.value = createAccessKeyDraft(latest)
      editReconciliation.value = null
      mutationState.value = 'idle'
      return
    }
    if (
      Object.keys(buildAccessKeyUpdatePatch(attempt.base, createAccessKeyDraft(latest))).length ===
      0
    ) {
      base.value = latest
      editReconciliation.value = null
      mutationState.value = 'idle'
      failed.value = true
      editNotApplied.value = true
      return
    }
    mutationState.value = 'indeterminate'
  } catch (error: unknown) {
    if (
      controller === activeController &&
      editReconciliation.value === attempt &&
      !(error instanceof RequestCancelledError)
    ) {
      mutationState.value = 'indeterminate'
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
      <InlineFeedback v-if="failed" tone="danger">{{
        t(editNotApplied ? 'accessKeys.drawer.editNotApplied' : 'accessKeys.drawer.saveFailed')
      }}</InlineFeedback>
      <InlineFeedback v-if="revealFailed" tone="danger">{{
        t('accessKeys.revealFailed')
      }}</InlineFeedback>
      <InlineFeedback v-if="mutationFeedbackKey" tone="warning">{{
        t(mutationFeedbackKey)
      }}</InlineFeedback>
      <InlineFeedback v-if="scopeFeedbackKey && !createOperationActive" tone="warning">{{
        t(scopeFeedbackKey)
      }}</InlineFeedback>

      <template v-if="!createCompleted">
        <label class="access-key-drawer__field" for="access-key-name">
          <span>{{ t('accessKeys.drawer.name') }}</span>
          <input
            id="access-key-name"
            ref="nameInput"
            v-model="draft.name"
            data-test="access-key-name"
            type="text"
            autocomplete="off"
            :disabled="formLocked"
          />
        </label>

        <label v-if="editing" class="access-key-drawer__field" for="access-key-status">
          <span>{{ t('accessKeys.drawer.status') }}</span>
          <select
            id="access-key-status"
            v-model="draft.status"
            data-test="access-key-status"
            :disabled="formLocked"
          >
            <option value="active">{{ t('accessKeys.status.active') }}</option>
            <option value="disabled">{{ t('accessKeys.status.disabled') }}</option>
          </select>
        </label>

        <fieldset>
          <legend>{{ t('accessKeys.drawer.groups') }}</legend>
          <label class="access-key-drawer__field">
            <span>{{ t('accessKeys.drawer.scopeMode') }}</span>
            <select
              data-test="access-key-groups-mode"
              :value="draft.scopeModes.groups"
              :disabled="formLocked || groupCatalogState !== 'ready'"
              @change="setScopeMode('groups', $event)"
            >
              <option value="all">{{ t('accessKeys.drawer.scopeAll') }}</option>
              <option value="restricted">{{ t('accessKeys.drawer.scopeRestricted') }}</option>
            </select>
          </label>
          <label v-for="group in groupOptions" :key="group.id" class="access-key-drawer__check">
            <input
              type="checkbox"
              :checked="draft.filters.groups.includes(group.id)"
              :disabled="
                formLocked ||
                draft.scopeModes.groups !== 'restricted' ||
                groupCatalogState === 'loading' ||
                groupCatalogState === 'error' ||
                (groupCatalogState === 'stale' && !base?.filters.groups.includes(group.id))
              "
              @change="toggleGroup(group.id, ($event.target as HTMLInputElement).checked)"
            />
            <span>{{ group.label }}</span>
          </label>
        </fieldset>

        <fieldset>
          <legend>{{ t('accessKeys.drawer.protocols') }}</legend>
          <label class="access-key-drawer__field">
            <span>{{ t('accessKeys.drawer.scopeMode') }}</span>
            <select
              data-test="access-key-protocols-mode"
              :value="draft.scopeModes.protocols"
              :disabled="
                formLocked || groupCatalogState === 'loading' || groupCatalogState === 'error'
              "
              @change="setScopeMode('protocols', $event)"
            >
              <option value="all">{{ t('accessKeys.drawer.scopeAll') }}</option>
              <option value="restricted">{{ t('accessKeys.drawer.scopeRestricted') }}</option>
            </select>
          </label>
          <label
            v-for="protocol in protocolOptions"
            :key="protocol"
            class="access-key-drawer__check"
          >
            <input
              type="checkbox"
              :checked="draft.filters.protocols.includes(protocol)"
              :disabled="
                formLocked ||
                draft.scopeModes.protocols !== 'restricted' ||
                groupCatalogState === 'loading' ||
                groupCatalogState === 'error'
              "
              @change="toggleProtocol(protocol, ($event.target as HTMLInputElement).checked)"
            />
            <span class="access-key-drawer__check-content">
              <span>{{ t(`common.protocols.${protocol}`) }}</span>
              <small v-if="protocol === 'openai-response'">{{
                t('accessKeys.drawer.reservedProtocolHint')
              }}</small>
            </span>
          </label>
        </fieldset>

        <fieldset>
          <legend>{{ t('accessKeys.drawer.models') }}</legend>
          <label class="access-key-drawer__field">
            <span>{{ t('accessKeys.drawer.scopeMode') }}</span>
            <select
              data-test="access-key-models-mode"
              :value="draft.scopeModes.models"
              :disabled="
                formLocked || groupCatalogState === 'loading' || groupCatalogState === 'error'
              "
              @change="setScopeMode('models', $event)"
            >
              <option value="all">{{ t('accessKeys.drawer.scopeAll') }}</option>
              <option value="restricted">{{ t('accessKeys.drawer.scopeRestricted') }}</option>
            </select>
          </label>
          <p>{{ t('accessKeys.drawer.modelsDescription') }}</p>
          <div class="access-key-drawer__model-entry">
            <input
              v-model="modelInput"
              data-test="access-key-model-input"
              type="text"
              list="access-key-model-options"
              autocomplete="off"
              :placeholder="t('accessKeys.drawer.modelPlaceholder')"
              :disabled="
                formLocked ||
                draft.scopeModes.models !== 'restricted' ||
                groupCatalogState === 'loading' ||
                groupCatalogState === 'error'
              "
              @keydown.enter.prevent="addModel"
            />
            <datalist id="access-key-model-options">
              <option v-for="model in modelOptions" :key="model" :value="model" />
            </datalist>
            <AppButton
              variant="secondary"
              :disabled="
                formLocked ||
                !modelInput.trim() ||
                draft.scopeModes.models !== 'restricted' ||
                groupCatalogState === 'loading' ||
                groupCatalogState === 'error'
              "
              @click="addModel"
            >
              <Plus :size="16" aria-hidden="true" />{{ t('accessKeys.drawer.addModel') }}
            </AppButton>
          </div>
          <div v-if="draft.filters.models.length" class="access-key-drawer__models">
            <span
              v-for="model in draft.filters.models"
              :key="model"
              class="access-key-drawer__model"
            >
              <code>{{ model }}</code>
              <button
                type="button"
                :aria-label="t('accessKeys.drawer.removeModel', { model })"
                :disabled="
                  formLocked || groupCatalogState === 'loading' || groupCatalogState === 'error'
                "
                @click="removeModel(model)"
              >
                <X :size="15" aria-hidden="true" />
              </button>
            </span>
          </div>
        </fieldset>

        <label class="access-key-drawer__field" for="access-key-rpm">
          <span>{{ t('accessKeys.drawer.rpm') }}</span>
          <input
            id="access-key-rpm"
            data-test="access-key-rpm"
            type="number"
            min="0"
            step="1"
            :value="draft.rpm_limit"
            :disabled="formLocked"
            @input="setRPM"
          />
          <small>{{ t('accessKeys.drawer.rpmDescription') }}</small>
        </label>
      </template>

      <section v-if="result" class="access-key-drawer__result" aria-live="polite">
        <div class="access-key-drawer__result-title">
          <KeyRound :size="18" aria-hidden="true" />
          <strong>{{ t('accessKeys.drawer.resultTitle') }}</strong>
        </div>
        <p>{{ t('accessKeys.drawer.resultDescription') }}</p>
        <div class="access-key-drawer__secret">
          <template v-if="resultSecret">
            <SecretValue
              :value="resultSecret"
              :reveal-label="t('common.reveal')"
              :conceal-label="t('common.conceal')"
              button-test="access-key-result-reveal"
              @conceal="ephemeralSecret.clear"
            />
            <CopyButton
              :value="resultSecret"
              :label="t('accessKeys.copy')"
              :success-label="t('common.copied')"
              :failure-label="t('common.copyFailed')"
            />
          </template>
          <AppButton
            v-else
            data-test="access-key-result-reveal"
            variant="secondary"
            :busy="revealPending"
            @click="revealResultSecret"
          >
            {{ t('common.reveal') }}
          </AppButton>
        </div>
      </section>

      <div class="access-key-drawer__actions">
        <AppButton variant="secondary" :disabled="closeBlocked" @click="setOpen(false)">
          {{ t(createCompleted ? 'common.close' : 'common.cancel') }}
        </AppButton>
        <AppButton
          v-if="!createCompleted"
          data-test="access-key-save"
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
.access-key-drawer__field,
fieldset {
  display: grid;
  gap: var(--space-2);
}
.access-key-drawer__field > span,
legend {
  font-weight: 700;
}
input,
select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
fieldset {
  min-width: 0;
  margin: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
fieldset p,
small,
.access-key-drawer__result p {
  margin: 0;
  color: var(--color-text-muted);
}
.access-key-drawer__check {
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
}
.access-key-drawer__check input {
  width: 20px;
  min-height: 20px;
}
.access-key-drawer__check-content {
  display: grid;
  gap: var(--space-1);
}
.access-key-drawer__model-entry,
.access-key-drawer__secret,
.access-key-drawer__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.access-key-drawer__model-entry input {
  flex: 1 1 220px;
}
.access-key-drawer__models {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.access-key-drawer__model {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-1);
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding-left: var(--space-2);
}
.access-key-drawer__model button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}
.access-key-drawer__result {
  display: grid;
  gap: var(--space-2);
  border: 1px solid var(--color-success);
  border-radius: var(--radius-control);
  background: var(--color-success-bg);
  padding: var(--space-3);
}
.access-key-drawer__result-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-success);
}
.access-key-drawer__actions {
  justify-content: flex-end;
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-4);
}
</style>
