<script setup lang="ts">
import { KeyRound, Plus, Search } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type {
  CredentialCollectionDto,
  CredentialCollectionFilters,
  CredentialItemDto,
  CredentialStatus,
  CredentialSummaryDto,
  GroupSummaryDto,
} from '@/api/control/types'
import { useCollectionLoading } from '@/app/loading-state'
import {
  batchCredentials,
  cacheCredentialBatch,
  cacheCredentialItem,
  credentialCollectionQueryOptions,
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
  connectionType: 'api_key' | 'subscription'
}>()
const client = useApiClient()
const queryClient = useQueryClient()
const route = useRoute()
const router = useRouter()
const { n, t } = useI18n()
const filters = computed(() => parseCredentialRouteQuery(route.query))
const routeState = computed(() => parseCredentialRouteState(route.query))
const credentialsQuery = useQuery(
  credentialCollectionQueryOptions(client, () => props.groupId, filters),
)
const searchDraft = ref(filters.value.q ?? '')
const selectedIds = ref(new Set<number>())
const pendingOperations = ref(new Set<string>())
const feedback = ref('')
const deleteTarget = ref<{ ids: number[]; mask?: string } | undefined>()
const connectionWorkspaceOpen = ref(false)
const connectionStages = ref<CredentialStage[]>([])
const reauthorizationTarget = ref<CredentialItemDto | null>(null)
const connectOperationKey = ref<string>()
const copyControllers = useAbortControllerPool()
const searchDebounce = useDebouncedAction(250)
const connectionWorkspaceDescription = computed(() =>
  reauthorizationTarget.value
    ? t('group.credentials.subscription.reauthorizeDescription', {
        account: reauthorizationTarget.value.mask,
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
const dialogBusy = computed(() => {
  const target = deleteTarget.value
  if (target === undefined) return false
  return target.ids.length === 1 ? pending(target.ids[0]) : batchBusy.value
})
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
  return batchBusy.value || pending(id)
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
    await cacheCredentialItem(queryClient, props.groupId, result)
    if (refetchActive) await refetchActiveCredentialPage()
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
    const next = { ...item, observation }
    await reconcileItem(next, false)
  } catch (cause) {
    feedback.value = t(
      presentSubscriptionErrorKey(cause, 'group.credentials.subscription.syncFailed'),
    )
  } finally {
    setPending(item.credential_id, 'observation', false)
  }
}

const autoSavedStageIDs = new Set<string>()

watch(
  connectionStages,
  (stages) => {
    if (
      !connectionWorkspaceOpen.value ||
      reauthorizationTarget.value === null ||
      singleBusy.value
    ) {
      return
    }
    const ready = stages.find(
      ({ status, expires_at_ms }) => status === 'ready' && expires_at_ms > Date.now(),
    )
    if (!ready || autoSavedStageIDs.has(ready.stage_id)) return
    autoSavedStageIDs.add(ready.stage_id)
    void saveConnectedAccounts()
  },
  { deep: true },
)

// 抽屉自己管理焦点与滚动，这里只负责重置暂存状态。
function openConnectionWorkspace(target?: CredentialItemDto): void {
  connectionStages.value = []
  reauthorizationTarget.value = target ?? null
  connectOperationKey.value = undefined
  connectionWorkspaceOpen.value = true
}

function setConnectionWorkspace(open: boolean): void {
  if (!open && singleBusy.value) return
  connectionWorkspaceOpen.value = open
  if (!open) {
    connectionStages.value = []
    reauthorizationTarget.value = null
    connectOperationKey.value = undefined
  }
}

async function saveConnectedAccounts(): Promise<void> {
  const now = Date.now()
  const ready = connectionStages.value.filter(
    ({ status, expires_at_ms }) => status === 'ready' && expires_at_ms > now,
  )
  if (ready.length === 0 || singleBusy.value) {
    if (!singleBusy.value && connectionStages.value.some(({ status }) => status === 'ready')) {
      connectionStages.value = connectionStages.value.map((stage) =>
        stage.status === 'ready' && stage.expires_at_ms <= now
          ? { ...stage, status: 'expired' }
          : stage,
      )
      feedback.value = t('common.subscriptionErrors.stageExpired')
    }
    return
  }
  feedback.value = ''
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
    } else {
      connectOperationKey.value ??= crypto.randomUUID()
      await connectGroupCredentials(
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
    }
    succeeded = true
  } catch (cause) {
    feedback.value = t(
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
        <AppButton v-if="connectionType === 'subscription'" @click="openConnectionWorkspace()">
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
      :open="connectionWorkspaceOpen"
      :title="
        reauthorizationTarget
          ? t('group.credentials.subscription.reauthorize')
          : t('group.credentials.subscription.connect')
      "
      :description="connectionWorkspaceDescription"
      show-description
      :close-label="t('common.close')"
      :dismissible="!singleBusy"
      @update:open="setConnectionWorkspace"
    >
      <SubscriptionCredentialStager
        v-model="connectionStages"
        compact
        hide-header
        :single="reauthorizationTarget !== null"
        :disabled="singleBusy"
      />
      <template #footer>
        <AppButton
          variant="secondary"
          :disabled="singleBusy"
          @click="setConnectionWorkspace(false)"
        >
          {{ t('group.credentials.cancel') }}
        </AppButton>
        <AppButton
          :busy="singleBusy"
          :disabled="!connectionStages.some(({ status }) => status === 'ready')"
          @click="saveConnectedAccounts"
        >
          {{
            reauthorizationTarget
              ? t('group.credentials.subscription.confirmReauthorize')
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
          <AppButton v-if="connectionType === 'subscription'" @click="openConnectionWorkspace()">
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
            :item="item"
            :busy="rowBusy(item.credential_id)"
            @toggle="mutateItem($event, 'toggle')"
            @restore="mutateItem($event, 'restore')"
            @refresh="refreshObservation"
            @reauthorize="openConnectionWorkspace"
            @remove="deleteTarget = { ids: [$event.credential_id], mask: $event.mask }"
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
.group-credentials__accounts {
  display: grid;
  gap: var(--space-2);
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
