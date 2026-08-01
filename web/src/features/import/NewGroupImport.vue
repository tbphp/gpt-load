<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { ArrowRight, CloudDownload, PenLine, RefreshCw } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type { GroupProtocol } from '@/api/control/types'
import { ApiError, InvalidResponseError, RequestCancelledError } from '@/api/errors'
import { groupDetailLocation } from '@/app/route-locations'
import {
  createGroup,
  discoverModels,
  importGroupKeys,
  isUpstreamUrlConflictData,
  type GroupCreateRequest,
  type UpstreamUrlConflictData,
} from '@/app/resources/groups'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import ModelAliasEditor from '@/features/models/ModelAliasEditor.vue'
import ModelDiscoveryDrawer from '@/features/models/ModelDiscoveryDrawer.vue'
import {
  findModelNameConflicts,
  modelDraftValidity,
  type ModelAliasEditorLabels,
  type ModelDiscoveryDrawerLabels,
  type ModelNameConflict,
} from '@/features/models/model-draft'

import { findProviderPreset } from './channel-presets'
import ImportConnectionSection from './ImportConnectionSection.vue'
import ImportOperationNotice from './ImportOperationNotice.vue'
import { useImportOperationOwner } from './import-operation-owner'
import { useImportRecovery } from './import-recovery'
import { analyzeKeys } from './key-analysis'
import KeyTextarea from './KeyTextarea.vue'
import type { ImportDraft, ModelDraftItem } from './model-draft'
import { createDiscoveredModelDraft, toGroupModels } from './model-draft'
import ProviderPresetPicker from './ProviderPresetPicker.vue'

const props = defineProps<{ initialDraft?: ImportDraft | null }>()
const api = useApiClient()
const queryClient = useQueryClient()
const recovery = useImportRecovery()
const router = useRouter()
const { t } = useI18n()

function freshDraft(): ImportDraft {
  const preset = findProviderPreset('openai')!
  return {
    mode: 'new',
    preset_id: preset.id,
    name: '',
    upstream_url: preset.upstream_url,
    protocols: [...preset.protocols],
    keys: '',
    models: [],
  }
}

function cloneDraft(source: ImportDraft): ImportDraft {
  return {
    ...source,
    protocols: [...source.protocols],
    models: source.models.map((model) => ({ ...model })),
  }
}

const defaultDraft = freshDraft()
const draft = reactive<ImportDraft>(cloneDraft(props.initialDraft ?? defaultDraft))
let nextModelKey = Math.max(0, ...draft.models.map(({ key }) => key)) + 1
const discoveryCandidates = ref<string[]>([])
const discoveryErrorKey = ref('')
const discoveryLoading = ref(false)
const discoveryDrawerOpen = ref(false)
const errorKey = ref('')
const submissionError = ref<HTMLElement>()
const conflict = ref<UpstreamUrlConflictData | null>(null)
const serverModelConflicts = ref<ModelNameConflict[]>([])
const completed = ref(false)
const importOperationOwner = useImportOperationOwner()
const createOperation = importOperationOwner.createGroup
const appendOperation = importOperationOwner.importKeys
if (createOperation.outcome.value?.kind === 'confirmed') createOperation.reset()
if (appendOperation.outcome.value?.kind === 'confirmed') appendOperation.reset()
const mutationPending = computed(
  () => createOperation.pending.value || appendOperation.pending.value,
)
const payloadLocked = computed(
  () => createOperation.operation.value !== null || appendOperation.operation.value !== null,
)
const activeMutation = computed(() =>
  appendOperation.operation.value
    ? appendOperation
    : createOperation.operation.value
      ? createOperation
      : null,
)
const mutationOutcome = computed(() => activeMutation.value?.outcome.value ?? null)
const operationNoticeKey = computed(() => {
  const outcome = mutationOutcome.value
  if (!outcome) return ''
  if (outcome.kind === 'reconciling') return 'import.operation.reconciling'
  if (outcome.kind === 'indeterminate') return 'import.operation.indeterminate'
  if (outcome.kind === 'failed' && outcome.reason === 'retryable-precondition')
    return 'import.operation.waiting'
  if (outcome.kind === 'failed' && outcome.reason === 'expired-known')
    return 'import.operation.expired'
  return ''
})
const operationResourceIdentity = computed(() => {
  const outcome = mutationOutcome.value
  return outcome?.kind === 'failed' && outcome.reason === 'expired-known'
    ? outcome.resource_identity
    : ''
})
const canRetryOperation = computed(() => activeMutation.value?.canRetry.value ?? false)
let discoveryController: AbortController | undefined
let discoveryRequestIdentity = 0
let componentActive = true

