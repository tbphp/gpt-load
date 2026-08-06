<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { ArrowRight, Plus, RefreshCw } from '@lucide/vue'
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
  groupCollectionQueryOptions,
  importGroupKeys,
  isUpstreamUrlConflictData,
  type GroupCreateRequest,
  type ModelDiscoveryRequest,
  type UpstreamUrlConflictData,
} from '@/app/resources/groups'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import {
  normalizeProviderSearch,
  providerSuggestionsQueryOptions,
  type ModelCandidate,
  type ProviderSuggestion,
} from '@/app/resources/providers'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import { useDebouncedAction } from '@/app/use-debounced-action'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import ModelAliasEditor from '@/features/models/ModelAliasEditor.vue'
import ModelDiscoveryDrawer from '@/features/models/ModelDiscoveryDrawer.vue'
import { isValidUpstreamBaseURL } from '@/lib/upstream-base-url'
import {
  appendSelectedCandidates,
  findModelNameConflicts,
  mergeCandidateMetadata,
  modelDraftValidity,
  readModelNameConflicts,
  type ModelAliasEditorLabels,
  type ModelDiscoveryDrawerLabels,
  type ModelNameConflict,
} from '@/features/models/model-draft'
import ModelPricingStatus from '@/features/models/ModelPricingStatus.vue'

import ImportConnectionSection from './ImportConnectionSection.vue'
import ImportOperationNotice from './ImportOperationNotice.vue'
import { useImportOperationOwner } from './import-operation-owner'
import { useImportRecovery } from './import-recovery'
import { analyzeKeys } from './key-analysis'
import KeyTextarea from './KeyTextarea.vue'
import type { ImportDraft, ModelDraftItem } from './model-draft'
import { createDiscoveredModelDraft, toGroupModels } from './model-draft'
import ProviderCatalogDrawer from './ProviderCatalogDrawer.vue'
import ProviderPresetPicker from './ProviderPresetPicker.vue'
import { deriveRecentProviders, type RecentProviderEntry } from './recent-providers'

const props = defineProps<{ initialDraft?: ImportDraft | null }>()
const api = useApiClient()
const queryClient = useQueryClient()
const recovery = useImportRecovery()
const router = useRouter()
const { t } = useI18n()
const importOperationOwner = useImportOperationOwner()
const createOperation = importOperationOwner.createGroup
const appendOperation = importOperationOwner.importKeys

function freshDraft(): ImportDraft {
  return {
    mode: 'new',
    provider_id: null,
    name: '',
    upstream_url: '',
    protocols: [],
    keys: '',
    models: [],
  }
}

function cloneDraft(source: ImportDraft): ImportDraft {
  return {
    ...source,
    protocols: [...source.protocols],
    models: source.models.map((model) => ({ ...model, sources: [...model.sources] })),
  }
}

function upstreamURLConflict(cause: unknown): UpstreamUrlConflictData | null {
  return cause instanceof ApiError &&
    cause.code === 'UPSTREAM_URL_CONFLICT' &&
    isUpstreamUrlConflictData(cause.data)
    ? cause.data
    : null
}

const defaultDraft = freshDraft()
const operationDraft =
  createOperation.operation.value?.payload.draft ??
  (appendOperation.operation.value?.payload.draft.mode === 'new'
    ? appendOperation.operation.value.payload.draft
    : null)
