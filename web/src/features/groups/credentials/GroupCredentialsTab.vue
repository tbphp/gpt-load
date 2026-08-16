<script setup lang="ts">
import { KeyRound, Plus, Search } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { ApiError } from '@/api/errors'
import type {
  CredentialCollectionDto,
  CredentialCollectionFilters,
  CredentialItemDto,
  CredentialStatus,
  CredentialSummaryDto,
  GroupSummaryDto,
} from '@/api/control/types'
import { useCollectionLoading } from '@/app/loading-state'
import { channelsQueryOptions, type ChannelCapabilitiesDto } from '@/app/resources/channels'
import {
  batchCredentials,
  cacheCredentialBatch,
  cacheCredentialItem,
  consumeCredentialResetCredit,
  credentialCollectionQueryOptions,
  getCredentialDetail,
  revealCredential,
  restoreCredential,
  refreshCredentialObservation,
  updateCredential,
} from '@/app/resources/credentials'
import {
  connectGroupCredentials,
  reauthorizeGroupCredential,
  type CredentialStage,
} from '@/app/resources/credential-stages'
import { groupDetailLocation, importLocation } from '@/app/route-locations'
import { controlQueryKeys } from '@/app/query-keys'
import { useToast } from '@/app/toast'
import { useAbortControllerPool } from '@/app/use-abort-controller-pool'
import { useDebouncedAction } from '@/app/use-debounced-action'
import CollectionFilterBar from '@/components/collection/CollectionFilterBar.vue'
import CollectionStatusSummary from '@/components/collection/CollectionStatusSummary.vue'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import SubscriptionCredentialStager from '@/features/import/SubscriptionCredentialStager.vue'
import { presentSubscriptionErrorKey } from '@/features/subscription-error-presenter'

import CredentialBatchBar from './GroupCredentialBatchBar.vue'
import CredentialRecord from './GroupCredentialRecord.vue'
import SubscriptionAccountCard from './SubscriptionAccountCard.vue'
import {
  constrainCredentialSearch,
  isCanonicalCredentialRouteQuery,
  parseCredentialRouteQuery,
  parseCredentialRouteState,
  serializeCredentialRouteQuery,
  type CredentialRouteState,
} from '../group-route'

const props = defineProps<{
  groupId: number
  channelId: string
  connectionType: 'api_key' | 'subscription'
}>()
const client = useApiClient()
const queryClient = useQueryClient()
const route = useRoute()
const router = useRouter()
const { n, t } = useI18n()
const toast = useToast()
const filters = computed(() => parseCredentialRouteQuery(route.query))
const routeState = computed(() => parseCredentialRouteState(route.query))
const credentialsQuery = useQuery(
  credentialCollectionQueryOptions(client, () => props.groupId, filters),
)
const channelsQuery = useQuery(channelsQueryOptions(client, ''))
const channelDescriptor = computed(() =>
  channelsQuery.data.value?.items.find(({ channel_id }) => channel_id === props.channelId),
)
const authorizationMethods = computed(
  () => channelDescriptor.value?.connection.authorization_methods ?? [],
)
const channelName = computed(() => channelDescriptor.value?.name ?? props.channelId)
const channelNotices = computed(() => channelDescriptor.value?.notices ?? [])
const channelCapabilities = computed<ChannelCapabilitiesDto>(
  () =>
    channelDescriptor.value?.capabilities ?? {
      model_discovery: false,
      quota_observation: false,
      credential_actions: [],
    },
)
const searchDraft = ref(filters.value.q ?? '')
const selectedIds = ref(new Set<number>())
const pendingOperations = ref(new Set<string>())
const loadedDetails = ref(new Map<number, CredentialItemDto>())
const detailErrors = ref(new Map<number, string>())
const feedback = ref('')
const deleteTarget = ref<{ ids: number[]; mask?: string } | undefined>()
const resetTarget = ref<{ item: CredentialItemDto; idempotencyKey: string } | undefined>()
const resetOperationKeys = new Map<number, string>()
const connectionWorkspaceOpen = ref(false)
const connectionStages = ref<CredentialStage[]>([])
const reauthorizationTarget = ref<CredentialItemDto | null>(null)
const connectOperationKey = ref<string>()
// 抽屉打开时列表区被遮住，连接失败的提示必须落在抽屉内部才看得见。
const connectFeedback = ref('')
const copyControllers = useAbortControllerPool()
const searchDebounce = useDebouncedAction(250)
function credentialDisplayName(item: CredentialItemDto): string {
  return item.account.email ?? item.mask
}
const connectionWorkspaceDescription = computed(() =>
  reauthorizationTarget.value
    ? t('group.credentials.subscription.reauthorizeDescription', {
        account: credentialDisplayName(reauthorizationTarget.value),
        id: reauthorizationTarget.value.credential_id,
      })
    : t('group.credentials.subscription.connectDescription'),
)