const keyAnalysis = computed(() => analyzeKeys(draft.keys))
const urlError = computed(() => {
  const value = draft.upstream_url.trim()
  if (!value) return t('import.connection.urlError')
  try {
    const parsed = new URL(value)
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
      parsed.hostname !== '' &&
      !parsed.username &&
      !parsed.password &&
      !parsed.hash
      ? ''
      : t('import.connection.urlError')
  } catch {
    return t('import.connection.urlError')
  }
})
const protocolsError = computed(() =>
  draft.protocols.length ? '' : t('import.connection.protocolsError'),
)
const modelConflicts = computed(() =>
  serverModelConflicts.value.length
    ? serverModelConflicts.value
    : findModelNameConflicts(toGroupModels(draft.models)),
)
const modelValidity = computed(() => modelDraftValidity(draft.models, modelConflicts.value))
const canDiscover = computed(
  () =>
    !payloadLocked.value &&
    !urlError.value &&
    !protocolsError.value &&
    keyAnalysis.value.nonEmptyCount > 0 &&
    !keyAnalysis.value.tooManyKeys,
)
const canCreate = computed(
  () =>
    !payloadLocked.value &&
    !mutationPending.value &&
    !urlError.value &&
    !protocolsError.value &&
    keyAnalysis.value.nonEmptyCount > 0 &&
    !keyAnalysis.value.tooManyKeys &&
    modelValidity.value.invalidIndexes.size === 0,
)
const currentModelIDs = computed(() => draft.models.map(({ id }) => id.trim()).filter(Boolean))
const dirty = computed(
  () => !completed.value && JSON.stringify(snapshotDraft()) !== JSON.stringify(defaultDraft),
)
const summary = computed(() =>
  t('import.summary', {
    keys: keyAnalysis.value.nonEmptyCount,
    protocols: draft.protocols.length,
    models: draft.models.length,
  }),
)
const discoveryError = computed(() => (discoveryErrorKey.value ? t(discoveryErrorKey.value) : ''))
const aliasEditorLabels = computed<ModelAliasEditorLabels>(() => ({
  tableLabel: t('import.models.tableLabel'),
  id: t('import.models.id'),
  alias: t('import.models.alias'),
  thirdColumn: t('import.models.source'),
  actions: t('import.models.actions'),
  search: t('import.models.search'),
  clearSearch: t('import.models.clearSearch'),
  aliasEnabledFor: (id) => t('import.models.aliasEnabledFor', { id }),
  aliasFor: (id) => t('import.models.aliasFor', { id }),
  aliasPlaceholder: t('import.models.aliasPlaceholder'),
  aliasRequired: t('import.models.aliasRequired'),
  removeFor: (id) => t('import.models.removeFor', { id }),
  manualId: t('import.models.manualId'),
  add: t('import.models.add'),
  empty: t('import.models.empty'),
  noMatches: t('import.models.noMatches'),
  conflictSummary: t('import.models.conflictSummary'),
  emptyAliasSummary: t('import.models.emptyAliasSummary'),
  locateFirstInvalid: t('import.models.locateFirstInvalid'),
  nameConflict: (name) => t('import.models.nameConflict', { name }),
}))
const discoveryDrawerLabels = computed<ModelDiscoveryDrawerLabels>(() => ({
  title: t('import.models.drawer.title'),
  description: t('import.models.drawer.description'),
  close: t('import.models.drawer.close'),
  loading: t('import.models.drawer.loading'),
  notice: t('import.models.drawer.notice'),
  search: t('import.models.drawer.search'),
  filterLabel: t('import.models.drawer.filterLabel'),
  filterUnadded: t('import.models.drawer.filterUnadded'),
  filterAll: t('import.models.drawer.filterAll'),
  alreadyAdded: t('import.models.drawer.alreadyAdded'),
  unadded: t('import.models.drawer.unadded'),
  noMatches: t('import.models.drawer.noMatches'),
  empty: t('import.models.drawer.empty'),
  selected: (count) => t('import.models.drawer.selected', { count }),
  selectAll: t('import.models.drawer.selectAll'),
  deselectAll: t('import.models.drawer.deselectAll'),
  retry: t('common.retry'),
  cancel: t('common.cancel'),
  confirm: t('import.models.drawer.confirm'),
}))