const draft = reactive<ImportDraft>(
  cloneDraft(operationDraft ?? props.initialDraft ?? defaultDraft),
)
let nextModelKey = Math.max(0, ...draft.models.map(({ key }) => key)) + 1
const discoveryCandidates = ref<ModelCandidate[]>([])
const providerSearchInput = ref(draft.provider_id ?? '')
const providerSearchQuery = ref(normalizeProviderSearch(providerSearchInput.value))
const providerSearchDebounce = useDebouncedAction(250)
const selectedProvider = ref<ProviderSuggestion | null>(null)
const shouldApplyDefaultProvider = ref(!operationDraft && !props.initialDraft)
const providerSuggestionsQuery = useQuery(providerSuggestionsQueryOptions(api, providerSearchQuery))
const officialProviders = computed(
  () =>
    providerSuggestionsQuery.data.value?.items.filter(({ source }) => source === 'official') ?? [],
)
const catalogSuggestions = computed(
  () =>
    providerSuggestionsQuery.data.value?.items.filter(({ source }) => source !== 'official') ?? [],
)
const recentGroupsQuery = useQuery(
  groupCollectionQueryOptions(api, { sort: 'created', page: 1, page_size: 20 }),
)
const recentProviders = computed<RecentProviderEntry[]>(() =>
  deriveRecentProviders(recentGroupsQuery.data.value?.items ?? []),
)
const catalogDrawerOpen = ref(false)
const discoveryErrorKey = ref('')
const discoveryLoading = ref(false)
const discoveryDrawerOpen = ref(false)
const modelEditor = ref<{
  addManual: () => Promise<void>
  focusFirstInvalid: () => Promise<void>
}>()
const errorKey = ref('')
const submissionError = ref<HTMLElement>()
const conflict = ref<UpstreamUrlConflictData | null>(
  upstreamURLConflict(createOperation.lastError.value),
)
const serverModelConflicts = ref<ModelNameConflict[]>([])
const completed = ref(false)
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
  if (!value || !isValidUpstreamBaseURL(value)) return t('import.connection.urlError')
  return ''
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
const modelValidationSummary = computed(() =>
  [
    modelConflicts.value.length ? t('import.models.conflictSummary') : '',
    modelValidity.value.emptyIDIndexes.size ? t('import.models.manualIdRequired') : '',
    modelValidity.value.emptyAliasIndexes.size ? t('import.models.emptyAliasSummary') : '',
  ]
    .filter(Boolean)
    .join(' · '),
)
const submissionErrorMessage = computed(
  () => modelValidationSummary.value || (errorKey.value ? t(errorKey.value) : ''),
)
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
  t(draft.models.length ? 'import.summary' : 'import.summaryOptional', {
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
  searchLabel: t('import.models.searchLabel'),
  clearSearch: t('import.models.clearSearch'),
  aliasEnabledFor: (id) => t('import.models.aliasEnabledFor', { id }),
  aliasFor: (id) => t('import.models.aliasFor', { id }),
  aliasPlaceholder: t('import.models.aliasPlaceholder'),
  aliasRequired: t('import.models.aliasRequired'),
  removeFor: (id) => t('import.models.removeFor', { id }),
  manualId: t('import.models.manualId'),
  manualIdRequired: t('import.models.manualIdRequired'),
  add: t('import.models.add'),
  addInline: t('import.models.addInline'),
  count: (count) => t('import.models.count', { count }),
  empty: t('import.models.empty'),
  noMatches: t('import.models.noMatches'),
  nameConflict: (name) => t('import.models.nameConflict', { name }),
}))
const discoveryDrawerLabels = computed<ModelDiscoveryDrawerLabels>(() => ({
  title: t('import.models.drawer.title'),
  description: t('import.models.drawer.description'),
  close: t('import.models.drawer.close'),
  loading: t('import.models.drawer.loading'),
  search: t('import.models.drawer.search'),
  clearSearch: t('import.models.clearSearch'),
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
  pricingStatus: {
    pending: t('import.models.pricing.pending'),
    configured: t('import.models.pricing.configured'),
  },
  sources: {
    catalog: t('import.models.sources.catalog'),
    live: t('import.models.sources.live'),
  },
}))

const unsavedChanges = useUnsavedChanges(dirty, { blocked: mutationPending })
const unregisterRecovery = recovery.register(() => {
  if (completed.value) return null
  const stableDraft =
    createOperation.operation.value?.payload.draft ?? appendOperation.operation.value?.payload.draft
  return stableDraft?.mode === 'new' ? cloneDraft(stableDraft) : snapshotDraft()
})

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

function cancelDefaultProvider(): void {
  shouldApplyDefaultProvider.value = false
}