const collection = computed(() => credentialsQuery.data.value)
const {
  initial: initialLoading,
  transition: collectionTransition,
  refreshing: collectionRefreshing,
  rows: skeletonRows,
} = useCollectionLoading(
  {
    pending: () => credentialsQuery.isPending.value,
    placeholder: () => credentialsQuery.isPlaceholderData.value,
    fetching: () => credentialsQuery.isFetching.value,
    hasData: () => collection.value !== undefined,
    itemCount: () => collection.value?.items.length ?? 0,
  },
  { fallbackRows: 20 },
)
const selectedCount = computed(() => selectedIds.value.size)
const allVisibleSelected = computed(() => {
  const items = collection.value?.items ?? []
  return (
    items.length > 0 && items.every(({ credential_id }) => selectedIds.value.has(credential_id))
  )
})
const batchBusy = computed(() =>
  [...pendingOperations.value].some((key) => key.startsWith('batch:')),
)
const singleBusy = computed(() =>
  [...pendingOperations.value].some((key) => !key.startsWith('batch:')),
)
// 抽屉只关心自己那一次写入。用页面级 singleBusy 会让任意一行在忙时把抽屉
// 整个锁死，连关闭按钮都点不动。
const connectBusy = computed(() =>
  [...pendingOperations.value].some(
    (key) => key.endsWith(':connect') || key.endsWith(':reauthorize'),
  ),
)
const dialogBusy = computed(() => {
  const target = deleteTarget.value
  if (target === undefined) return false
  return target.ids.length === 1 ? pending(target.ids[0]) : batchBusy.value
})
const resetDialogBusy = computed(() =>
  resetTarget.value === undefined ? false : pending(resetTarget.value.item.credential_id),
)
const hasChangedConditions = computed(
  () => filters.value.q !== undefined || filters.value.status !== undefined,
)
const statusSummaryItems = computed(() => {
  const summary = collection.value?.summary
  if (!summary) return []

  return [
    {
      value: undefined,
      label: t('group.credentials.status.all'),
      count: summary.total,
      tone: 'neutral' as const,
    },
    {
      value: 'available',
      label: t('group.credentials.effective.available'),
      count: summary.available,
      tone: 'success' as const,
    },
    {
      value: 'cooldown',
      label: t('group.credentials.effective.cooldown'),
      count: summary.cooldown,
      tone: 'warning' as const,
    },
    {
      value: 'blacklisted',
      label: t('group.credentials.effective.blacklisted'),
      count: summary.blacklisted,
      tone: 'danger' as const,
    },
    {
      value: 'disabled',
      label: t('group.credentials.effective.disabled'),
      count: summary.disabled,
      tone: 'neutral' as const,
    },
  ]
})

watch(
  () => route.query,
  (query) => {
    searchDebounce.cancel()
    const next = parseCredentialRouteQuery(query)
    const state = parseCredentialRouteState(query)
    searchDraft.value = next.q ?? ''
    if (!isCanonicalCredentialRouteQuery(query, next, state)) {
      void router.replace(
        groupDetailLocation(props.groupId, serializeCredentialRouteQuery(next, state)),
      )
    }
  },
  { deep: true, immediate: true },
)

watch(
  () => [filters.value.status, filters.value.q, filters.value.page, filters.value.page_size],
  () => {
    selectedIds.value = new Set()
  },
)
watch(
  () => props.groupId,
  () => {
    connectionWorkspaceOpen.value = false
    connectionStages.value = []
    reauthorizationTarget.value = null
    loadedDetails.value = new Map()
    detailErrors.value = new Map()
  },
)
watch(
  () => [
    props.groupId,
    filters.value.page,
    collection.value?.items.map(({ credential_id }) => credential_id).join(','),
  ],
  () => concealCopiedCredentials(),
)
watch(
  () => ({
    totalPages: collection.value?.pagination.total_pages,
    page: filters.value.page,
    placeholder: credentialsQuery.isPlaceholderData.value,
  }),
  ({ totalPages, page, placeholder }) => {
    if (!placeholder && totalPages !== undefined && totalPages > 0 && page > totalPages)
      updateRoute({ ...filters.value, page: totalPages }, true)
  },
)

function updateRoute(
  next: CredentialCollectionFilters,
  replace = false,
  state: CredentialRouteState = routeState.value,
): void {
  const location = groupDetailLocation(props.groupId, serializeCredentialRouteQuery(next, state))
  void (replace ? router.replace(location) : router.push(location))
}

function setFilter(
  patch: Partial<Pick<CredentialCollectionFilters, 'q' | 'status' | 'page_size'>>,
): void {
  updateRoute({ ...filters.value, ...patch, page: 1 })
}

function scheduleSearch(): void {
  searchDebounce.schedule(() => {
    setFilter({ q: constrainCredentialSearch(searchDraft.value) })
  })
}

function clearSearch(): void {
  searchDebounce.cancel()
  searchDraft.value = ''
  setFilter({ q: undefined })
}

function resetFilters(): void {
  searchDebounce.cancel()
  searchDraft.value = ''
  updateRoute({ page: 1, page_size: filters.value.page_size })
}

function setStatus(value: string | undefined): void {
  setFilter({ status: value as CredentialStatus | undefined })
}

