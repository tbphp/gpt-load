<script setup lang="ts">
import { KeyRound, Plus, Search } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type {
  GroupKeyCollectionFilters,
  GroupKeyCollectionDto,
  GroupKeyItemDto,
  GroupKeySummaryDto,
  GroupKeyStatus,
  GroupSummaryDto,
} from '@/api/control/types'
import {
  batchGroupKeys,
  cacheGroupKeyBatch,
  cacheGroupKeyItem,
  groupKeyCollectionQueryOptions,
  revealGroupKey,
  restoreGroupKey,
  updateGroupKey,
} from '@/app/resources/upstream-keys'
import { groupDetailLocation, importLocation } from '@/app/route-locations'
import { controlQueryKeys } from '@/app/query-keys'
import { useAbortControllerPool } from '@/app/use-abort-controller-pool'
import { useDebouncedAction } from '@/app/use-debounced-action'
import CollectionFilterBar from '@/components/collection/CollectionFilterBar.vue'
import CollectionStatusSummary from '@/components/collection/CollectionStatusSummary.vue'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'

import GroupKeyBatchBar from './GroupKeyBatchBar.vue'
import GroupKeyRecord from './GroupKeyRecord.vue'
import {
  constrainGroupKeySearch,
  isCanonicalGroupKeyRouteQuery,
  parseGroupKeyRouteQuery,
  serializeGroupKeyRouteQuery,
} from '../group-route'

const props = defineProps<{ groupId: number }>()
const client = useApiClient()
const queryClient = useQueryClient()
const route = useRoute()
const router = useRouter()
const { n, t } = useI18n()
const filters = computed(() => parseGroupKeyRouteQuery(route.query))
const keysQuery = useQuery(groupKeyCollectionQueryOptions(client, () => props.groupId, filters))
const searchDraft = ref(filters.value.q ?? '')
const selectedIds = ref(new Set<number>())
const pendingOperations = ref(new Set<string>())
const feedback = ref('')
const deleteTarget = ref<{ ids: number[]; mask?: string } | undefined>()
const copyControllers = useAbortControllerPool()
const searchDebounce = useDebouncedAction(250)

const collection = computed(() => keysQuery.data.value)
const selectedCount = computed(() => selectedIds.value.size)
const allVisibleSelected = computed(() => {
  const items = collection.value?.items ?? []
  return items.length > 0 && items.every(({ id }) => selectedIds.value.has(id))
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
      label: t('group.keys.status.all'),
      count: summary.total,
      tone: 'neutral' as const,
    },
    {
      value: 'available',
      label: t('group.keys.effective.available'),
      count: summary.available,
      tone: 'success' as const,
    },
    {
      value: 'cooldown',
      label: t('group.keys.effective.cooldown'),
      count: summary.cooldown,
      tone: 'warning' as const,
    },
    {
      value: 'blacklisted',
      label: t('group.keys.effective.blacklisted'),
      count: summary.blacklisted,
      tone: 'danger' as const,
    },
    {
      value: 'disabled',
      label: t('group.keys.effective.disabled'),
      count: summary.disabled,
      tone: 'neutral' as const,
    },
  ]
})