const unsavedChanges = useUnsavedChanges(dirty, { blocked: mutationPending })
const unregisterRecovery = recovery.register(() => (completed.value ? null : snapshotDraft()))

function snapshotDraft(): ImportDraft {
  return cloneDraft(draft)
}

function cancelDiscovery(): void {
  discoveryRequestIdentity += 1
  discoveryController?.abort()
  discoveryController = undefined
  discoveryLoading.value = false
}

function invalidateDiscovery(): void {
  cancelDiscovery()
  discoveryCandidates.value = []
  discoveryErrorKey.value = ''
}

watch(
  [() => draft.upstream_url, () => draft.keys, () => draft.protocols.join('\u0000')],
  invalidateDiscovery,
)

function applyPreset(id: ImportDraft['preset_id']): void {
  if (payloadLocked.value) return
  draft.preset_id = id
  const preset = findProviderPreset(id)
  draft.upstream_url = preset?.upstream_url ?? ''
  draft.protocols = preset ? [...preset.protocols] : []
}

function setProtocols(protocols: GroupProtocol[]): void {
  draft.protocols = protocols
}

function createManualRow(id: string): ModelDraftItem {
  return {
    id,
    alias: '',
    alias_enabled: false,
    source: 'manual',
    key: nextModelKey++,
  }
}

function updateModels(models: ModelDraftItem[]): void {
  serverModelConflicts.value = []
  draft.models = models
}

function requestDiscovery(): void {
  if (!canDiscover.value) return
  const request = {
    upstream_url: draft.upstream_url.trim(),
    protocols: [...draft.protocols],
    keys: draft.keys,
  }
  cancelDiscovery()
  const controller = new AbortController()
  discoveryController = controller
  const identity = ++discoveryRequestIdentity
  discoveryDrawerOpen.value = true
  discoveryCandidates.value = []
  discoveryErrorKey.value = ''
  discoveryLoading.value = true
  void runDiscovery(request, controller, identity)
}

async function runDiscovery(
  request: { upstream_url: string; protocols: GroupProtocol[]; keys: string },
  controller: AbortController,
  identity: number,
): Promise<void> {
  try {
    const result = await discoverModels(api, request, controller.signal)
    if (discoveryRequestIdentity !== identity || discoveryController !== controller) return
    discoveryCandidates.value = [...new Set(result.models.map((id) => id.trim()).filter(Boolean))]
  } catch (cause: unknown) {
    if (
      cause instanceof RequestCancelledError ||
      discoveryRequestIdentity !== identity ||
      discoveryController !== controller
    ) {
      return
    }
    discoveryErrorKey.value = 'import.discoveryFailed'
  } finally {
    if (discoveryRequestIdentity === identity && discoveryController === controller) {
      discoveryController = undefined
      discoveryLoading.value = false
    }
  }
}

function confirmCandidates(selectedCandidates: string[]): void {
  const present = new Set(draft.models.map(({ id }) => id.trim()))
  const additions = createDiscoveredModelDraft(
    selectedCandidates.filter((id) => !present.has(id.trim())),
    () => nextModelKey++,
  )
  serverModelConflicts.value = []
  draft.models = [...draft.models, ...additions]
  discoveryDrawerOpen.value = false
}

function buildCreateBody(confirmSameURL: boolean): GroupCreateRequest {
  const name = draft.name.trim()
  return {
    ...(name ? { name } : {}),
    upstream_url: draft.upstream_url.trim(),
    protocols: [...draft.protocols],
    models: toGroupModels(draft.models),
    keys: draft.keys,
    confirm_same_upstream_url: confirmSameURL,
  }
}