function setPage(page: number): void {
  updateRoute({ ...filters.value, page })
}
function setPageSize(pageSize: 20 | 50 | 100): void {
  setFilter({ page_size: pageSize })
}
function setExpanded(id: number, expanded: boolean): void {
  const next = new Set(routeState.value.expandedCredentialIDs)
  if (expanded) next.add(id)
  else next.delete(id)
  updateRoute(filters.value, false, { ...routeState.value, expandedCredentialIDs: [...next] })
}
function credentialExpanded(id: number): boolean {
  return routeState.value.expandedCredentialIDs.includes(id)
}
function setWeightEditor(id: number, open: boolean): void {
  updateRoute(filters.value, false, {
    ...routeState.value,
    weightCredentialID: open ? id : undefined,
  })
}
function setSelected(id: number, checked: boolean): void {
  const next = new Set(selectedIds.value)
  if (checked) next.add(id)
  else next.delete(id)
  selectedIds.value = next
}
function setAllVisible(checked: boolean): void {
  const next = new Set(selectedIds.value)
  for (const { credential_id } of collection.value?.items ?? []) {
    if (checked) next.add(credential_id)
    else next.delete(credential_id)
  }
  selectedIds.value = next
}
function operation(id: number, action: string): string {
  return `${id}:${action}`
}
function pending(id: number): boolean {
  return [...pendingOperations.value].some((value) => value.startsWith(`${id}:`))
}
function rowBusy(id: number): boolean {
  return (
    batchBusy.value ||
    [...pendingOperations.value].some(
      (value) => value.startsWith(`${id}:`) && value !== operation(id, 'usage'),
    )
  )
}
function detailBusy(id: number): boolean {
  return pendingOperations.value.has(operation(id, 'usage'))
}
function detailLoaded(id: number): boolean {
  return loadedDetails.value.has(id)
}
function detailError(id: number): string {
  return detailErrors.value.get(id) ?? ''
}
function clearDetailState(id: number): void {
  const loaded = new Map(loadedDetails.value)
  loaded.delete(id)
  loadedDetails.value = loaded
  const errors = new Map(detailErrors.value)
  errors.delete(id)
  detailErrors.value = errors
}
async function resolveCopyValue(id: number): Promise<string> {
  const controller = copyControllers.create()
  try {
    const result = await revealCredential(client, props.groupId, id, controller.signal)
    const values = Object.values(result.credential)
    return values.length === 1 ? values[0] : JSON.stringify(result.credential)
  } finally {
    copyControllers.release(controller)
  }
}
function concealCopiedCredentials(): void {
  copyControllers.abortAll()
}
function setPending(id: number | 'batch', action: string, value: boolean): void {
  const next = new Set(pendingOperations.value)
  const key = id === 'batch' ? `batch:${action}` : operation(id, action)
  if (value) next.add(key)
  else next.delete(key)
  pendingOperations.value = next
}
function cachedCurrentSummary(): CredentialSummaryDto | undefined {
  return queryClient.getQueryData<CredentialCollectionDto>(
    controlQueryKeys.groups.credentials(props.groupId, filters.value),
  )?.summary
}

function cachedCurrentCredential(id: number): CredentialItemDto | undefined {
  return queryClient
    .getQueryData<CredentialCollectionDto>(
      controlQueryKeys.groups.credentials(props.groupId, filters.value),
    )
    ?.items.find(({ credential_id }) => credential_id === id)
}

function withPreservedDetail(
  item: CredentialItemDto,
  source: CredentialItemDto,
): CredentialItemDto {
  if (item.secret_version !== source.secret_version) return item

  const preservedItem: CredentialItemDto = {
    ...item,
    ...(source.last_used_at_ms === undefined ? {} : { last_used_at_ms: source.last_used_at_ms }),
    ...(source.daily_usage === undefined ? {} : { daily_usage: source.daily_usage }),
  }
  const sourceObservation = source.observation
  const targetObservation = item.observation
  if (
    !targetObservation?.snapshot ||
    !sourceObservation?.snapshot ||
    targetObservation.observation_version !== sourceObservation.observation_version ||
    targetObservation.observed_at_ms !== sourceObservation.observed_at_ms
  ) {
    return preservedItem
  }

  const observedUsageByWindow = new Map(
    sourceObservation.snapshot.quota_windows.flatMap((window) =>
      window.observed_usage === undefined ? [] : [[window.id, window.observed_usage] as const],
    ),
  )
  if (observedUsageByWindow.size === 0) return preservedItem

  return {
    ...preservedItem,
    observation: {
      ...targetObservation,
      snapshot: {
        ...targetObservation.snapshot,
        quota_windows: targetObservation.snapshot.quota_windows.map((window) => {
          const observedUsage = observedUsageByWindow.get(window.id)
          return window.observed_usage !== undefined || observedUsage === undefined
            ? window
            : { ...window, observed_usage: observedUsage }
        }),
      },
    },
  }
}

function credentialWithDetail(item: CredentialItemDto): CredentialItemDto {
  const detail = loadedDetails.value.get(item.credential_id)
  return detail === undefined ? item : withPreservedDetail(item, detail)
}

function synchronizeGroupSummary(summary: CredentialSummaryDto | undefined): void {
  const queryKey = controlQueryKeys.groups.summary(props.groupId)
  if (summary === undefined) {
    void queryClient.invalidateQueries({ queryKey, exact: true, refetchType: 'none' })
    return
  }
  queryClient.setQueryData<GroupSummaryDto>(queryKey, (group) => {
    if (group === undefined) return group
    return {
      ...group,
      credential_count: summary.total,
      service_status:
        group.service_status === 'disabled'
          ? 'disabled'
          : summary.available > 0
            ? 'available'
            : 'unavailable',
    }
  })
}

async function invalidateReconciliationQueries(): Promise<void> {
  try {
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: controlQueryKeys.groups.credentials(props.groupId, filters.value),
        exact: true,
        refetchType: 'none',
      }),
      queryClient.invalidateQueries({
        queryKey: controlQueryKeys.groups.summary(props.groupId),
        exact: true,
        refetchType: 'none',
      }),
    ])
  } catch {
    // The reconciliation warning remains actionable even if query invalidation is unavailable.
  }
}

async function refetchActiveCredentialPage(): Promise<void> {
  await queryClient.refetchQueries(
    {
      queryKey: controlQueryKeys.groups.credentials(props.groupId, filters.value),
      exact: true,
      type: 'active',
    },
    { throwOnError: true },
  )
}