watch(
  () => route.query,
  (query) => {
    searchDebounce.cancel()
    const next = parseGroupKeyRouteQuery(query)
    searchDraft.value = next.q ?? ''
    if (!isCanonicalGroupKeyRouteQuery(query, next)) {
      void router.replace(groupDetailLocation(props.groupId, serializeGroupKeyRouteQuery(next)))
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
  () => [props.groupId, filters.value.page, collection.value?.items.map(({ id }) => id).join(',')],
  () => concealCopiedKeys(),
)
watch(
  () => ({
    totalPages: collection.value?.pagination.total_pages,
    page: filters.value.page,
    placeholder: keysQuery.isPlaceholderData.value,
  }),
  ({ totalPages, page, placeholder }) => {
    if (!placeholder && totalPages !== undefined && totalPages > 0 && page > totalPages)
      updateRoute({ ...filters.value, page: totalPages }, true)
  },
)

function updateRoute(next: GroupKeyCollectionFilters, replace = false): void {
  const location = groupDetailLocation(props.groupId, serializeGroupKeyRouteQuery(next))
  void (replace ? router.replace(location) : router.push(location))
}

function setFilter(
  patch: Partial<Pick<GroupKeyCollectionFilters, 'q' | 'status' | 'page_size'>>,
): void {
  updateRoute({ ...filters.value, ...patch, page: 1 })
}

function scheduleSearch(): void {
  searchDebounce.schedule(() => {
    setFilter({ q: constrainGroupKeySearch(searchDraft.value) })
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
  setFilter({ status: value as GroupKeyStatus | undefined })
}

function setPage(page: number): void {
  updateRoute({ ...filters.value, page })
}
function setPageSize(pageSize: 20 | 50 | 100): void {
  setFilter({ page_size: pageSize })
}
function setSelected(id: number, checked: boolean): void {
  const next = new Set(selectedIds.value)
  if (checked) next.add(id)
  else next.delete(id)
  selectedIds.value = next
}
function setAllVisible(checked: boolean): void {
  const next = new Set(selectedIds.value)
  for (const { id } of collection.value?.items ?? []) {
    if (checked) next.add(id)
    else next.delete(id)
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
    const result = await revealGroupKey(client, props.groupId, id, controller.signal)
    return result.key
  } finally {
    copyControllers.release(controller)
  }
}
function concealCopiedKeys(): void {
  copyControllers.abortAll()
}
function setPending(id: number | 'batch', action: string, value: boolean): void {
  const next = new Set(pendingOperations.value)
  const key = id === 'batch' ? `batch:${action}` : operation(id, action)
  if (value) next.add(key)
  else next.delete(key)
  pendingOperations.value = next
}
function cachedCurrentSummary(): GroupKeySummaryDto | undefined {
  return queryClient.getQueryData<GroupKeyCollectionDto>(
    controlQueryKeys.groups.keys(props.groupId, filters.value),
  )?.summary
}

function synchronizeGroupSummary(summary: GroupKeySummaryDto | undefined): void {
  const queryKey = controlQueryKeys.groups.summary(props.groupId)
  if (summary === undefined) {
    void queryClient.invalidateQueries({ queryKey, exact: true, refetchType: 'none' })
    return
  }
  queryClient.setQueryData<GroupSummaryDto>(queryKey, (group) => {
    if (group === undefined) return group
    return {
      ...group,
      key_count: summary.total,
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
        queryKey: controlQueryKeys.groups.keys(props.groupId, filters.value),
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

async function refetchActiveKeyPage(): Promise<void> {
  await queryClient.refetchQueries(
    {
      queryKey: controlQueryKeys.groups.keys(props.groupId, filters.value),
      exact: true,
      type: 'active',
    },
    { throwOnError: true },
  )
}

async function reconcileItem(result: GroupKeyItemDto, refetchActive: boolean): Promise<void> {
  try {
    await cacheGroupKeyItem(queryClient, props.groupId, result)
    if (refetchActive) await refetchActiveKeyPage()
    const summary = cachedCurrentSummary()
    if (summary === undefined) {
      await invalidateReconciliationQueries()
      return
    }
    synchronizeGroupSummary(summary)
  } catch {
    feedback.value = t('group.keys.reconcileFailed')
    await invalidateReconciliationQueries()
  }
}

async function reconcileBatch(
  action: 'enable' | 'disable' | 'delete',
  result: Awaited<ReturnType<typeof batchGroupKeys>>,
): Promise<void> {
  try {
    await cacheGroupKeyBatch(queryClient, props.groupId, action, result)
    if (action !== 'delete') await refetchActiveKeyPage()
    synchronizeGroupSummary(result.summary)
  } catch {
    feedback.value = t('group.keys.reconcileFailed')
    await invalidateReconciliationQueries()
  }
}

async function mutateItem(
  item: GroupKeyItemDto,
  action: 'weight' | 'toggle' | 'restore',
  value?: string,
): Promise<void> {
  if (batchBusy.value || pending(item.id)) return
  feedback.value = ''
  setPending(item.id, action, true)
  let result: GroupKeyItemDto
  try {
    result =
      action === 'restore'
        ? await restoreGroupKey(client, props.groupId, item.id)
        : await updateGroupKey(
            client,
            props.groupId,
            item.id,
            action === 'weight'
              ? { weight_manual: value === 'auto' ? null : Number(value) }
              : { status: item.configured_status === 'active' ? 'disabled' : 'active' },
          )
  } catch {
    feedback.value = t(
      action === 'restore' ? 'group.keys.restoreFailed' : 'group.keys.updateFailed',
    )
    setPending(item.id, action, false)
    return
  }
  try {
    await reconcileItem(result, action !== 'weight')
  } finally {
    setPending(item.id, action, false)
  }
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target || dialogBusy.value || batchBusy.value) return
  feedback.value = ''
  if (target.ids.length === 1) {
    const id = target.ids[0]
    setPending(id, 'delete', true)
    let result: Awaited<ReturnType<typeof batchGroupKeys>>
    try {
      result = await batchGroupKeys(client, props.groupId, {
        action: 'delete',
        key_ids: [id],
      })
    } catch {
      feedback.value = t('group.keys.deleteFailed')
      setPending(id, 'delete', false)
      return
    }
    try {
      await reconcileBatch('delete', result)
      deleteTarget.value = undefined
      selectedIds.value.delete(id)
      selectedIds.value = new Set(selectedIds.value)
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
  let result: Awaited<ReturnType<typeof batchGroupKeys>>
  try {
    result = await batchGroupKeys(client, props.groupId, { action, key_ids: ids })
  } catch {
    feedback.value = t('group.keys.batch.failed')
    setPending('batch', action, false)
    return false
  }
  try {
    await reconcileBatch(action, result)
    selectedIds.value = new Set()
    return true
  } finally {
    setPending('batch', action, false)
  }
}
</script>

<template>
  <section
    class="group-keys"
    aria-labelledby="group-keys-heading"
    :aria-busy="keysQuery.isFetching.value ? 'true' : undefined"
  >
    <PanelHeader heading-id="group-keys-heading" :title="t('group.keys.title')">
      <template #actions>
        <RouterLink
          v-slot="{ navigate }"
          :to="importLocation({ mode: 'existing', group_id: groupId })"
          custom
        >
          <AppButton role="link" @click="navigate">
            <Plus :size="16" aria-hidden="true" />{{ t('group.keys.add') }}
          </AppButton>
        </RouterLink>
      </template>
    </PanelHeader>
    <QueryFeedback
      v-if="keysQuery.isPending.value"
      state="loading"
      :message="t('group.keys.loading')"
    />
    <QueryFeedback
      v-else-if="keysQuery.isError.value && !collection"
      state="error"
      :message="t('group.keys.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="keysQuery.refetch()"
    />
    <template v-else-if="collection">
      <QueryFeedback
        v-if="keysQuery.isError.value"
        state="stale"
        :message="t('group.keys.stale')"
        :retry-label="t('common.retry')"
        @retry="keysQuery.refetch()"
      />
      <p v-if="feedback" class="group-keys__feedback" role="alert">{{ feedback }}</p>
      <CollectionStatusSummary
        v-if="collection.summary.total > 0"
        :total="collection.summary.total"
        :items="statusSummaryItems"
        :model-value="filters.status"
        :label="t('group.keys.summary.region')"
        :total-label="t('group.keys.summary.current')"
        appearance="detail"
        @update:model-value="setStatus"
      />
      <CollectionFilterBar
        v-if="collection.summary.total > 0"
        single-column
        :label="t('group.keys.filters.region')"
        :show-result="hasChangedConditions"
        appearance="detail"
      >
        <label class="collection-filter-field collection-filter-field--search">
          <span class="collection-filter-label">{{ t('group.keys.filters.search') }}</span>
          <AppSearchInput
            v-model="searchDraft"
            :label="t('group.keys.filters.search')"
            :placeholder="t('group.keys.filters.placeholder')"
            :clear-label="t('group.keys.filters.clear')"
            @update:model-value="scheduleSearch"
            @clear="clearSearch"
          />
        </label>
        <template #result>
          <span aria-live="polite">
            {{
              t('group.keys.filters.result', {
                shown: n(collection.items.length),
                total: n(collection.pagination.total_items),
              })
            }}
          </span>
          <AppButton variant="link" size="inline" @click="resetFilters">
            {{ t('group.keys.filters.reset') }}
          </AppButton>
        </template>
      </CollectionFilterBar>
      <EmptyState
        v-if="collection.summary.total === 0"
        :title="t('group.keys.emptyTitle')"
        :description="t('group.keys.emptyDescription')"
        variant="ledger"
      >
        <template #icon><KeyRound :size="20" /></template>
        <template #actions>
          <RouterLink
            v-slot="{ navigate }"
            :to="importLocation({ mode: 'existing', group_id: groupId })"
            custom
          >
            <AppButton role="link" @click="navigate">
              <Plus :size="15" aria-hidden="true" />{{ t('group.keys.add') }}
            </AppButton>
          </RouterLink>
        </template>
      </EmptyState>
      <EmptyState
        v-else-if="collection.pagination.total_items === 0"
        :title="t('group.keys.emptyFilterTitle')"
        :description="t('group.keys.emptyFilterDescription')"
        variant="ledger"
      >
        <template #icon><Search :size="20" /></template>
        <template #actions>
          <AppButton variant="secondary" size="compact" @click="resetFilters">
            {{ t('group.keys.filters.reset') }}
          </AppButton>
        </template>
      </EmptyState>
      <template v-else>
        <GroupKeyBatchBar
          v-if="selectedCount > 0"
          :selected-count="selectedCount"
          :pending="batchBusy || singleBusy"
          @enable="runBatch('enable')"
          @disable="runBatch('disable')"
          @remove="deleteTarget = { ids: [...selectedIds] }"
        />
        <LedgerRecordList
          :label="t('group.keys.caption')"
          :row-count="collection.pagination.total_items + 1"
          grid-class="group-key-record-grid"
        >
          <template #header>
            <span class="group-keys__select-all" role="columnheader">
              <label>
                <span class="sr-only">{{ t('group.keys.selectVisible') }}</span>
                <input
                  type="checkbox"
                  :checked="allVisibleSelected"
                  :disabled="batchBusy"
                  @change="setAllVisible(($event.target as HTMLInputElement).checked)"
                />
              </label>
            </span>
            <span role="columnheader">{{ t('group.keys.columns.key') }}</span>
            <span role="columnheader">{{ t('group.keys.columns.status') }}</span>
            <span role="columnheader">{{ t('group.keys.columns.weight') }}</span>
            <span role="columnheader">{{ t('group.keys.columns.recent') }}</span>
            <span role="columnheader">{{ t('group.keys.columns.actions') }}</span>
          </template>

          <GroupKeyRecord
            v-for="(item, index) in collection.items"
            :key="item.id"
            :item="item"
            :row-index="
              (collection.pagination.page - 1) * collection.pagination.page_size + index + 2
            "
            :selected="selectedIds.has(item.id)"
            :busy="rowBusy(item.id)"
            :resolve-copy-value="resolveCopyValue"
            @update:selected="setSelected(item.id, $event)"
            @weight="mutateItem($event.item, 'weight', $event.value)"
            @toggle="mutateItem($event, 'toggle')"
            @restore="mutateItem($event, 'restore')"
            @remove="deleteTarget = { ids: [$event.id], mask: $event.mask }"
          />
        </LedgerRecordList>
        <PaginationBar
          :page="collection.pagination.page"
          :page-size="collection.pagination.page_size"
          :total-items="collection.pagination.total_items"
          :total-pages="collection.pagination.total_pages"
          show-page-size
          appearance="detail"
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
          ? t('group.keys.deleteTitle')
          : t('group.keys.batch.deleteTitle')
      "
      :description="
        deleteTarget?.ids.length === 1
          ? t('group.keys.deleteDescription', { mask: deleteTarget.mask })
          : t('group.keys.batch.deleteDescription', { count: n(deleteTarget?.ids.length ?? 0) })
      "
      :close-label="t('group.keys.closeDialog')"
      :cancel-label="t('group.keys.cancel')"
      :confirm-label="t('group.keys.confirmDelete')"
      tone="danger"
      :pending="dialogBusy"
      @update:open="!$event && (deleteTarget = undefined)"
      @confirm="confirmDelete"
    />
  </section>
</template>

<style scoped>
.group-keys {
  display: grid;
  min-width: 0;
  gap: 0;
  padding-top: var(--detail-panel-padding-top);
}
.group-keys__feedback {
  margin: 0;
  border: 1px solid var(--color-feedback-danger-border);
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  color: var(--color-text);
  padding: var(--space-3);
}
.group-key-record-grid {
  --ledger-record-list-record-min-height: 52px;
  --ledger-record-list-record-padding: 8px 0;
  --ledger-record-list-grid: 48px minmax(150px, 0.95fr) 116px minmax(118px, 0.72fr)
    minmax(150px, 0.95fr) minmax(280px, 1.7fr);
  --ledger-record-list-column-gap: 12px;
}
.group-keys__select-all {
  display: flex;
  justify-content: center;
}
.group-keys__select-all label {
  display: grid;
  width: 32px;
  height: 32px;
  place-items: center;
  cursor: pointer;
}
.group-keys__select-all input {
  width: 16px;
  height: 16px;
  accent-color: var(--color-action);
}
@media (max-width: 1120px) {
  .group-key-record-grid {
    --ledger-record-list-grid: 44px minmax(130px, 0.9fr) 108px minmax(108px, 0.7fr)
      minmax(132px, 0.9fr) minmax(250px, 1.45fr);
    --ledger-record-list-column-gap: 9px;
  }
}
@media (max-width: 1023px) and (min-width: 861px) {
  .group-key-record-grid {
    --ledger-record-list-grid: 44px minmax(130px, 0.9fr) 108px minmax(108px, 0.7fr)
      minmax(220px, 1.35fr);
  }
  .group-key-record-grid :deep(.ledger-record-list__header > :nth-child(5)),
  .group-key-record-grid :deep(.group-key-record__recent) {
    display: none;
  }
}
@media (max-width: 860px) {
  .group-key-record-grid {
    --ledger-record-list-card-grid: minmax(0, 0.8fr) minmax(0, 1.2fr);
  }
  .group-keys__select-all label {
    width: var(--touch-target);
    height: var(--touch-target);
  }
}
@media (max-width: 800px) {
  .group-keys {
    padding-top: var(--detail-panel-padding-top-compact);
  }
}
</style>
