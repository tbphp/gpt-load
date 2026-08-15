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
import { channelsQueryOptions, type ChannelDto } from '@/app/resources/channels'
import { connectGroupCredentials } from '@/app/resources/credential-stages'
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
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import StickySaveBar from '@/components/ui/StickySaveBar.vue'
import ModelAliasEditor from '@/features/models/ModelAliasEditor.vue'
import ModelDiscoveryDrawer from '@/features/models/ModelDiscoveryDrawer.vue'
import { presentSubscriptionErrorKey } from '@/features/subscription-error-presenter'
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
import SubscriptionCredentialStager from './SubscriptionCredentialStager.vue'
import type { ImportDraft, ModelDraftItem } from './model-draft'
import { createDiscoveredModelDraft, toGroupModels } from './model-draft'
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
const connectOperation = importOperationOwner.connectCredentials
const routeState = computed(() => parseImportRouteQuery(route.query))

function freshDraft(): ImportDraft {
  return {
    mode: 'new',
    channel_id: '',
    connection_type: 'api_key',
    params: {},
    name: '',
    credentials: '',
    staged_credentials: [],
    models: [],
  }
}

function cloneDraft(source: ImportDraft): ImportDraft {
  return {
    ...source,
    params: { ...source.params },
    staged_credentials: source.staged_credentials.map((stage) => ({
      stage_id: stage.stage_id,
      status: stage.status,
      ...(stage.authorization_url === undefined
        ? {}
        : { authorization_url: stage.authorization_url }),
      account: { ...stage.account },
      expires_at_ms: stage.expires_at_ms,
      ...(stage.error_code === undefined ? {} : { error_code: stage.error_code }),
    })),
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
  connectOperation.operation.value?.payload.draft ??
  (appendOperation.operation.value?.payload.draft.mode === 'new'
    ? appendOperation.operation.value.payload.draft
    : null)
const draft = reactive<ImportDraft>(
  cloneDraft(operationDraft ?? props.initialDraft ?? defaultDraft),
)
const baseUrlOverrideEnabled = ref(Boolean(draft.params.base_url?.trim()))
const paramTouched = reactive<Record<string, boolean>>({})
const visibleModelInvalidIndexes = ref<Set<number>>(new Set())
const revealAllModelErrors = ref(false)
let nextModelKey = Math.max(0, ...draft.models.map(({ key }) => key)) + 1
const discoveryCandidates = ref<ModelCandidate[]>([])
const shouldApplyDefaultChannel = ref(!operationDraft && !props.initialDraft)
const allChannelsQuery = useQuery(channelsQueryOptions(api, ''))
const allChannels = computed(() => allChannelsQuery.data.value?.items ?? [])
const selectedChannelCache = ref<ChannelDto | null>(null)
const selectedChannel = computed<ChannelDto | null>(
  () =>
    allChannels.value.find(({ channel_id }) => channel_id === draft.channel_id) ??
    (selectedChannelCache.value?.channel_id === draft.channel_id
      ? selectedChannelCache.value
      : null),
)
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
if (connectOperation.outcome.value?.kind === 'confirmed') connectOperation.reset()
const mutationPending = computed(
  () =>
    createOperation.pending.value ||
    appendOperation.pending.value ||
    connectOperation.pending.value,
)
const payloadLocked = computed(
  () =>
    createOperation.operation.value !== null ||
    appendOperation.operation.value !== null ||
    connectOperation.operation.value !== null,
)
const activeMutation = computed(() =>
  connectOperation.operation.value
    ? connectOperation
    : appendOperation.operation.value
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
const readyStages = computed(() =>
  draft.staged_credentials.filter(({ status }) => status === 'ready'),
)

function currentReadyStages(): typeof readyStages.value {
  const now = Date.now()
  return readyStages.value.filter(({ expires_at_ms }) => expires_at_ms > now)
}

function expireStaleReadyStages(): void {
  const now = Date.now()
  draft.staged_credentials = draft.staged_credentials.map((stage) =>
    stage.status === 'ready' && stage.expires_at_ms <= now
      ? { ...stage, status: 'expired' }
      : stage,
  )
}
const credentialCount = computed(() =>
  draft.connection_type === 'subscription'
    ? readyStages.value.length
    : credentialAnalysis.value.nonEmptyCount,
)
const connectionChannel = computed<ChannelDto | null>(() => selectedChannel.value)
const isSubscription = computed(() => draft.connection_type === 'subscription')
const structuredCredentials = computed(
  () =>
    selectedChannel.value !== null &&
    (selectedChannel.value.credential_fields.length !== 1 ||
      selectedChannel.value.credential_fields[0]?.key !== 'api_key'),
)
const allParamErrors = computed<Record<string, string>>(() => {
  const errors: Record<string, string> = {}
  const channel = connectionChannel.value
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
const paramErrors = computed<Record<string, string>>(() =>
  Object.fromEntries(Object.entries(allParamErrors.value).filter(([key]) => paramTouched[key])),
)
const visibleParamError = computed(() => Object.values(paramErrors.value)[0] ?? '')
const paramsError = computed(() =>
  selectedChannel.value === null
    ? t('import.presets.channelRequired')
    : (Object.values(allParamErrors.value)[0] ?? ''),
)
const modelConflicts = computed(() =>
  serverModelConflicts.value.length
    ? serverModelConflicts.value
    : findModelNameConflicts(toGroupModels(draft.models)),
)
const modelValidity = computed(() => modelDraftValidity(draft.models, modelConflicts.value))
const hasVisibleModelErrors = computed(
  () =>
    visibleModelInvalidIndexes.value.size > 0 ||
    (revealAllModelErrors.value && modelValidity.value.invalidIndexes.size > 0),
)
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
    (hasVisibleModelErrors.value ? modelValidationSummary.value : '') ||
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
const submitBlockedReason = computed(() => {
  if (payloadLocked.value || mutationPending.value) return ''
  if (paramsError.value) {
    if (selectedChannel.value === null) return t('import.presets.channelRequired')
    return visibleParamError.value || t('import.steps.channel.incomplete')
  }
  if (credentialCount.value === 0)
    return t(
      draft.connection_type === 'subscription'
        ? 'import.subscription.required'
        : 'import.credentials.required',
    )
  if (!isSubscription.value && credentialAnalysis.value.tooManyCredentials) {
    return t('import.credentials.tooMany')
  }
  if (modelValidity.value.invalidIndexes.size) return t('import.models.resolveErrors')
  return ''
})
const canDiscover = computed(
  () =>
    !payloadLocked.value &&
    !paramsError.value &&
    credentialCount.value > 0 &&
    (draft.connection_type === 'subscription' || !credentialAnalysis.value.tooManyCredentials),
)
const canCreate = computed(
  () =>
    !payloadLocked.value &&
    !mutationPending.value &&
    !paramsError.value &&
    credentialCount.value > 0 &&
    (draft.connection_type === 'subscription' || !credentialAnalysis.value.tooManyCredentials) &&
    modelValidity.value.invalidIndexes.size === 0,
)
const currentModelIDs = computed(() => draft.models.map(({ id }) => id.trim()).filter(Boolean))
const dirty = computed(
  () => !completed.value && JSON.stringify(snapshotDraft()) !== JSON.stringify(defaultDraft),
)
const summary = computed(() => {
  const models = draft.models.length
  if (isSubscription.value) {
    return t(models ? 'import.summaryAccounts' : 'import.summaryAccountsOptional', {
      accounts: credentialCount.value,
      models,
    })
  }
  return t(models ? 'import.summary' : 'import.summaryOptional', {
    credentials: credentialCount.value,
    models,
  })
})

type ImportStepState = 'pending' | 'active' | 'ready' | 'error' | 'optional'

const connectionTypeLabel = computed(() =>
  t(
    isSubscription.value
      ? 'import.steps.channel.connectionTypes.subscription'
      : 'import.steps.channel.connectionTypes.apiKey',
  ),
)
const channelStepState = computed<ImportStepState>(() => {
  if (visibleParamError.value) return 'error'
  if (selectedChannel.value && !paramsError.value) return 'ready'
  return 'active'
})
const channelStepSummary = computed(() => {
  if (visibleParamError.value) return visibleParamError.value
  const channel = selectedChannel.value
  if (!channel) return ''
  return t('import.steps.channel.summary', {
    channel: channel.name,
    connection: connectionTypeLabel.value,
  })
})
const subscriptionStageError = computed(
  () =>
    isSubscription.value &&
    readyStages.value.length === 0 &&
    draft.staged_credentials.some(({ status }) =>
      ['failed', 'cancelled', 'expired', 'outcome_unknown'].includes(status),
    ),
)
const activeSubscriptionStage = computed(() =>
  draft.staged_credentials.find(
    ({ status }) => status === 'pending_authorization' || status === 'exchanging',
  ),
)
const credentialStepState = computed<ImportStepState>(() => {
  if (
    (!isSubscription.value && credentialAnalysis.value.tooManyCredentials) ||
    credentialValidation.value !== null ||
    subscriptionStageError.value
  ) {
    return 'error'
  }
  if (credentialCount.value > 0) return 'ready'
  return channelStepState.value === 'ready' ? 'active' : 'pending'
})
const credentialStepTitle = computed(() =>
  t(
    isSubscription.value
      ? 'import.steps.credentials.subscriptionTitle'
      : structuredCredentials.value
        ? 'import.steps.credentials.structuredTitle'
        : 'import.steps.credentials.apiKeyTitle',
  ),
)
const credentialStepDescription = computed(() =>
  t(
    isSubscription.value
      ? 'import.subscription.description'
      : structuredCredentials.value
        ? 'import.credentials.structuredDescription'
        : 'import.credentials.description',
  ),
)
const credentialStepSummary = computed(() => {
  if (!isSubscription.value && credentialAnalysis.value.tooManyCredentials) {
    return t('import.credentials.tooMany')
  }
  if (credentialValidation.value || subscriptionStageError.value) {
    return t('import.steps.credentials.needsAttention')
  }
  if (isSubscription.value && credentialCount.value > 0) {
    return t('import.subscription.readyCount', { count: credentialCount.value })
  }
  if (isSubscription.value && activeSubscriptionStage.value) {
    return t(`import.subscription.status.${activeSubscriptionStage.value.status}`)
  }
  if (credentialCount.value > 0) {
    return t(
      structuredCredentials.value
        ? 'import.steps.credentials.credentialCount'
        : 'import.steps.credentials.keyCount',
      { count: credentialCount.value },
    )
  }
  return ''
})
const modelStepState = computed<ImportStepState>(() =>
  hasVisibleModelErrors.value ? 'error' : 'optional',
)
const modelStepSummary = computed(() => {
  if (hasVisibleModelErrors.value) return t('import.models.resolveErrors')
  if (draft.models.length) return t('import.models.count', { count: draft.models.length })
  if (paramsError.value) return t('import.steps.models.availableAfterChannel')
  if (credentialCount.value === 0) {
    return t(
      isSubscription.value
        ? 'import.steps.models.availableAfterAccount'
        : 'import.steps.models.availableAfterCredentials',
    )
  }
  return t('import.steps.models.optionalSummary')
})
const resolvedGroupName = computed(
  () => draft.name.trim() || selectedChannel.value?.name || t('import.steps.create.unnamedGroup'),
)
const createStatusTitle = computed(() => {
  if (mutationPending.value) return t('import.steps.create.creating')
  if (payloadLocked.value) return t('import.steps.create.checking')
  return canCreate.value ? summary.value : t('import.steps.create.incomplete')
})
const createStatusDescription = computed(() => {
  if (mutationPending.value) return t('import.steps.create.creatingDescription')
  if (payloadLocked.value) return t('import.steps.create.checkingDescription')
  if (!canCreate.value) return submitBlockedReason.value
  return t('import.steps.create.readyDescription', { name: resolvedGroupName.value })
})
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
    createOperation.operation.value?.payload.draft ??
    connectOperation.operation.value?.payload.draft ??
    appendOperation.operation.value?.payload.draft
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

function retryChannels(): void {
  void allChannelsQuery.refetch()
}

watch(
  [
    () => draft.channel_id,
    () => draft.connection_type,
    () => JSON.stringify(draft.params),
    () => draft.credentials,
    () => readyStages.value.map(({ stage_id }) => stage_id).join(','),
  ],
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
    draft.connection_type = channel.connection.type
    draft.params = initialChannelParams(channel)
    defaultDraft.channel_id = channel.channel_id
    defaultDraft.connection_type = channel.connection.type
    defaultDraft.params = initialChannelParams(channel)
  },
  { immediate: true },
)

watch(
  selectedChannel,
  (channel) => {
    if (!channel || payloadLocked.value) return
    const connectionType = channel.connection.type
    if (draft.connection_type === connectionType) return
    draft.connection_type = connectionType
    draft.params = initialChannelParams(channel)
    baseUrlOverrideEnabled.value = false
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
    channel.param_fields
      .filter(({ required, default_value: defaultValue }) => required || defaultValue !== null)
      .map(({ key, default_value: defaultValue }) => [key, defaultValue ?? '']),
  )
}

function resetParamTouches(): void {
  for (const key of Object.keys(paramTouched)) delete paramTouched[key]
}

function touchChannelParam(key: string): void {
  paramTouched[key] = true
}

function selectChannel(channel: ChannelDto): void {
  if (payloadLocked.value) return
  cancelDefaultChannel()
  resetParamTouches()
  selectedChannelCache.value = channel
  draft.channel_id = channel.channel_id
  draft.connection_type = channel.connection.type
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
  delete paramTouched.base_url
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
  const subscriptionStage =
    draft.connection_type === 'subscription' ? currentReadyStages()[0] : undefined
  if (draft.connection_type === 'subscription' && !subscriptionStage) {
    expireStaleReadyStages()
    // 让位给 draft 变更触发的 invalidateDiscovery，否则这条提示会被它清掉。
    void nextTick(() => {
      discoveryErrorKey.value = 'common.subscriptionErrors.stageExpired'
    })
    return
  }
  const request = {
    channel_id: draft.channel_id,
    connection_type: draft.connection_type,
    params: Object.fromEntries(
      Object.entries(draft.params).map(([key, value]) => [key, value.trim()]),
    ),
    ...(draft.connection_type === 'subscription'
      ? { staged_credential_id: subscriptionStage?.stage_id }
      : { credentials: draft.credentials }),
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
    discoveryErrorKey.value =
      draft.connection_type === 'subscription'
        ? presentSubscriptionErrorKey(cause, 'common.modelDiscoveryFailed')
        : 'common.modelDiscoveryFailed'
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

async function addManualModel(): Promise<void> {
  if (payloadLocked.value) return
  await modelEditor.value?.addManual()
}

function updateVisibleModelInvalidIndexes(indexes: Set<number>): void {
  visibleModelInvalidIndexes.value = new Set(indexes)
}

async function focusFirstInvalidModel(): Promise<void> {
  revealAllModelErrors.value = true
  await nextTick()
  await modelEditor.value?.focusFirstInvalid()
}

function buildCreateBody(confirmSameTarget: boolean): GroupCreateRequest {
  const name = draft.name.trim()
  return {
    channel_id: draft.channel_id,
    connection_type: draft.connection_type,
    params: Object.fromEntries(
      Object.entries(draft.params).map(([key, value]) => [key, value.trim()]),
    ),
    ...(name ? { name } : {}),
    models: toGroupModels(draft.models),
    ...(draft.connection_type === 'subscription'
      ? { staged_credential_ids: currentReadyStages().map(({ stage_id }) => stage_id) }
      : { credentials: draft.credentials }),
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
  draft.staged_credentials = []
  recovery.clear()
  if (kind === 'create') createOperation.reset()
  else {
    appendOperation.reset()
    connectOperation.reset()
  }

  await applyInvalidationPlan(
    queryClient,
    kind === 'create'
      ? mutationInvalidationPlans.group.create
      : mutationInvalidationPlans.group.importCredentials(groupID),
  )
  if (!componentActive) return
  toast.show({
    message: t(
      isSubscription.value
        ? duplicated > 0
          ? 'import.subscription.resultDuplicated'
          : 'import.subscription.result'
        : 'import.credentials.result',
      { added, duplicated },
    ),
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
  if (
    draft.connection_type === 'subscription' &&
    readyStages.value.length > 0 &&
    currentReadyStages().length === 0
  ) {
    expireStaleReadyStages()
    // 同上：draft 变更会触发清空 errorKey 的 watcher，先让它跑完。
    await nextTick()
    await reportSubmissionError('common.subscriptionErrors.stageExpired')
    return
  }
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
      revealAllModelErrors.value = true
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
  await reportSubmissionError(
    draft.connection_type === 'subscription'
      ? presentSubscriptionErrorKey(cause, 'import.createFailed')
      : 'import.createFailed',
  )
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
  const stableDraft = current.payload.draft
  createOperation.reset()
  if (stableDraft.connection_type === 'subscription') {
    const stageIDs = current.payload.request.staged_credential_ids ?? []
    if (!importOperationOwner.beginConnectCredentials({ groupID, stageIDs }, stableDraft)) return
    await executeConnectOperation()
    if (conflict.value === displayedConflict) conflict.value = null
    return
  }
  const credentials = current.payload.request.credentials ?? ''
  if (!importOperationOwner.beginImportCredentials({ groupID, credentials }, 'new', stableDraft))
    return
  await executeAppendOperation()
  if (conflict.value === displayedConflict) conflict.value = null
}

async function executeConnectOperation(): Promise<void> {
  if (!connectOperation.operation.value) return
  errorKey.value = ''
  const outcome = await connectOperation.execute((operation, signal) =>
    connectGroupCredentials(
      api,
      operation.payload.groupID,
      operation.payload.stageIDs,
      operation.idempotencyKey,
      signal,
    ),
  )
  if (!outcome) return
  if (outcome.kind === 'confirmed') {
    await finishSuccess(
      outcome.value.group_id,
      'append',
      outcome.value.credentials_added,
      outcome.value.credentials_duplicated,
    )
    return
  }
  if (!componentActive || outcome.kind !== 'failed' || outcome.reason !== 'rejected') return
  const cause = connectOperation.lastError.value
  connectOperation.reset()
  await reportSubmissionError(presentSubscriptionErrorKey(cause, 'import.appendFailed'))
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
  if (connectOperation.operation.value) await executeConnectOperation()
  else if (appendOperation.operation.value) await executeAppendOperation()
  else if (createOperation.operation.value) await executeCreateOperation()
}

async function abandonOperation(): Promise<void> {
  if (mutationPending.value || !payloadLocked.value) return
  if (!(await unsavedChanges.confirmDiscard()) || mutationPending.value) return
  createOperation.reset()
  appendOperation.reset()
  connectOperation.reset()
  conflict.value = null
  serverModelConflicts.value = []
  credentialValidation.value = null
  errorKey.value = ''
}

function returnToEdit(): void {
  if (mutationPending.value) return
  createOperation.reset()
  appendOperation.reset()
  connectOperation.reset()
  conflict.value = null
  credentialValidation.value = null
  errorKey.value = ''
}

watch([() => draft.channel_id, () => draft.credentials], () => {
  credentialValidation.value = null
})

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

    <div class="new-group-import__steps">
      <section
        class="new-group-import__step"
        :data-state="channelStepState"
        aria-labelledby="import-channel-step-heading"
      >
        <PanelHeader
          heading-id="import-channel-step-heading"
          :step="1"
          :title="t('import.steps.channel.title')"
        >
          <template #title-suffix>
            <span class="new-group-import__requirement new-group-import__requirement--required">
              {{ t('import.required') }}
            </span>
          </template>
          <template v-if="channelStepSummary" #actions>
            <span
              class="new-group-import__step-summary"
              :class="{ 'new-group-import__step-summary--error': visibleParamError }"
            >
              <ChannelIcon
                v-if="selectedChannel && !visibleParamError"
                :icon="selectedChannel.icon"
                :mark="selectedChannel.mark"
              />
              <span>{{ channelStepSummary }}</span>
            </span>
          </template>
        </PanelHeader>

        <div class="new-group-import__step-body">
          <ChannelPresetPicker
            :model-value="draft.channel_id"
            :channels="allChannels"
            :selected-channel="selectedChannel"
            :loading="allChannelsQuery.isFetching.value"
            :error="allChannelsQuery.isError.value"
            :disabled="payloadLocked"
            hide-header
            compact
            @select="selectChannel"
            @retry="retryChannels"
          />

          <ImportConnectionSection
            :channel="connectionChannel"
            :name="draft.name"
            :params="draft.params"
            :param-errors="paramErrors"
            :base-url-override-enabled="baseUrlOverrideEnabled"
            :disabled="payloadLocked"
            @update:name="draft.name = $event"
            @update:param="setChannelParam"
            @update:base-url-override="setBaseURLOverride"
            @blur:param="touchChannelParam"
          />
        </div>
      </section>

      <section
        class="new-group-import__step"
        :data-state="credentialStepState"
        aria-labelledby="import-credentials-step-heading"
      >
        <PanelHeader
          heading-id="import-credentials-step-heading"
          :step="2"
          :title="credentialStepTitle"
          :description="credentialStepDescription"
        >
          <template #title-suffix>
            <span class="new-group-import__requirement new-group-import__requirement--required">
              {{ t('import.required') }}
            </span>
          </template>
          <template v-if="credentialStepSummary" #actions>
            <span
              class="new-group-import__step-summary"
              :class="{ 'new-group-import__step-summary--error': credentialStepState === 'error' }"
            >
              {{ credentialStepSummary }}
            </span>
          </template>
        </PanelHeader>

        <div class="new-group-import__step-body">
          <SubscriptionCredentialStager
            v-if="isSubscription"
            v-model="draft.staged_credentials"
            :channel-id="draft.channel_id"
            context="create"
            :disabled="payloadLocked"
            hide-header
            compact
          />
          <CredentialTextarea
            v-else
            v-model="draft.credentials"
            :channel="selectedChannel"
            :disabled="payloadLocked"
            hide-header
            compact
          />
        </div>
      </section>

      <section
        class="new-group-import__step"
        :data-state="modelStepState"
        aria-labelledby="import-models-heading"
      >
        <PanelHeader
          heading-id="import-models-heading"
          :step="3"
          :title="t('import.steps.models.title')"
        >
          <template #title-suffix>
            <span class="new-group-import__requirement new-group-import__requirement--optional">
              {{ t('import.optional') }}
            </span>
          </template>
          <template #actions>
            <span
              class="new-group-import__step-summary"
              :class="{ 'new-group-import__step-summary--error': hasVisibleModelErrors }"
            >
              {{ modelStepSummary }}
            </span>
          </template>
        </PanelHeader>

        <div
          id="import-models-content"
          class="new-group-import__step-body new-group-import__models-body"
        >
          <div v-if="draft.models.length === 0" class="new-group-import__models-empty">
            <span>{{ t('import.models.empty') }}</span>
            <div class="new-group-import__models-actions">
              <AppButton
                variant="secondary"
                size="sm"
                :busy="discoveryLoading"
                :disabled="!canDiscover"
                @click="requestDiscovery"
              >
                <RefreshCw :size="16" aria-hidden="true" />{{ t('import.discover') }}
              </AppButton>
              <AppButton
                variant="secondary"
                size="sm"
                :disabled="payloadLocked"
                @click="addManualModel"
              >
                <Plus :size="16" aria-hidden="true" />{{ t('import.models.add') }}
              </AppButton>
            </div>
          </div>

          <div v-if="draft.models.length > 0" class="new-group-import__models-toolbar">
            <AppButton
              variant="secondary"
              size="sm"
              :busy="discoveryLoading"
              :disabled="!canDiscover"
              @click="requestDiscovery"
            >
              <RefreshCw :size="16" aria-hidden="true" />{{ t('import.discover') }}
            </AppButton>
            <AppButton
              variant="secondary"
              size="sm"
              :disabled="payloadLocked"
              @click="addManualModel"
            >
              <Plus :size="16" aria-hidden="true" />{{ t('import.models.add') }}
            </AppButton>
          </div>

          <ModelAliasEditor
            v-show="draft.models.length > 0"
            ref="modelEditor"
            class="new-group-import__model-editor"
            :model-value="draft.models"
            :conflicts="modelConflicts"
            :labels="aliasEditorLabels"
            :create-row="createManualRow"
            :disabled="payloadLocked"
            :search="routeState.modelSearch ?? ''"
            :addable="false"
            validation-mode="blur"
            :show-all-errors="revealAllModelErrors"
            @update:model-value="updateModels"
            @update:search="setModelSearch"
            @visible-validation-change="updateVisibleModelInvalidIndexes"
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
        </div>
      </section>
    </div>

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
            @click="focusFirstInvalidModel"
          >
            {{ t('import.models.locateFirstInvalid') }}
          </AppButton>
        </span>
      </InlineFeedback>
    </div>

    <StickySaveBar
      appearance="ledger"
      always-visible
      :dirty="!canCreate && !mutationPending"
      :pending="mutationPending"
      :status="canCreate ? 'saved' : operationNoticeKey ? 'indeterminate' : 'idle'"
    >
      <template #status>
        <div>
          <strong>{{ createStatusTitle }}</strong>
          <span v-if="createStatusDescription">{{ createStatusDescription }}</span>
        </div>
      </template>
      <template #save>
        <AppButton size="sm" :busy="mutationPending" :disabled="!canCreate" @click="submitCreate">
          {{ t('import.create') }}<ArrowRight :size="16" aria-hidden="true" />
        </AppButton>
      </template>
    </StickySaveBar>

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
      :title="t(isSubscription ? 'import.conflict.titleSubscription' : 'import.conflict.title')"
      :description="
        t(
          isSubscription
            ? 'import.conflict.descriptionSubscription'
            : 'import.conflict.description',
        )
      "
      :close-label="t('import.conflict.close')"
      :dismissible="!mutationPending"
      @update:open="updateConflictDialog"
    >
      <template #body>
        <div v-if="conflict" class="new-group-import__conflict-groups">
          <div v-for="group in conflict.groups" :key="group.id">
            <div>
              <strong>#{{ group.id }} · {{ group.name }}</strong>
              <span>{{
                t(
                  isSubscription
                    ? 'import.conflict.appendHelpSubscription'
                    : 'import.conflict.appendHelp',
                )
              }}</span>
            </div>
            <AppButton
              variant="secondary"
              :disabled="mutationPending"
              @click="appendToGroup(group.id)"
            >
              {{
                t(isSubscription ? 'import.conflict.appendSubscription' : 'import.conflict.append')
              }}
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

.new-group-import__steps,
.new-group-import__step,
.new-group-import__step-body {
  min-width: 0;
}

.new-group-import__step {
  padding: var(--space-5) 0 var(--space-6);
}

.new-group-import__step + .new-group-import__step {
  border-top: 1px solid var(--color-border-subtle);
}

.new-group-import__step :deep(.panel-header) {
  min-height: 22px;
  align-items: center;
  margin-bottom: 0;
  border-bottom: 0;
  padding-bottom: 0;
}

.new-group-import__step :deep(.panel-header h2) {
  gap: var(--space-3);
}

.new-group-import__step :deep(.panel-header p) {
  margin-left: 34px;
}

.new-group-import__step :deep(.panel-header__step) {
  width: 22px;
  height: 22px;
  border: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
  color: var(--color-text-faint);
}

.new-group-import__step[data-state='active'] :deep(.panel-header__step) {
  border-color: var(--color-action);
  background: var(--color-action);
  color: var(--color-action-ink);
}

.new-group-import__step[data-state='ready'] :deep(.panel-header__step) {
  border-color: color-mix(in srgb, var(--color-success) 34%, var(--color-border-subtle));
  background: var(--color-success-bg);
  color: var(--color-success);
}

.new-group-import__step[data-state='error'] :deep(.panel-header__step) {
  border-color: color-mix(in srgb, var(--color-danger) 34%, var(--color-border-subtle));
  background: var(--color-danger-bg);
  color: var(--color-danger);
}

.new-group-import__step-body {
  margin: var(--space-4) 0 0 34px;
}

.new-group-import__requirement {
  display: inline-flex;
  align-items: center;
  border-radius: var(--radius-tag);
  padding: 2px 8px;
  font-size: var(--text-label-xs);
  font-weight: 600;
  letter-spacing: 0.01em;
}

.new-group-import__requirement--required {
  background: var(--color-action-soft);
  color: var(--color-action);
}

.new-group-import__requirement--optional {
  background: var(--color-neutral-bg);
  color: var(--color-neutral);
}

.new-group-import__step-summary {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.new-group-import__step-summary > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.new-group-import__step-summary--error {
  color: var(--color-danger);
}

.new-group-import__models-body {
  display: grid;
  gap: var(--space-3);
}

.new-group-import__models-empty {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
  border: 1px dashed var(--color-border-control);
  border-radius: var(--radius-control);
  padding: 14px var(--space-4);
  color: var(--color-text-faint);
  font-size: var(--text-meta);
}

.new-group-import__models-empty > span {
  min-width: 0;
  flex: 1 1 240px;
}

.new-group-import__models-actions,
.new-group-import__models-toolbar {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
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

.new-group-import__error {
  margin-top: var(--space-4);
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

.new-group-import :deep(.sticky-save-bar--ledger) {
  margin-top: var(--space-5);
}

.new-group-import :deep(.sticky-save-bar--ledger .sticky-save-bar__actions) {
  flex: none;
}

@media (max-width: 860px) {
  .new-group-import__step :deep(.panel-header__actions) {
    display: flex;
    width: 100%;
    align-items: center;
    justify-content: flex-start;
  }

  .new-group-import__step-summary {
    flex: 1 1 auto;
  }

  .new-group-import__step-body,
  .new-group-import__step :deep(.panel-header p) {
    margin-left: 0;
  }

  .new-group-import__models-actions :deep(.app-button),
  .new-group-import__models-toolbar :deep(.app-button) {
    min-height: var(--touch-target);
  }

  .new-group-import :deep(.sticky-save-bar--ledger .sticky-save-bar__actions) {
    display: grid;
    width: 100%;
    grid-template-columns: minmax(0, 1fr);
  }

  .new-group-import :deep(.sticky-save-bar--ledger .sticky-save-bar__actions .app-button) {
    width: 100%;
  }
}

@media (max-width: 640px) {
  .new-group-import__models-actions,
  .new-group-import__models-toolbar {
    width: 100%;
  }

  .new-group-import__models-actions :deep(.app-button),
  .new-group-import__models-toolbar :deep(.app-button) {
    flex: 1 1 160px;
  }

  .new-group-import__conflict-groups > div {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