async function reconcileItem(result: CredentialItemDto, refetchActive: boolean): Promise<void> {
  try {
    const current = cachedCurrentCredential(result.credential_id)
    if (current !== undefined && current.secret_version !== result.secret_version) {
      clearDetailState(result.credential_id)
    }
    const reconciled = current === undefined ? result : withPreservedDetail(result, current)
    await cacheCredentialItem(queryClient, props.groupId, reconciled)
    if (refetchActive) {
      await refetchActiveCredentialPage()
      const refreshed = cachedCurrentCredential(result.credential_id)
      if (refreshed !== undefined) {
        await cacheCredentialItem(
          queryClient,
          props.groupId,
          withPreservedDetail(refreshed, reconciled),
        )
      }
    }
    const summary = cachedCurrentSummary()
    if (summary === undefined) {
      await invalidateReconciliationQueries()
      return
    }
    synchronizeGroupSummary(summary)
  } catch {
    feedback.value = t('group.credentials.reconcileFailed')
    await invalidateReconciliationQueries()
  }
}

async function refreshObservation(item: CredentialItemDto): Promise<void> {
  if (pending(item.credential_id)) return
  feedback.value = ''
  setPending(item.credential_id, 'observation', true)
  try {
    const observation = await refreshCredentialObservation(
      client,
      props.groupId,
      item.credential_id,
    )
    clearDetailState(item.credential_id)
    await reconcileItem({ ...item, observation }, true)
  } catch (cause) {
    feedback.value = t(
      presentSubscriptionErrorKey(cause, 'group.credentials.subscription.syncFailed'),
    )
  } finally {
    setPending(item.credential_id, 'observation', false)
  }
}

async function loadCredentialUsage(item: CredentialItemDto): Promise<void> {
  if (batchBusy.value || pending(item.credential_id) || detailLoaded(item.credential_id)) return
  const errors = new Map(detailErrors.value)
  errors.delete(item.credential_id)
  detailErrors.value = errors
  setPending(item.credential_id, 'usage', true)
  try {
    const detail = await getCredentialDetail(client, props.groupId, item.credential_id)
    await cacheCredentialItem(queryClient, props.groupId, detail.credential)
    const loaded = new Map(loadedDetails.value)
    loaded.set(item.credential_id, detail.credential)
    loadedDetails.value = loaded
  } catch {
    const nextErrors = new Map(detailErrors.value)
    nextErrors.set(item.credential_id, t('group.credentials.loadFailed'))
    detailErrors.value = nextErrors
  } finally {
    setPending(item.credential_id, 'usage', false)
  }
}

function openResetCreditDialog(item: CredentialItemDto): void {
  if (pending(item.credential_id)) return
  const idempotencyKey = resetOperationKeys.get(item.credential_id) ?? crypto.randomUUID()
  resetOperationKeys.set(item.credential_id, idempotencyKey)
  resetTarget.value = { item, idempotencyKey }
}

async function confirmResetCredit(): Promise<void> {
  const target = resetTarget.value
  if (target === undefined || pending(target.item.credential_id)) return
  feedback.value = ''
  setPending(target.item.credential_id, 'reset-credit', true)
  try {
    const result = await consumeCredentialResetCredit(
      client,
      props.groupId,
      target.item.credential_id,
      target.idempotencyKey,
    )
    if (result.observation) {
      await reconcileItem({ ...target.item, observation: result.observation }, false)
    } else {
      await refetchActiveCredentialPage()
    }
    if (result.observation_pending) {
      feedback.value = t('group.credentials.subscription.consumeResetCreditPending')
    }
    resetOperationKeys.delete(target.item.credential_id)
    resetTarget.value = undefined
  } catch (cause) {
    if (cause instanceof ApiError && cause.code !== 'RESET_CREDIT_OUTCOME_UNKNOWN') {
      resetOperationKeys.delete(target.item.credential_id)
    }
    feedback.value = t(
      presentSubscriptionErrorKey(cause, 'group.credentials.subscription.consumeResetCreditFailed'),
    )
  } finally {
    setPending(target.item.credential_id, 'reset-credit', false)
  }
}

const autoWrittenSignatures = new Set<string>()
const readyConnectionStages = computed(() =>
  connectionStages.value.filter(({ status }) => status === 'ready'),
)

// 授权就绪即写入，省掉一次多余的确认点击。同时发起多个授权时等全部落定
// 再一次性写入，避免第一个完成就把抽屉关掉；有失败的暂存时不自动写入，
// 让用户先处理那一条。
watch(
  connectionStages,
  (stages) => {
    if (!connectionWorkspaceOpen.value || connectBusy.value || stages.length === 0) return
    const now = Date.now()
    if (!stages.every(({ status, expires_at_ms }) => status === 'ready' && expires_at_ms > now)) {
      return
    }
    const signature = stages.map(({ stage_id }) => stage_id).join(',')
    if (autoWrittenSignatures.has(signature)) return
    autoWrittenSignatures.add(signature)
    void saveConnectedAccounts()
  },
  { deep: true },
)

// 抽屉自己管理焦点与滚动，这里只负责重置暂存状态。
function openConnectionWorkspace(target?: CredentialItemDto): void {
  connectionStages.value = []
  reauthorizationTarget.value = target ?? null
  connectOperationKey.value = undefined
  connectFeedback.value = ''
  autoWrittenSignatures.clear()
  connectionWorkspaceOpen.value = true
}