function readServerModelConflicts(value: unknown): ModelNameConflict[] {
  if (typeof value !== 'object' || value === null || !('conflicts' in value)) return []
  const conflicts = (value as { conflicts?: unknown }).conflicts
  if (!Array.isArray(conflicts)) return []
  return conflicts.flatMap((item) => {
    if (
      typeof item !== 'object' ||
      item === null ||
      typeof (item as { client_model?: unknown }).client_model !== 'string' ||
      !Array.isArray((item as { indexes?: unknown }).indexes) ||
      !(item as { indexes: unknown[] }).indexes.every(
        (index) => typeof index === 'number' && Number.isSafeInteger(index) && index >= 0,
      )
    ) {
      return []
    }
    return [item as ModelNameConflict]
  })
}

async function finishSuccess(groupID: number, kind: 'create' | 'append'): Promise<void> {
  completed.value = true
  draft.keys = ''
  recovery.clear()
  if (kind === 'create') createOperation.reset()
  else appendOperation.reset()

  await applyInvalidationPlan(
    queryClient,
    kind === 'create'
      ? mutationInvalidationPlans.group.create
      : mutationInvalidationPlans.group.importKeys(groupID),
  )
  if (!componentActive) return
  await router.push(groupDetailLocation(groupID))
}

async function reportSubmissionError(key: string): Promise<void> {
  errorKey.value = key
  await nextTick()
  submissionError.value?.focus()
}

async function submitCreate(): Promise<void> {
  if (!canCreate.value) return
  cancelDiscovery()
  conflict.value = null
  errorKey.value = ''
  serverModelConflicts.value = []
  if (!importOperationOwner.beginCreate(buildCreateBody(false))) return
  await executeCreateOperation()
}

async function executeCreateOperation(): Promise<void> {
  if (!createOperation.operation.value) return
  errorKey.value = ''
  const outcome = await createOperation.execute((operation, signal) =>
    createGroup(api, operation.payload, operation.idempotencyKey, signal),
  )
  if (!outcome) return
  if (outcome.kind === 'confirmed') {
    const targetID = outcome.value.group_id
    await finishSuccess(targetID, 'create')
    return
  }
  if (!componentActive || outcome.kind !== 'failed' || outcome.reason !== 'rejected') return
  const cause = createOperation.lastError.value
  if (
    cause instanceof ApiError &&
    cause.code === 'UPSTREAM_URL_CONFLICT' &&
    isUpstreamUrlConflictData(cause.data)
  ) {
    conflict.value = cause.data
    return
  }
  if (cause instanceof ApiError && cause.code === 'MODEL_NAME_CONFLICT') {
    serverModelConflicts.value = readServerModelConflicts(cause.data)
  }
  createOperation.reset()
  await reportSubmissionError('import.createFailed')
}

async function submitSeparateGroup(): Promise<void> {
  const current = createOperation.operation.value
  if (!current || !conflict.value || mutationPending.value) return
  const displayedConflict = conflict.value
  const payload = structuredClone(current.payload)
  createOperation.reset()
  if (
    !importOperationOwner.beginCreate({
      ...payload,
      confirm_same_upstream_url: true,
    })
  ) {
    return
  }
  await executeCreateOperation()
  if (conflict.value === displayedConflict) conflict.value = null
}

async function appendToGroup(groupID: number): Promise<void> {
  const current = createOperation.operation.value
  if (!current || !conflict.value || mutationPending.value) return
  const displayedConflict = conflict.value
  const keys = current.payload.keys
  createOperation.reset()
  if (!importOperationOwner.beginImportKeys({ groupID, keys }, 'new')) return
  await executeAppendOperation()
  if (conflict.value === displayedConflict) conflict.value = null
}

async function executeAppendOperation(): Promise<void> {
  if (!appendOperation.operation.value) return
  errorKey.value = ''
  const outcome = await appendOperation.execute(async (operation, signal) => {
    const imported = await importGroupKeys(
      api,
      operation.payload.groupID,
      { keys: operation.payload.keys },
      operation.idempotencyKey,
      signal,
    )
    if (imported.group_id !== operation.payload.groupID) throw new InvalidResponseError()
    return imported
  })
  if (!outcome) return
  if (outcome.kind === 'confirmed') {
    const targetID = outcome.value.group_id
    await finishSuccess(targetID, 'append')
    return
  }
  if (!componentActive || outcome.kind !== 'failed' || outcome.reason !== 'rejected') return
  appendOperation.reset()
  await reportSubmissionError('import.appendFailed')
}

