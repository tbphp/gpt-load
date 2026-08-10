<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { ArrowRight, Plus, RefreshCw } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { ApiError, InvalidResponseError, RequestCancelledError } from '@/api/errors'
import { useToast } from '@/app/toast'
import { groupDetailLocation, importLocation } from '@/app/route-locations'
import { constrainCollectionSearch } from '@/app/route-query'
import {
  channelsQueryOptions,
  normalizeChannelSearch,
  type ChannelDto,
} from '@/app/resources/channels'
import {
  createGroup,
  discoverModels,
  importGroupCredentials,
  isSameTargetConflictData,
  readCredentialValidationData,
  type CredentialValidationData,
  type GroupCreateRequest,
  type ModelDiscoveryRequest,
  type SameTargetConflictData,
} from '@/app/resources/groups'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { type ModelCandidate } from '@/app/resources/providers'
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
import { analyzeCredentials } from './credential-analysis'
import CredentialTextarea from './CredentialTextarea.vue'
import type { ImportDraft, ModelDraftItem } from './model-draft'
import { createDiscoveredModelDraft, toGroupModels } from './model-draft'
import ChannelCatalogDrawer from './ChannelCatalogDrawer.vue'
import ChannelPresetPicker from './ChannelPresetPicker.vue'
import {
  parseImportRouteQuery,
  serializeImportRouteQuery,
  type ImportDiscoveryFilter,
  type ImportPanel,
  type ImportRouteState,
} from './import-route'

const props = defineProps<{ initialDraft?: ImportDraft | null }>()
const api = useApiClient()
const queryClient = useQueryClient()
const recovery = useImportRecovery()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const toast = useToast()
const importOperationOwner = useImportOperationOwner()
const createOperation = importOperationOwner.createGroup
const appendOperation = importOperationOwner.importCredentials
const routeState = computed(() => parseImportRouteQuery(route.query))

function freshDraft(): ImportDraft {
  return {
    mode: 'new',
    channel_id: '',
    params: {},
    name: '',
    credentials: '',
    models: [],
  }
}

function cloneDraft(source: ImportDraft): ImportDraft {
  return {
    ...source,
    params: { ...source.params },
    models: source.models.map((model) => ({ ...model, sources: [...model.sources] })),
  }
}