function setConnectionWorkspace(open: boolean): void {
  if (!open && connectBusy.value) return
  connectionWorkspaceOpen.value = open
  if (!open) {
    connectionStages.value = []
    reauthorizationTarget.value = null
    connectOperationKey.value = undefined
    connectFeedback.value = ''
    autoWrittenSignatures.clear()
  }
}

async function saveConnectedAccounts(): Promise<void> {
  const now = Date.now()
  const ready = connectionStages.value.filter(
    ({ status, expires_at_ms }) => status === 'ready' && expires_at_ms > now,
  )
  if (ready.length === 0 || connectBusy.value) {
    if (!connectBusy.value && connectionStages.value.some(({ status }) => status === 'ready')) {
      connectionStages.value = connectionStages.value.map((stage) =>
        stage.status === 'ready' && stage.expires_at_ms <= now
          ? { ...stage, status: 'expired' }
          : stage,
      )
      connectFeedback.value = t('common.subscriptionErrors.stageExpired')
    }
    return
  }
  connectFeedback.value = ''
  const target = reauthorizationTarget.value
  let succeeded = false
  setPending(target?.credential_id ?? 0, target ? 'reauthorize' : 'connect', true)
  try {
    if (target) {
      const result = await reauthorizeGroupCredential(
        client,
        props.groupId,
        target.credential_id,
        ready[0].stage_id,
        target.secret_version,
        (connectOperationKey.value ??= crypto.randomUUID()),
      )
      await reconcileItem(result, true)
      toast.show({
        message: t('group.credentials.subscription.reauthorizeSucceeded', {
          account: credentialDisplayName(target),
        }),
        tone: 'success',
      })
    } else {
      connectOperationKey.value ??= crypto.randomUUID()
      const result = await connectGroupCredentials(
        client,
        props.groupId,
        ready.map(({ stage_id }) => stage_id),
        connectOperationKey.value,
      )
      await refetchActiveCredentialPage()
      void queryClient.invalidateQueries({
        queryKey: controlQueryKeys.groups.summary(props.groupId),
        exact: true,
        refetchType: 'active',
      })
      toast.show({
        message: t(
          result.credentials_duplicated > 0
            ? 'group.credentials.subscription.connectDuplicated'
            : 'group.credentials.subscription.connectSucceeded',
          {
            added: n(result.credentials_added),
            duplicated: n(result.credentials_duplicated),
          },
        ),
        tone: result.credentials_added === 0 ? 'warning' : 'success',
        duration: 4_000,
      })
    }
    succeeded = true
  } catch (cause) {
    connectFeedback.value = t(
      presentSubscriptionErrorKey(
        cause,
        target
          ? 'group.credentials.subscription.reauthorizeFailed'
          : 'group.credentials.subscription.connectFailed',
      ),
    )
  } finally {
    setPending(target?.credential_id ?? 0, target ? 'reauthorize' : 'connect', false)
    if (succeeded) setConnectionWorkspace(false)
  }
}

async function reconcileBatch(
  action: 'enable' | 'disable' | 'delete',
  result: Awaited<ReturnType<typeof batchCredentials>>,
): Promise<void> {
  try {
    await cacheCredentialBatch(queryClient, props.groupId, action, result)
    await refetchActiveCredentialPage()
    synchronizeGroupSummary(result.summary)
  } catch {
    feedback.value = t('group.credentials.reconcileFailed')
    await invalidateReconciliationQueries()
  }
}

function clearDeletedRouteState(ids: readonly number[]): void {
  const deleted = new Set(ids)
  const next: CredentialRouteState = {
    expandedCredentialIDs: routeState.value.expandedCredentialIDs.filter((id) => !deleted.has(id)),
    weightCredentialID:
      routeState.value.weightCredentialID !== undefined &&
      deleted.has(routeState.value.weightCredentialID)
        ? undefined
        : routeState.value.weightCredentialID,
  }
  updateRoute(filters.value, true, next)
}

async function mutateItem(
  item: CredentialItemDto,
  action: 'weight' | 'toggle' | 'restore',
  value?: string,
): Promise<void> {
  if (batchBusy.value || pending(item.credential_id)) return
  feedback.value = ''
  setPending(item.credential_id, action, true)
  let result: CredentialItemDto
  try {
    result =
      action === 'restore'
        ? await restoreCredential(client, props.groupId, item.credential_id)
        : await updateCredential(
            client,
            props.groupId,
            item.credential_id,
            action === 'weight'
              ? { weight_manual: value === 'auto' ? null : Number(value) }
              : { status: item.configured_status === 'active' ? 'disabled' : 'active' },
          )
  } catch {
    feedback.value = t(
      action === 'restore' ? 'group.credentials.restoreFailed' : 'group.credentials.updateFailed',
    )
    setPending(item.credential_id, action, false)
    return
  }
  try {
    await reconcileItem(result, action !== 'weight')
  } finally {
    setPending(item.credential_id, action, false)
  }
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target || dialogBusy.value || batchBusy.value) return
  feedback.value = ''
  if (target.ids.length === 1) {
    const id = target.ids[0]
    setPending(id, 'delete', true)
    let result: Awaited<ReturnType<typeof batchCredentials>>
    try {
      result = await batchCredentials(client, props.groupId, {
        action: 'delete',
        credential_ids: [id],
      })
    } catch {
      feedback.value = t('group.credentials.deleteFailed')
      setPending(id, 'delete', false)
      return
    }
    try {
      await reconcileBatch('delete', result)
      deleteTarget.value = undefined
      selectedIds.value.delete(id)
      selectedIds.value = new Set(selectedIds.value)
      clearDeletedRouteState([id])
    } finally {
      setPending(id, 'delete', false)
    }
    return
  }
  if (await runBatch('delete', target.ids)) deleteTarget.value = undefined
}