async function retryOperation(): Promise<void> {
  if (appendOperation.operation.value) await executeAppendOperation()
  else if (createOperation.operation.value) await executeCreateOperation()
}

async function abandonOperation(): Promise<void> {
  if (mutationPending.value || !payloadLocked.value) return
  if (!(await unsavedChanges.confirmDiscard()) || mutationPending.value) return
  createOperation.reset()
  appendOperation.reset()
  conflict.value = null
  serverModelConflicts.value = []
  errorKey.value = ''
}

function returnToEdit(): void {
  if (mutationPending.value) return
  createOperation.reset()
  appendOperation.reset()
  conflict.value = null
  errorKey.value = ''
}

function updateConflictDialog(open: boolean): void {
  if (!open && conflict.value !== null) returnToEdit()
}

onBeforeUnmount(() => {
  componentActive = false
  cancelDiscovery()
  unregisterRecovery()
})
</script>

<template>
  <div class="new-group-import">
    <ImportOperationNotice
      :message-key="operationNoticeKey"
      :resource-identity="operationResourceIdentity"
      :can-retry="canRetryOperation"
      :can-abandon="payloadLocked && !mutationPending"
      :pending="mutationPending"
      @retry="retryOperation"
      @abandon="abandonOperation"
    />

    <ProviderPresetPicker
      :model-value="draft.preset_id"
      :disabled="payloadLocked"
      @update:model-value="applyPreset"
    />

    <ImportConnectionSection
      :name="draft.name"
      :upstream-url="draft.upstream_url"
      :protocols="draft.protocols"
      :url-error="urlError"
      :protocols-error="protocolsError"
      :disabled="payloadLocked"
      @update:name="draft.name = $event"
      @update:upstream-url="draft.upstream_url = $event"
      @update:protocols="setProtocols"
    />

    <KeyTextarea v-model="draft.keys" :disabled="payloadLocked" />

    <section class="new-group-import__models" aria-labelledby="import-models-heading">
      <header class="new-group-import__models-header">
        <div>
          <h2 id="import-models-heading">{{ t('import.models.title') }}</h2>
          <p>{{ t('import.models.description') }}</p>
        </div>
        <AppButton
          variant="secondary"
          :busy="discoveryLoading"
          :disabled="!canDiscover"
          @click="requestDiscovery"
        >
          <RefreshCw :size="16" aria-hidden="true" />{{ t('import.discover') }}
        </AppButton>
      </header>

      <div class="new-group-import__models-status" aria-live="polite">
        <span>{{ t('import.models.optional') }}</span>
        <p>{{ t('import.models.optionalDescription') }}</p>
      </div>

      <ModelAliasEditor
        :model-value="draft.models"
        :conflicts="modelConflicts"
        :labels="aliasEditorLabels"
        :create-row="createManualRow"
        :disabled="payloadLocked"
        @update:model-value="updateModels"
      >
        <template #third-column="{ item }">
          <span :class="['new-group-import__source', `new-group-import__source--${item.source}`]">
            <PenLine v-if="item.source === 'manual'" :size="14" aria-hidden="true" />
            <CloudDownload v-else :size="14" aria-hidden="true" />
            {{ t(`import.models.sources.${item.source}`) }}
          </span>
        </template>
      </ModelAliasEditor>

      <InlineFeedback tone="info" appearance="hint">
        {{ t('import.models.discoveryNotice') }}
      </InlineFeedback>
    </section>

    <div v-if="errorKey" ref="submissionError" class="new-group-import__error" tabindex="-1">
      <InlineFeedback tone="danger">{{ t(errorKey) }}</InlineFeedback>
    </div>

    <footer class="new-group-import__actions">
      <div aria-live="polite">
        <strong>{{ summary }}</strong>
        <span>{{ t('import.actionHelp') }}</span>
      </div>
      <AppButton :busy="mutationPending" :disabled="!canCreate" @click="submitCreate">
        {{ t('import.create') }}<ArrowRight :size="16" aria-hidden="true" />
      </AppButton>
    </footer>

    <ModelDiscoveryDrawer
      :open="discoveryDrawerOpen"
      :candidates="discoveryCandidates"
      :current-ids="currentModelIDs"
      :loading="discoveryLoading"
      :error="discoveryError"
      :labels="discoveryDrawerLabels"
      :dismissible="!discoveryLoading"
      @update:open="discoveryDrawerOpen = $event"
      @retry="requestDiscovery"
      @confirm="confirmCandidates"
    />

    <AppDialog
      :open="conflict !== null"
      :title="t('import.conflict.title')"
      :description="t('import.conflict.description')"
      :close-label="t('import.conflict.close')"
      :dismissible="!mutationPending"
      @update:open="updateConflictDialog"
    >
      <template #body>
        <div v-if="conflict" class="new-group-import__conflict-groups">
          <div v-for="group in conflict.groups" :key="group.id">
            <div>
              <strong>#{{ group.id }} · {{ group.name }}</strong>
              <span>{{ t('import.conflict.appendHelp') }}</span>
            </div>
            <AppButton
              variant="secondary"
              :disabled="mutationPending"
              @click="appendToGroup(group.id)"
            >
              {{ t('import.conflict.append') }}
            </AppButton>
          </div>
        </div>
      </template>
      <template #footer>
        <AppButton variant="ghost" :disabled="mutationPending" @click="returnToEdit">
          {{ t('import.conflict.edit') }}
        </AppButton>
        <AppButton :busy="mutationPending" :disabled="mutationPending" @click="submitSeparateGroup">
          {{ t('import.conflict.separate') }}
        </AppButton>
      </template>
    </AppDialog>
  </div>