function sameTargetConflict(cause: unknown): SameTargetConflictData | null {
  return cause instanceof ApiError &&
    cause.code === 'CHANNEL_TARGET_CONFLICT' &&
    isSameTargetConflictData(cause.data)
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
const baseUrlOverrideEnabled = ref(Boolean(draft.params.base_url?.trim()))
let nextModelKey = Math.max(0, ...draft.models.map(({ key }) => key)) + 1
const discoveryCandidates = ref<ModelCandidate[]>([])
const channelSearchInput = ref(routeState.value.channelSearch ?? '')
const channelSearchQuery = ref(normalizeChannelSearch(channelSearchInput.value))
const channelSearchDebounce = useDebouncedAction(250)
const shouldApplyDefaultChannel = ref(!operationDraft && !props.initialDraft)
const channelsQuery = useQuery(channelsQueryOptions(api, channelSearchQuery))
const allChannelsQuery = useQuery(channelsQueryOptions(api, ''))
const allChannels = computed(() => allChannelsQuery.data.value?.items ?? [])
const featuredChannels = computed(() => allChannels.value.slice(0, 3))
const selectedChannelCache = ref<ChannelDto | null>(null)
const selectedChannel = computed<ChannelDto | null>(
  () =>
    allChannels.value.find(({ channel_id }) => channel_id === draft.channel_id) ??
    channelsQuery.data.value?.items.find(({ channel_id }) => channel_id === draft.channel_id) ??
    (selectedChannelCache.value?.channel_id === draft.channel_id
      ? selectedChannelCache.value
      : null),
)
const channelCatalogLoading = computed(
  () => channelsQuery.isFetching.value || allChannelsQuery.isFetching.value,
)
const catalogDrawerOpen = computed(() => routeState.value.panel === 'channels')
const discoveryErrorKey = ref('')
const discoveryLoading = ref(false)
const discoveryDrawerOpen = computed(() => routeState.value.panel === 'discovery')
const modelEditor = ref<{
  addManual: () => Promise<void>
  focusFirstInvalid: () => Promise<void>
}>()
const errorKey = ref('')
const credentialValidation = ref<CredentialValidationData | null>(null)
const submissionError = ref<HTMLElement>()
const conflict = ref<SameTargetConflictData | null>(
  sameTargetConflict(createOperation.lastError.value),
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
let discoveryPanelRun = false
let componentActive = true

const credentialAnalysis = computed(() => analyzeCredentials(draft.credentials, draft.channel_id))
const paramErrors = computed<Record<string, string>>(() => {
  const errors: Record<string, string> = {}
  const channel = selectedChannel.value
  if (!channel) return errors
  for (const field of channel.param_fields) {
    const value = draft.params[field.key]?.trim() ?? ''
    if ((field.required || (field.key === 'base_url' && baseUrlOverrideEnabled.value)) && !value) {
      errors[field.key] = t('import.connection.paramRequired', { name: field.label })
      continue
    }
    if (field.input_kind === 'url' && value && !isValidUpstreamBaseURL(value)) {
      errors[field.key] = t('import.connection.urlError')
    }
  }
  return errors
})
const paramsError = computed(() =>
  selectedChannel.value === null
    ? t('import.presets.channelRequired')
    : (Object.values(paramErrors.value)[0] ?? ''),
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
  () =>
    modelValidationSummary.value ||
    (credentialValidation.value
      ? t('import.credentials.validation', {
          entry: credentialValidation.value.entry,
          field: credentialValidation.value.field,
          reason: t(
            `import.credentials.validationReasons.${credentialValidation.value.reason_code}`,
          ),
        })
      : errorKey.value
        ? t(errorKey.value)
        : ''),
)
const canDiscover = computed(
  () =>
    !payloadLocked.value &&
    !paramsError.value &&
    credentialAnalysis.value.nonEmptyCount > 0 &&
    !credentialAnalysis.value.tooManyCredentials,
)
const canCreate = computed(
  () =>
    !payloadLocked.value &&
    !mutationPending.value &&
    !paramsError.value &&
    credentialAnalysis.value.nonEmptyCount > 0 &&
    !credentialAnalysis.value.tooManyCredentials &&
    modelValidity.value.invalidIndexes.size === 0,
)
const currentModelIDs = computed(() => draft.models.map(({ id }) => id.trim()).filter(Boolean))
const dirty = computed(
  () => !completed.value && JSON.stringify(snapshotDraft()) !== JSON.stringify(defaultDraft),
)
const summary = computed(() =>
  t(draft.models.length ? 'import.summary' : 'import.summaryOptional', {
    credentials: credentialAnalysis.value.nonEmptyCount,
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
  pricingDiscovered: (source) => t('import.models.pricing.discovered', { source }),
  sources: {
    catalog: t('import.models.sources.catalog'),
    live: t('import.models.sources.live'),
  },
}))

const unsavedChanges = useUnsavedChanges(dirty, {
  blocked: mutationPending,
  allowRouteUpdate: (to, from) =>
    to.name === from.name &&
    parseImportRouteQuery(to.query).mode === 'new' &&
    parseImportRouteQuery(from.query).mode === 'new',
})
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

function cancelDefaultChannel(): void {
  shouldApplyDefaultChannel.value = false
}

function updateRoute(patch: Partial<ImportRouteState>, replace = false): void {
  const state: ImportRouteState = {
    ...routeState.value,
    ...patch,
    mode: 'new',
    groupID: undefined,
  }
  const location = importLocation(serializeImportRouteQuery(state))
  void (replace ? router.replace(location) : router.push(location))
}

function setPanel(panel: ImportPanel | undefined): void {
  updateRoute(
    panel === 'discovery'
      ? { panel }
      : {
          panel,
          discoverySearch: undefined,
          discoveryFilter: 'unadded',
        },
  )
}

function setModelSearch(value: string): void {
  updateRoute({ modelSearch: constrainCollectionSearch(value) }, true)
}

function setDiscoverySearch(value: string): void {
  updateRoute({ discoverySearch: constrainCollectionSearch(value) }, true)
}

function setDiscoveryFilter(value: ImportDiscoveryFilter): void {
  updateRoute({ discoveryFilter: value })
}

watch(
  () => routeState.value.channelSearch,
  (search) => {
    const value = search ?? ''
    if (value === channelSearchInput.value) return
    channelSearchInput.value = value
    channelSearchDebounce.cancel()
    channelSearchQuery.value = normalizeChannelSearch(value)
  },
)

function setChannelSearch(value: string): void {
  channelSearchInput.value = value
  cancelDefaultChannel()
  updateRoute({ channelSearch: constrainCollectionSearch(value) }, true)
  channelSearchDebounce.schedule(() => {
    channelSearchQuery.value = normalizeChannelSearch(value)
  })
}

function retryChannels(): void {
  channelSearchDebounce.cancel()
  const normalizedInput = normalizeChannelSearch(channelSearchInput.value)
  const normalizedQuery = normalizeChannelSearch(channelSearchQuery.value)
  if (normalizedQuery !== normalizedInput) {
    channelSearchQuery.value = normalizedInput
    return
  }
  void Promise.all([channelsQuery.refetch(), allChannelsQuery.refetch()])
}

watch(
  [() => draft.channel_id, () => JSON.stringify(draft.params), () => draft.credentials],
  invalidateDiscovery,
)

watch(
  allChannels,
  (channels) => {
    if (!channels.length || !shouldApplyDefaultChannel.value) return
    if (JSON.stringify(snapshotDraft()) !== JSON.stringify(defaultDraft)) {
      cancelDefaultChannel()
      return
    }
    const channel = channels[0]
    cancelDefaultChannel()
    draft.channel_id = channel.channel_id
    draft.params = initialChannelParams(channel)
    defaultDraft.channel_id = channel.channel_id
    defaultDraft.params = initialChannelParams(channel)
  },
  { immediate: true },
)

watch(
  () => JSON.stringify(snapshotDraft()),
  () => {
    errorKey.value = ''
  },
)

function initialChannelParams(channel: ChannelDto): Record<string, string> {
  return Object.fromEntries(
    channel.param_fields.filter(({ required }) => required).map(({ key }) => [key, '']),
  )
}

function selectChannel(channel: ChannelDto): void {
  if (payloadLocked.value) return
  cancelDefaultChannel()
  selectedChannelCache.value = channel
  draft.channel_id = channel.channel_id
  draft.params = initialChannelParams(channel)
  baseUrlOverrideEnabled.value = false
  setPanel(undefined)
}

function setChannelParam(key: string, value: string): void {
  cancelDefaultChannel()
  const params = { ...draft.params }
  if (key === 'base_url' && !value.trim()) delete params.base_url
  else params[key] = value
  if (key === 'base_url' && value.trim()) baseUrlOverrideEnabled.value = true
  draft.params = params
}

function setBaseURLOverride(enabled: boolean): void {
  cancelDefaultChannel()
  baseUrlOverrideEnabled.value = enabled
  if (enabled) return
  const params = { ...draft.params }
  delete params.base_url
  draft.params = params
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
  if (!discoveryDrawerOpen.value) {
    setPanel('discovery')
    return
  }
  discoveryPanelRun = true
  startDiscovery()
}

function startDiscovery(): void {
  if (!canDiscover.value || discoveryLoading.value) return
  const request = {
    channel_id: draft.channel_id,
    params: Object.fromEntries(
      Object.entries(draft.params).map(([key, value]) => [key, value.trim()]),
    ),
    credentials: draft.credentials,
  }
  cancelDiscovery()
  const controller = new AbortController()
  discoveryController = controller
  const identity = ++discoveryRequestIdentity
  discoveryCandidates.value = []
  discoveryErrorKey.value = ''
  discoveryLoading.value = true
  void runDiscovery(request, controller, identity)
}

watch(
  [discoveryDrawerOpen, canDiscover],
  ([open, discoverable]) => {
    if (!open) {
      discoveryPanelRun = false
      if (discoveryLoading.value) cancelDiscovery()
      return
    }
    if (!discoverable || discoveryPanelRun) return
    discoveryPanelRun = true
    startDiscovery()
  },
  { immediate: true },
)

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
  setPanel(undefined)
}

function addManualModel(): void {
  void modelEditor.value?.addManual()
}

function buildCreateBody(confirmSameTarget: boolean): GroupCreateRequest {
  const name = draft.name.trim()
  return {
    channel_id: draft.channel_id,
    params: Object.fromEntries(
      Object.entries(draft.params).map(([key, value]) => [key, value.trim()]),
    ),
    ...(name ? { name } : {}),
    models: toGroupModels(draft.models),
    credentials: draft.credentials,
    confirm_same_target: confirmSameTarget,
  }
}

async function finishSuccess(
  groupID: number,
  kind: 'create' | 'append',
  added: number,
  duplicated: number,
): Promise<void> {
  completed.value = true
  draft.credentials = ''
  recovery.clear()
  if (kind === 'create') createOperation.reset()
  else appendOperation.reset()

  await applyInvalidationPlan(
    queryClient,
    kind === 'create'
      ? mutationInvalidationPlans.group.create
      : mutationInvalidationPlans.group.importCredentials(groupID),
  )
  if (!componentActive) return
  toast.show({
    message: t('import.credentials.result', { added, duplicated }),
    tone: added === 0 ? 'warning' : 'success',
    duration: 4_000,
  })
  await router.push(groupDetailLocation(groupID))
}

async function reportSubmissionError(key: string): Promise<void> {
  credentialValidation.value = null
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
  credentialValidation.value = null
  const outcome = await createOperation.execute((operation, signal) =>
    createGroup(api, operation.payload.request, operation.idempotencyKey, signal),
  )
  if (!outcome) return
  if (outcome.kind === 'confirmed') {
    const targetID = outcome.value.group_id
    await finishSuccess(
      targetID,
      'create',
      outcome.value.credentials_added,
      outcome.value.credentials_duplicated,
    )
    return
  }
  if (!componentActive || outcome.kind !== 'failed' || outcome.reason !== 'rejected') return
  const cause = createOperation.lastError.value
  const targetConflict = sameTargetConflict(cause)
  if (targetConflict) {
    conflict.value = targetConflict
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
  if (cause instanceof ApiError && cause.code === 'VALIDATION_FAILED') {
    const validation = readCredentialValidationData(cause.data)
    if (validation) {
      credentialValidation.value = validation
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
        confirm_same_target: true,
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
  const credentials = current.payload.request.credentials
  const stableDraft = current.payload.draft
  createOperation.reset()
  if (!importOperationOwner.beginImportCredentials({ groupID, credentials }, 'new', stableDraft))
    return
  await executeAppendOperation()
  if (conflict.value === displayedConflict) conflict.value = null
}

async function executeAppendOperation(): Promise<void> {
  if (!appendOperation.operation.value) return
  errorKey.value = ''
  const outcome = await appendOperation.execute(async (operation, signal) => {
    const imported = await importGroupCredentials(
      api,
      operation.payload.groupID,
      { credentials: operation.payload.credentials },
      operation.idempotencyKey,
      signal,
    )
    if (imported.group_id !== operation.payload.groupID) throw new InvalidResponseError()
    return imported
  })
  if (!outcome) return
  if (outcome.kind === 'confirmed') {
    const targetID = outcome.value.group_id
    await finishSuccess(
      targetID,
      'append',
      outcome.value.credentials_added,
      outcome.value.credentials_duplicated,
    )
    return
  }
  if (!componentActive || outcome.kind !== 'failed' || outcome.reason !== 'rejected') return
  const cause = appendOperation.lastError.value
  if (cause instanceof ApiError && cause.code === 'VALIDATION_FAILED') {
    const validation = readCredentialValidationData(cause.data)
    if (validation) {
      credentialValidation.value = validation
      appendOperation.reset()
      await nextTick()
      submissionError.value?.focus()
      return
    }
  }
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
  credentialValidation.value = null
  errorKey.value = ''
}

function returnToEdit(): void {
  if (mutationPending.value) return
  createOperation.reset()
  appendOperation.reset()
  conflict.value = null
  credentialValidation.value = null
  errorKey.value = ''
}

watch(
  () => draft.credentials,
  () => {
    credentialValidation.value = null
  },
)

function updateConflictDialog(open: boolean): void {
  if (!open && conflict.value !== null) returnToEdit()
}

onBeforeUnmount(() => {
  componentActive = false
  channelSearchDebounce.cancel()
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

    <ChannelPresetPicker
      :model-value="draft.channel_id"
      :selected-channel="selectedChannel"
      :featured-channels="featuredChannels"
      :search-results="channelsQuery.data.value?.items ?? []"
      :search="channelSearchInput"
      :searching="channelsQuery.isFetching.value"
      :search-error="channelsQuery.isError.value"
      :disabled="payloadLocked"
      @select="selectChannel"
      @browse="setPanel('channels')"
      @retry="retryChannels"
      @update:search="setChannelSearch"
    />

    <ChannelCatalogDrawer
      :open="catalogDrawerOpen"
      :recent="[]"
      :channels="channelsQuery.data.value?.items ?? []"
      :search="channelSearchInput"
      :loading="channelCatalogLoading"
      :error="channelsQuery.isError.value"
      @update:open="setPanel($event ? 'channels' : undefined)"
      @update:search="setChannelSearch"
      @retry="retryChannels"
      @select="selectChannel"
    />

    <ImportConnectionSection
      :channel="selectedChannel"
      :name="draft.name"
      :params="draft.params"
      :param-errors="paramErrors"
      :base-url-override-enabled="baseUrlOverrideEnabled"
      :disabled="payloadLocked"
      @update:name="draft.name = $event"
      @update:param="setChannelParam"
      @update:base-url-override="setBaseURLOverride"
    />

    <CredentialTextarea
      v-model="draft.credentials"
      :channel="selectedChannel"
      :disabled="payloadLocked"
    />

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
        :search="routeState.modelSearch ?? ''"
        @update:model-value="updateModels"
        @update:search="setModelSearch"
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
      :search="routeState.discoverySearch ?? ''"
      :filter="routeState.discoveryFilter"
      @update:open="setPanel($event ? 'discovery' : undefined)"
      @update:search="setDiscoverySearch"
      @update:filter="setDiscoveryFilter"
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