async function runBatch(
  action: 'enable' | 'disable' | 'delete',
  ids = [...selectedIds.value],
): Promise<boolean> {
  if (ids.length === 0 || batchBusy.value || singleBusy.value) return false
  feedback.value = ''
  setPending('batch', action, true)
  let result: Awaited<ReturnType<typeof batchCredentials>>
  try {
    result = await batchCredentials(client, props.groupId, { action, credential_ids: ids })
  } catch {
    feedback.value = t('group.credentials.batch.failed')
    setPending('batch', action, false)
    return false
  }
  try {
    await reconcileBatch(action, result)
    selectedIds.value = new Set()
    if (action === 'delete') clearDeletedRouteState(ids)
    return true
  } finally {
    setPending('batch', action, false)
  }
}
</script>

<template>
  <section
    class="group-credentials"
    aria-labelledby="group-credentials-heading"
    :aria-busy="credentialsQuery.isFetching.value ? 'true' : undefined"
  >
    <PanelHeader
      heading-id="group-credentials-heading"
      :title="
        connectionType === 'subscription'
          ? t('group.credentials.subscription.title')
          : t('group.credentials.title')
      "
    >
      <template #actions>
        <AppButton
          v-if="connectionType === 'subscription' && authorizationMethods.length > 0"
          @click="openConnectionWorkspace()"
        >
          <Plus :size="16" aria-hidden="true" />{{ t('group.credentials.subscription.connect') }}
        </AppButton>
        <RouterLink
          v-else
          v-slot="{ navigate }"
          :to="importLocation({ mode: 'existing', group_id: groupId })"
          custom
        >
          <AppButton role="link" @click="navigate">
            <Plus :size="16" aria-hidden="true" />{{ t('group.credentials.add') }}
          </AppButton>
        </RouterLink>
      </template>
    </PanelHeader>

    <AppDrawer
      v-if="connectionType === 'subscription'"
      appearance="ledger"
      :open="connectionWorkspaceOpen"
      :title="
        reauthorizationTarget
          ? t('group.credentials.subscription.reauthorize')
          : t('group.credentials.subscription.connect')
      "
      :description="connectionWorkspaceDescription"
      show-description
      :close-label="t('common.close')"
      :dismissible="!connectBusy"
      @update:open="setConnectionWorkspace"
    >
      <div class="group-credentials__connect">
        <InlineFeedback v-if="connectFeedback" tone="danger" appearance="ledger">
          {{ connectFeedback }}
        </InlineFeedback>
        <SubscriptionCredentialStager
          v-model="connectionStages"
          :channel-id="channelId"
          :channel-name="channelName"
          :authorization-methods="authorizationMethods"
          :notices="channelNotices"
          compact
          hide-header
          context="connect"
          :single="reauthorizationTarget !== null"
          :disabled="connectBusy"
        />
      </div>
      <template #footer>
        <AppButton
          variant="secondary"
          :disabled="connectBusy"
          @click="setConnectionWorkspace(false)"
        >
          {{ t('group.credentials.cancel') }}
        </AppButton>
        <AppButton
          :busy="connectBusy"
          :disabled="readyConnectionStages.length === 0"
          @click="saveConnectedAccounts"
        >
          {{
            reauthorizationTarget
              ? t('group.credentials.subscription.confirmReauthorize')
              : readyConnectionStages.length > 1
                ? t('group.credentials.subscription.confirmConnectCount', {
                    count: n(readyConnectionStages.length),
                  })
                : t('group.credentials.subscription.confirmConnect')
          }}
        </AppButton>
      </template>
    </AppDrawer>

    <AsyncRefreshIndicator :active="collectionRefreshing" :label="t('group.credentials.loading')" />

    <SkeletonSurface
      v-if="credentialsQuery.isPending.value || initialLoading"
      variant="collection"
      :rows="filters.page_size"
      :columns="6"
      row-height="52px"
      mobile-row-height="190px"
      show-controls
      :concealed="!initialLoading"
      :label="t('group.credentials.loading')"
    />
    <QueryFeedback
      v-else-if="credentialsQuery.isError.value && !collection"
      state="error"
      :message="t('group.credentials.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="credentialsQuery.refetch()"
    />
    <template v-else-if="collection">
      <QueryFeedback
        v-if="credentialsQuery.isError.value"
        state="stale"
        :message="t('group.credentials.stale')"
        :retry-label="t('common.retry')"
        @retry="credentialsQuery.refetch()"
      />
      <p v-if="feedback" class="group-credentials__feedback" role="alert">{{ feedback }}</p>
      <template v-if="connectionType !== 'subscription'">
        <CollectionStatusSummary
          v-if="collection.summary.total > 0"
          :total="collection.summary.total"
          :items="statusSummaryItems"
          :model-value="filters.status"
          :label="t('group.credentials.summary.region')"
          :total-label="t('group.credentials.summary.current')"
          appearance="detail"
          @update:model-value="setStatus"
        />
        <CollectionFilterBar
          v-if="collection.summary.total > 0"
          single-column
          :label="t('group.credentials.filters.region')"
          :show-result="hasChangedConditions"
          appearance="detail"
        >
          <label class="collection-filter-field collection-filter-field--search">
            <span class="collection-filter-label">{{ t('group.credentials.filters.search') }}</span>
            <AppSearchInput
              v-model="searchDraft"
              :label="t('group.credentials.filters.search')"
              :placeholder="t('group.credentials.filters.placeholder')"
              :clear-label="t('group.credentials.filters.clear')"
              @update:model-value="scheduleSearch"
              @clear="clearSearch"
            />
          </label>
          <template #result>
            <span aria-live="polite">
              {{
                t('group.credentials.filters.result', {
                  shown: n(collection.items.length),
                  total: n(collection.pagination.total_items),
                })
              }}
            </span>
            <AppButton variant="link" size="inline" @click="resetFilters">
              {{ t('group.credentials.filters.reset') }}
            </AppButton>
          </template>
        </CollectionFilterBar>
      </template>
      <SkeletonSurface
        v-if="collectionTransition"
        variant="collection"
        :rows="skeletonRows"
        :columns="6"
        row-height="52px"
        mobile-row-height="190px"
        :label="t('group.credentials.loading')"
      />
      <EmptyState
        v-else-if="collection.summary.total === 0"
        :title="
          connectionType === 'subscription'
            ? t('group.credentials.subscription.emptyTitle')
            : t('group.credentials.emptyTitle')
        "
        :description="
          connectionType === 'subscription'
            ? t('group.credentials.subscription.emptyDescription')
            : t('group.credentials.emptyDescription')
        "
        variant="ledger"
      >
        <template #icon><KeyRound :size="20" /></template>
        <template #actions>
          <AppButton
            v-if="connectionType === 'subscription' && authorizationMethods.length > 0"
            @click="openConnectionWorkspace()"
          >
            <Plus :size="15" aria-hidden="true" />{{ t('group.credentials.subscription.connect') }}
          </AppButton>
          <RouterLink
            v-else
            v-slot="{ navigate }"
            :to="importLocation({ mode: 'existing', group_id: groupId })"
            custom
          >
            <AppButton role="link" @click="navigate">
              <Plus :size="15" aria-hidden="true" />{{ t('group.credentials.add') }}
            </AppButton>
          </RouterLink>
        </template>
      </EmptyState>
      <EmptyState
        v-else-if="collection.pagination.total_items === 0"
        :title="t('group.credentials.emptyFilterTitle')"
        :description="t('group.credentials.emptyFilterDescription')"
        variant="ledger"
      >
        <template #icon><Search :size="20" /></template>
        <template #actions>
          <AppButton variant="secondary" size="compact" @click="resetFilters">
            {{ t('group.credentials.filters.reset') }}
          </AppButton>
        </template>
      </EmptyState>
      <template v-else-if="connectionType === 'subscription'">
        <div class="group-credentials__accounts">
          <SubscriptionAccountCard
            v-for="item in collection.items"
            :key="item.credential_id"
            :item="credentialWithDetail(item)"
            :busy="rowBusy(item.credential_id)"
            :detail-busy="detailBusy(item.credential_id)"
            :detail-loaded="detailLoaded(item.credential_id)"
            :detail-error="detailError(item.credential_id)"
            :authorization-methods="authorizationMethods"
            :capabilities="channelCapabilities"
            @toggle="mutateItem($event, 'toggle')"
            @restore="mutateItem($event, 'restore')"
            @refresh="refreshObservation"
            @load-details="loadCredentialUsage"
            @reset="openResetCreditDialog"
            @reauthorize="openConnectionWorkspace"
            @remove="
              deleteTarget = {
                ids: [$event.credential_id],
                mask: $event.account.email ?? $event.mask,
              }
            "
          />
        </div>
        <PaginationBar
          v-if="collection.pagination.total_pages > 1"
          :page="collection.pagination.page"
          :page-size="collection.pagination.page_size"
          :total-items="collection.pagination.total_items"
          :total-pages="collection.pagination.total_pages"
          show-page-size
          appearance="detail"
          :pending="credentialsQuery.isFetching.value"
          @previous="setPage(filters.page - 1)"
          @next="setPage(filters.page + 1)"
          @update:page-size="setPageSize"
        />
      </template>
      <template v-else>
        <CredentialBatchBar
          v-if="selectedCount > 0"
          :selected-count="selectedCount"
          :pending="batchBusy || singleBusy"
          @enable="runBatch('enable')"
          @disable="runBatch('disable')"
          @remove="deleteTarget = { ids: [...selectedIds] }"
        />
        <LedgerRecordList
          :label="t('group.credentials.caption')"
          :row-count="collection.pagination.total_items + 1"
          grid-class="group-credential-record-grid"
        >
          <template #header>
            <span class="group-credentials__select-all" role="columnheader">
              <label>
                <span class="sr-only">{{ t('group.credentials.selectVisible') }}</span>
                <input
                  type="checkbox"
                  :checked="allVisibleSelected"
                  :disabled="batchBusy"
                  @change="setAllVisible(($event.target as HTMLInputElement).checked)"
                />
              </label>
            </span>
            <span role="columnheader">{{ t('group.credentials.columns.credential') }}</span>
            <span role="columnheader">{{ t('group.credentials.columns.status') }}</span>
            <span role="columnheader">{{ t('group.credentials.columns.weight') }}</span>
            <span role="columnheader">{{ t('group.credentials.columns.recent') }}</span>
            <span role="columnheader">{{ t('group.credentials.columns.actions') }}</span>
          </template>

          <CredentialRecord
            v-for="(item, index) in collection.items"
            :key="item.credential_id"
            :item="item"
            :row-index="
              (collection.pagination.page - 1) * collection.pagination.page_size + index + 2
            "
            :selected="selectedIds.has(item.credential_id)"
            :busy="rowBusy(item.credential_id)"
            :expanded="credentialExpanded(item.credential_id)"
            :weight-editor-open="routeState.weightCredentialID === item.credential_id"
            :resolve-copy-value="resolveCopyValue"
            @update:selected="setSelected(item.credential_id, $event)"
            @update:expanded="setExpanded(item.credential_id, $event)"
            @update:weight-editor-open="setWeightEditor(item.credential_id, $event)"
            @weight="mutateItem($event.item, 'weight', $event.value)"
            @toggle="mutateItem($event, 'toggle')"
            @restore="mutateItem($event, 'restore')"
            @remove="deleteTarget = { ids: [$event.credential_id], mask: $event.mask }"
          />
        </LedgerRecordList>
        <PaginationBar
          :page="collection.pagination.page"
          :page-size="collection.pagination.page_size"
          :total-items="collection.pagination.total_items"
          :total-pages="collection.pagination.total_pages"
          show-page-size
          appearance="detail"
          :pending="credentialsQuery.isFetching.value"
          @previous="setPage(filters.page - 1)"
          @next="setPage(filters.page + 1)"
          @update:page-size="setPageSize"
        />
      </template>
    </template>
    <AppConfirmDialog
      appearance="ledger"
      :open="resetTarget !== undefined"
      :title="t('group.credentials.subscription.consumeResetCreditTitle')"
      :description="
        t('group.credentials.subscription.consumeResetCreditDescription', {
          account: resetTarget ? credentialDisplayName(resetTarget.item) : '',
        })
      "
      :close-label="t('group.credentials.closeDialog')"
      :cancel-label="t('group.credentials.cancel')"
      :confirm-label="t('group.credentials.subscription.consumeResetCredit')"
      :pending="resetDialogBusy"
      @update:open="!$event && (resetTarget = undefined)"
      @confirm="confirmResetCredit"
    />
    <AppConfirmDialog
      appearance="ledger"
      :open="deleteTarget !== undefined"
      :title="
        deleteTarget?.ids.length === 1
          ? t('group.credentials.deleteTitle')
          : t('group.credentials.batch.deleteTitle')
      "
      :description="
        deleteTarget?.ids.length === 1
          ? t('group.credentials.deleteDescription', { mask: deleteTarget.mask })
          : t('group.credentials.batch.deleteDescription', {
              count: n(deleteTarget?.ids.length ?? 0),
            })
      "
      :close-label="t('group.credentials.closeDialog')"
      :cancel-label="t('group.credentials.cancel')"
      :confirm-label="t('group.credentials.confirmDelete')"
      tone="danger"
      :pending="dialogBusy"
      @update:open="!$event && (deleteTarget = undefined)"
      @confirm="confirmDelete"
    />
  </section>