</template>

<style scoped>
.new-group-import {
  min-width: 0;
}

.new-group-import :deep(.operation-notice) {
  margin-top: var(--space-5);
}

.new-group-import__models {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-5) 0 var(--space-6);
}

.new-group-import__models-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}

.new-group-import__models-header h2,
.new-group-import__models-header p,
.new-group-import__models-status p {
  margin: 0;
}

.new-group-import__models-header h2 {
  font-family: var(--font-serif);
  font-size: var(--title-section);
  font-weight: 500;
}

.new-group-import__models-header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.new-group-import__models-status {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.new-group-import__models-status > span {
  flex: none;
  border-radius: var(--radius-tag);
  background: var(--color-neutral-bg);
  color: var(--color-neutral);
  padding: var(--space-1) var(--space-2);
  font-size: var(--text-label-xs);
  font-weight: 600;
}

.new-group-import__source {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.new-group-import__source--discovered {
  color: var(--color-success);
}

.new-group-import__conflict-groups > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.new-group-import__conflict-groups {
  display: grid;
  gap: var(--space-3);
}

.new-group-import__conflict-groups > div + div {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}

.new-group-import__conflict-groups > div > div {
  min-width: 0;
}

.new-group-import__conflict-groups strong,
.new-group-import__conflict-groups span {
  display: block;
}

.new-group-import__conflict-groups strong {
  overflow-wrap: anywhere;
}

.new-group-import__conflict-groups span {
  margin-top: var(--space-1);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.new-group-import__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-4) 0 0;
}

.new-group-import__error {
  margin-top: var(--space-5);
  outline: none;
}

.new-group-import__actions > div {
  min-width: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.new-group-import__actions strong,
.new-group-import__actions span {
  display: block;
}

.new-group-import__actions strong {
  color: var(--color-text);
  font-weight: 560;
}

.new-group-import__actions span {
  margin-top: var(--space-1);
}

@media (max-width: 640px) {
  .new-group-import__models-header,
  .new-group-import__actions,
  .new-group-import__conflict-groups > div {
    align-items: stretch;
    flex-direction: column;
  }

  .new-group-import__models-header :deep(.app-button),
  .new-group-import__actions :deep(.app-button) {
    min-height: var(--touch-target);
  }
}
</style>