function setProviderSearch(value: string): void {
  providerSearchInput.value = value
  cancelDefaultProvider()
  providerSearchDebounce.schedule(() => {
    providerSearchQuery.value = normalizeProviderSearch(value)
  })
}

function retryProviderSuggestions(): void {
  providerSearchDebounce.cancel()
  const normalizedInput = normalizeProviderSearch(providerSearchInput.value)
  const normalizedQuery = normalizeProviderSearch(providerSearchQuery.value)
  if (normalizedQuery !== normalizedInput) {
    providerSearchQuery.value = normalizedInput
    return
  }
  void providerSuggestionsQuery.refetch()
}

watch(
  [
    () => draft.provider_id,
    () => draft.upstream_url,
    () => draft.keys,
    () => draft.protocols.join('\u0000'),
  ],
  invalidateDiscovery,
)

watch(
  () => providerSuggestionsQuery.data.value?.items,
  (providers) => {
    if (!providers) return
    const current = draft.provider_id
      ? providers.find(({ provider_id }) => provider_id === draft.provider_id)
      : undefined
    if (current) selectedProvider.value = current
    if (!shouldApplyDefaultProvider.value) return
    if (JSON.stringify(snapshotDraft()) !== JSON.stringify(defaultDraft)) {
      cancelDefaultProvider()
      return
    }
    const provider = providers.find(({ source }) => source === 'official')
    if (!provider) return
    cancelDefaultProvider()
    selectedProvider.value = provider
    draft.provider_id = provider.provider_id
    draft.upstream_url = provider.api_url ?? ''
    draft.protocols = [...provider.protocols]
    defaultDraft.provider_id = provider.provider_id
    defaultDraft.upstream_url = provider.api_url ?? ''
    defaultDraft.protocols = [...provider.protocols]
  },
  { immediate: true },
)

watch(
  () => JSON.stringify(snapshotDraft()),
  () => {
    errorKey.value = ''
  },
)

function selectProvider(provider: ProviderSuggestion | null): void {
  if (payloadLocked.value) return
  cancelDefaultProvider()
  selectedProvider.value = provider
  draft.provider_id = provider?.provider_id ?? null
  if (provider === null) {
    // Explicit "custom connection": start from a blank slate.
    draft.upstream_url = ''
    draft.protocols = []
    return
  }
  // Some catalog-only suggestions have no known address or protocols (see
  // catalog.SearchProviderMetadataBounded). Only overwrite fields the
  // suggestion actually provides, so picking one never wipes out an address
  // the user already filled in (or just picked from "recent").
  if (provider.api_url) draft.upstream_url = provider.api_url
  if (provider.protocols.length) draft.protocols = [...provider.protocols]
}

function selectRecentProvider(entry: RecentProviderEntry): void {
  if (payloadLocked.value) return
  cancelDefaultProvider()
  catalogDrawerOpen.value = false
  selectedProvider.value = null
  draft.provider_id = entry.providerId
  draft.upstream_url = entry.upstreamUrl
  draft.protocols = [...entry.protocols]
}

function selectSuggestionFromCatalog(provider: ProviderSuggestion): void {
  selectProvider(provider)
  catalogDrawerOpen.value = false
}

function chooseCustomFromCatalog(): void {
  selectProvider(null)
  catalogDrawerOpen.value = false
}

function setProtocols(protocols: GroupProtocol[]): void {
  cancelDefaultProvider()
  draft.protocols = protocols
}

function setUpstreamURL(value: string): void {
  cancelDefaultProvider()
  draft.upstream_url = value
}

function createManualRow(): ModelDraftItem {
  return {
    id: '',
    name: '',
    sources: [],
    pricing_status: 'pending',
    alias: '',
    alias_enabled: false,
    editable_id: true,
    key: nextModelKey++,
  }
}