</template>

<style scoped>
.group-credentials {
  display: grid;
  min-width: 0;
  gap: 0;
  padding-top: var(--detail-panel-padding-top);
}
.group-credentials__feedback {
  margin: 0;
  border: 1px solid var(--color-feedback-danger-border);
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  color: var(--color-text);
  padding: var(--space-3);
}
/* auto-fill 而不是 auto-fit：只有一个账号时也占一个轨道宽度，不会被拉成整行。
   容器窄于两个轨道时自动退回单列，不需要额外断点。 */
.group-credentials__accounts {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(420px, 1fr));
  align-items: start;
  gap: var(--space-3);
}
.group-credentials__connect {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4) 0;
}
.group-credential-record-grid {
  --ledger-record-list-record-min-height: 52px;
  --ledger-record-list-record-padding: 8px 0;
  --ledger-record-list-grid: 48px minmax(150px, 0.95fr) 116px minmax(118px, 0.72fr)
    minmax(150px, 0.95fr) minmax(280px, 1.7fr);
  --ledger-record-list-column-gap: 12px;
}
.group-credentials__select-all {
  display: flex;
  justify-content: center;
}
.group-credentials__select-all label {
  display: grid;
  width: 32px;
  height: 32px;
  place-items: center;
  cursor: pointer;
}
.group-credentials__select-all input {
  width: 16px;
  height: 16px;
  accent-color: var(--color-action);
}
@media (max-width: 1120px) {
  .group-credential-record-grid {
    --ledger-record-list-grid: 44px minmax(130px, 0.9fr) 108px minmax(108px, 0.7fr)
      minmax(132px, 0.9fr) minmax(250px, 1.45fr);
    --ledger-record-list-column-gap: 9px;
  }
}
@media (max-width: 1023px) and (min-width: 861px) {
  .group-credential-record-grid {
    --ledger-record-list-grid: 44px minmax(130px, 0.9fr) 108px minmax(108px, 0.7fr)
      minmax(220px, 1.35fr);
  }
  .group-credential-record-grid :deep(.ledger-record-list__header > :nth-child(5)),
  .group-credential-record-grid :deep(.group-credential-record__recent) {
    display: none;
  }
}
@media (max-width: 860px) {
  .group-credential-record-grid {
    --ledger-record-list-card-grid: minmax(0, 0.8fr) minmax(0, 1.2fr);
  }
  .group-credentials__select-all label {
    width: var(--touch-target);
    height: var(--touch-target);
  }
}
@media (max-width: 800px) {
  .group-credentials {
    padding-top: var(--detail-panel-padding-top-compact);
  }
}
</style>