function updateModels(models: ModelDraftItem[]): void {
  serverModelConflicts.value = []
  const previousByKey = new Map(draft.models.map((item) => [item.key, item] as const))
  draft.models = models.map((item) => {
    const previous = previousByKey.get(item.key)
    return previous && previous.id === item.id
      ? { ...item, sources: [...item.sources] }
      : {
          ...item,
          name: item.id,
          sources: [],
          pricing_status: 'pending',
        }
  })
}

function requestDiscovery(): void {
  if (!canDiscover.value) return
  const request = {
    provider_id: draft.provider_id,
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
  request: ModelDiscoveryRequest,
  controller: AbortController,
  identity: number,
): Promise<void> {
  try {
    const result = await discoverModels(api, request, controller.signal)
    if (discoveryRequestIdentity !== identity || discoveryController !== controller) return
    discoveryCandidates.value = result.models
    draft.models = mergeCandidateMetadata(draft.models, result.models)
  } catch (cause: unknown) {
    if (
      cause instanceof RequestCancelledError ||
      discoveryRequestIdentity !== identity ||
      discoveryController !== controller
    ) {
      return
    }
    discoveryErrorKey.value = 'common.modelDiscoveryFailed'
  } finally {
    if (discoveryRequestIdentity === identity && discoveryController === controller) {
      discoveryController = undefined
      discoveryLoading.value = false
    }
  }
}

function confirmCandidates(selectedCandidates: ModelCandidate[]): void {
  serverModelConflicts.value = []
  draft.models = appendSelectedCandidates(draft.models, selectedCandidates, (candidate) =>
    createDiscoveredModelDraft([candidate], () => nextModelKey++).at(0)!,
  )
  discoveryDrawerOpen.value = false
}

function addManualModel(): void {
  void modelEditor.value?.addManual()
}

function buildCreateBody(confirmSameURL: boolean): GroupCreateRequest {
  const name = draft.name.trim()
  return {
    provider_id: draft.provider_id,
    ...(name ? { name } : {}),
    upstream_url: draft.upstream_url.trim(),
    protocols: [...draft.protocols],
    models: toGroupModels(draft.models),
    keys: draft.keys,
    confirm_same_upstream_url: confirmSameURL,
  }
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
  if (!importOperationOwner.beginCreate(buildCreateBody(false), snapshotDraft())) return
  await executeCreateOperation()
}

async function executeCreateOperation(): Promise<void> {
  if (!createOperation.operation.value) return
  errorKey.value = ''
  const outcome = await createOperation.execute((operation, signal) =>
    createGroup(api, operation.payload.request, operation.idempotencyKey, signal),
  )
  if (!outcome) return
  if (outcome.kind === 'confirmed') {
    const targetID = outcome.value.group_id
    await finishSuccess(targetID, 'create')
    return
  }
  if (!componentActive || outcome.kind !== 'failed' || outcome.reason !== 'rejected') return
  const cause = createOperation.lastError.value
  const upstreamConflict = upstreamURLConflict(cause)
  if (upstreamConflict) {
    conflict.value = upstreamConflict
    return
  }
  if (cause instanceof ApiError && cause.code === 'MODEL_NAME_CONFLICT') {
    const conflicts = readModelNameConflicts(cause.data)
    if (conflicts.length) {
      serverModelConflicts.value = conflicts
      createOperation.reset()
      await nextTick()
      submissionError.value?.focus()
      return
    }
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
    !importOperationOwner.beginCreate(
      {
        ...payload.request,
        confirm_same_upstream_url: true,
      },
      payload.draft,
    )
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
  const keys = current.payload.request.keys
  const stableDraft = current.payload.draft
  createOperation.reset()
  if (!importOperationOwner.beginImportKeys({ groupID, keys }, 'new', stableDraft)) return
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
  providerSearchDebounce.cancel()
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
      :model-value="draft.provider_id"
      :selected-provider="selectedProvider"
      :official-providers="officialProviders"
      :disabled="payloadLocked"
      @select="selectProvider"
      @browse="catalogDrawerOpen = true"
    />

    <ProviderCatalogDrawer
      :open="catalogDrawerOpen"
      :recent="recentProviders"
      :suggestions="catalogSuggestions"
      :search="providerSearchInput"
      :loading="providerSuggestionsQuery.isFetching.value"
      :error="providerSuggestionsQuery.isError.value"
      @update:open="catalogDrawerOpen = $event"
      @update:search="setProviderSearch"
      @retry="retryProviderSuggestions"
      @select-suggestion="selectSuggestionFromCatalog"
      @select-recent="selectRecentProvider"
      @custom="chooseCustomFromCatalog"
    />

    <ImportConnectionSection
      :name="draft.name"
      :upstream-url="draft.upstream_url"
      :protocols="draft.protocols"
      :url-error="urlError"
      :protocols-error="protocolsError"
      :disabled="payloadLocked"
      @update:name="draft.name = $event"
      @update:upstream-url="setUpstreamURL"
      @update:protocols="setProtocols"
    />

    <KeyTextarea v-model="draft.keys" :disabled="payloadLocked" />

    <section class="new-group-import__models" aria-labelledby="import-models-heading">
      <PanelHeader
        heading-id="import-models-heading"
        :title="t('import.models.title')"
        :description="t('import.models.description')"
      >
        <template #actions>
          <AppButton
            variant="secondary"
            :busy="discoveryLoading"
            :disabled="!canDiscover"
            @click="requestDiscovery"
          >
            <RefreshCw :size="16" aria-hidden="true" />{{ t('import.discover') }}
          </AppButton>
          <AppButton :disabled="payloadLocked" @click="addManualModel">
            <Plus :size="16" aria-hidden="true" />{{ t('import.models.add') }}
          </AppButton>
        </template>
      </PanelHeader>

      <ModelAliasEditor
        ref="modelEditor"
        class="new-group-import__model-editor"
        :model-value="draft.models"
        :conflicts="modelConflicts"
        :labels="aliasEditorLabels"
        :create-row="createManualRow"
        :disabled="payloadLocked"
        @update:model-value="updateModels"
      >
        <template #third-column="{ item }">
          <ModelPricingStatus
            :status="item.pricing_status"
            :labels="{
              pending: t('import.models.pricing.pending'),
              configured: t('import.models.pricing.configured'),
            }"
          />
        </template>
      </ModelAliasEditor>
    </section>

    <div
      v-if="submissionErrorMessage"
      ref="submissionError"
      class="new-group-import__error"
      tabindex="-1"
    >
      <InlineFeedback tone="danger" appearance="ledger">
        <span class="new-group-import__error-content">
          <span>{{ submissionErrorMessage }}</span>
          <AppButton
            v-if="modelValidity.invalidIndexes.size"
            variant="link"
            size="inline"
            @click="modelEditor?.focusFirstInvalid()"
          >
            {{ t('import.models.locateFirstInvalid') }}
          </AppButton>
        </span>
      </InlineFeedback>
    </div>

    <footer class="new-group-import__actions">
      <div aria-live="polite">
        <strong>{{ summary }}</strong>
      </div>
      <AppButton size="sm" :busy="mutationPending" :disabled="!canCreate" @click="submitCreate">
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
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 var(--space-6);
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
  min-height: 64px;
  padding: var(--space-4) 0 0;
}

.new-group-import__error {
  margin-top: var(--space-5);
}

.new-group-import__error-content {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}

.new-group-import__error-content :deep(.app-button) {
  flex: none;
  color: inherit;
  font-weight: 600;
}

.new-group-import__actions > div {
  display: flex;
  min-width: 0;
  min-height: var(--control-sm);
  align-items: center;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.new-group-import__actions strong,
.new-group-import__actions span {
  display: block;
}

.new-group-import__actions strong {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 560;
}

@media (max-width: 860px) {
  .new-group-import__actions {
    align-items: stretch;
    flex-direction: column;
  }

  .new-group-import__actions :deep(.app-button) {
    min-height: var(--touch-target);
  }
}

@media (max-width: 640px) {
  .new-group-import__conflict-groups > div {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
