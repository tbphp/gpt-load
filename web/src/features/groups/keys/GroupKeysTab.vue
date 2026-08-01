<script setup lang="ts">
import { Search, X } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type {
  GroupKeyCollectionFilters,
  GroupKeyItemDto,
  GroupKeyStatus,
} from '@/api/control/types'
import {
  batchGroupKeys,
  cacheGroupKeyBatch,
  cacheGroupKeyItem,
  deleteGroupKey,
  groupKeyCollectionQueryOptions,
  invalidateGroupKeyCollections,
  restoreGroupKey,
  updateGroupKey,
} from '@/app/resources/upstream-keys'
import { groupDetailLocation } from '@/app/route-locations'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import IconButton from '@/components/ui/IconButton.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusSummaryFilter, {
  type StatusSummaryFilterValue,
} from '@/components/ui/StatusSummaryFilter.vue'

import GroupKeyBatchBar from './GroupKeyBatchBar.vue'
import GroupKeyMobileCard from './GroupKeyMobileCard.vue'
import GroupKeyRow from './GroupKeyRow.vue'
import {
  constrainGroupKeySearch,
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
let searchTimer: ReturnType<typeof setTimeout> | undefined

const collection = computed(() => keysQuery.data.value)
const selectedCount = computed(() => selectedIds.value.size)
const allVisibleSelected = computed(() => {
  const items = collection.value?.items ?? []
  return items.length > 0 && items.every(({ id }) => selectedIds.value.has(id))
})
const batchBusy = computed(() =>
  [...pendingOperations.value].some((key) => key.startsWith('batch:')),
)

watch(
  () => route.query,
  (query) => {
    if (searchTimer !== undefined) {
      clearTimeout(searchTimer)
      searchTimer = undefined
    }
    const next = parseGroupKeyRouteQuery(query)
    searchDraft.value = next.q ?? ''
    const canonical = serializeGroupKeyRouteQuery(next)
    const keys = Object.keys(canonical)
    if (
      Object.keys(query).length !== keys.length ||
      keys.some((key) => query[key] !== canonical[key])
    ) {
      void router.replace(groupDetailLocation(props.groupId, canonical))
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
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchTimer = undefined
    setFilter({ q: constrainGroupKeySearch(searchDraft.value) })
  }, 250)
}

function clearSearch(): void {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  searchTimer = undefined
  searchDraft.value = ''
  setFilter({ q: undefined })
}

function setStatus(value: StatusSummaryFilterValue | undefined): void {
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
  checked ? next.add(id) : next.delete(id)
  selectedIds.value = next
}
function setAllVisible(checked: boolean): void {
  const next = new Set(selectedIds.value)
  for (const { id } of collection.value?.items ?? []) checked ? next.add(id) : next.delete(id)
  selectedIds.value = next
}
function operation(id: number, action: string): string {
  return `${id}:${action}`
}
function pending(id: number): boolean {
  return [...pendingOperations.value].some((value) => value.startsWith(`${id}:`))
}
function setPending(id: number | 'batch', action: string, value: boolean): void {
  const next = new Set(pendingOperations.value)
  const key = id === 'batch' ? `batch:${action}` : operation(id, action)
  value ? next.add(key) : next.delete(key)
  pendingOperations.value = next
}
async function refetchCurrent(): Promise<void> {
  await keysQuery.refetch()
}

async function mutateItem(
  item: GroupKeyItemDto,
  action: 'weight' | 'toggle' | 'restore',
  value?: string,
): Promise<void> {
  if (pending(item.id)) return
  feedback.value = ''
  setPending(item.id, action, true)
  try {
    const result =
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
    await cacheGroupKeyItem(queryClient, props.groupId, result)
    await refetchCurrent()
  } catch {
    feedback.value = t(
      action === 'restore' ? 'group.keys.restoreFailed' : 'group.keys.updateFailed',
    )
  } finally {
    setPending(item.id, action, false)
  }
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target || batchBusy.value) return
  feedback.value = ''
  if (target.ids.length === 1) {
    const id = target.ids[0]
    setPending(id, 'delete', true)
    try {
      await deleteGroupKey(client, props.groupId, id)
      await invalidateGroupKeyCollections(queryClient, props.groupId)
      deleteTarget.value = undefined
      selectedIds.value.delete(id)
      selectedIds.value = new Set(selectedIds.value)
      await refetchCurrent()
    } catch {
      feedback.value = t('group.keys.deleteFailed')
    } finally {
      setPending(id, 'delete', false)
    }
    return
  }
  await runBatch('delete', target.ids)
  deleteTarget.value = undefined
}

async function runBatch(
  action: 'enable' | 'disable' | 'delete',
  ids = [...selectedIds.value],
): Promise<void> {
  if (ids.length === 0 || batchBusy.value) return
  feedback.value = ''
  setPending('batch', action, true)
  try {
    const result = await batchGroupKeys(client, props.groupId, { action, key_ids: ids })
    await cacheGroupKeyBatch(queryClient, props.groupId, action, result)
    selectedIds.value = new Set()
    await refetchCurrent()
  } catch {
    feedback.value = t('group.keys.batch.failed')
  } finally {
    setPending('batch', action, false)
  }
}

onBeforeUnmount(() => {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
})
</script>

<template>
  <section class="group-keys" aria-labelledby="group-keys-heading">
    <header class="group-keys__header">
      <div>
        <h2 id="group-keys-heading">{{ t('group.keys.title') }}</h2>
        <p>{{ t('group.keys.description') }}</p>
      </div>
    </header>
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
      <StatusSummaryFilter
        v-if="collection.summary.total > 0"
        :summary="collection.summary"
        :model-value="filters.status"
        :labels="{
          region: t('group.keys.summary.region'),
          current: t('group.keys.summary.current'),
          all: t('group.keys.status.all'),
          available: t('group.keys.effective.available'),
          unavailable: '',
          cooldown: t('group.keys.effective.cooldown'),
          blacklisted: t('group.keys.effective.blacklisted'),
          disabled: t('group.keys.effective.disabled'),
        }"
        @update:model-value="setStatus"
      />
      <form
        v-if="collection.summary.total > 0"
        class="group-keys__filters"
        role="search"
        :aria-label="t('group.keys.filters.region')"
        @submit.prevent
      >
        <label
          ><span>{{ t('group.keys.filters.search') }}</span
          ><span class="group-keys__search"
            ><Search :size="15" aria-hidden="true" /><input
              v-model="searchDraft"
              type="search"
              :placeholder="t('group.keys.filters.placeholder')"
              @input="scheduleSearch" /><IconButton
              v-if="searchDraft"
              size="xs"
              variant="ghost"
              :label="t('group.keys.filters.clear')"
              @click="clearSearch"
              ><X :size="14" aria-hidden="true" /></IconButton></span
        ></label>
      </form>
      <EmptyState
        v-if="collection.summary.total === 0"
        :title="t('group.keys.emptyTitle')"
        :description="t('group.keys.emptyDescription')"
      />
      <EmptyState
        v-else-if="collection.pagination.total_items === 0"
        :title="t('group.keys.emptyFilterTitle')"
        :description="t('group.keys.emptyFilterDescription')"
        ><template #actions
          ><AppButton
            variant="secondary"
            size="compact"
            @click="updateRoute({ page: 1, page_size: filters.page_size })"
            >{{ t('group.keys.filters.reset') }}</AppButton
          ></template
        ></EmptyState
      >
      <template v-else>
        <div class="group-keys__desktop">
          <DataTable :caption="t('group.keys.caption')" dense
            ><thead>
              <tr>
                <th scope="col">
                  <input
                    type="checkbox"
                    :checked="allVisibleSelected"
                    :aria-label="t('group.keys.selectVisible')"
                    @change="setAllVisible(($event.target as HTMLInputElement).checked)"
                  />
                </th>
                <th scope="col">{{ t('group.keys.columns.key') }}</th>
                <th scope="col">{{ t('group.keys.columns.status') }}</th>
                <th scope="col">{{ t('group.keys.columns.weight') }}</th>
                <th scope="col">{{ t('group.keys.columns.recent') }}</th>
                <th scope="col">{{ t('group.keys.columns.actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <GroupKeyRow
                v-for="item in collection.items"
                :key="item.id"
                :item="item"
                :selected="selectedIds.has(item.id)"
                :busy="pending(item.id)"
                @update:selected="setSelected(item.id, $event)"
                @weight="mutateItem($event.item, 'weight', $event.value)"
                @toggle="mutateItem($event, 'toggle')"
                @restore="mutateItem($event, 'restore')"
                @remove="deleteTarget = { ids: [$event.id], mask: $event.mask }"
              /></tbody
          ></DataTable>
        </div>
        <div class="group-keys__mobile">
          <GroupKeyMobileCard
            v-for="item in collection.items"
            :key="item.id"
            :item="item"
            :selected="selectedIds.has(item.id)"
            :busy="pending(item.id)"
            @update:selected="setSelected(item.id, $event)"
            @weight="mutateItem($event.item, 'weight', $event.value)"
            @toggle="mutateItem($event, 'toggle')"
            @restore="mutateItem($event, 'restore')"
            @remove="deleteTarget = { ids: [$event.id], mask: $event.mask }"
          />
        </div>
        <PaginationBar
          :page="collection.pagination.page"
          :page-size="collection.pagination.page_size"
          :total-items="collection.pagination.total_items"
          :total-pages="collection.pagination.total_pages"
          show-page-size
          @previous="setPage(filters.page - 1)"
          @next="setPage(filters.page + 1)"
          @update:page-size="setPageSize"
        />
        <GroupKeyBatchBar
          v-if="selectedCount > 0"
          :selected-count="selectedCount"
          :pending="batchBusy"
          @enable="runBatch('enable')"
          @disable="runBatch('disable')"
          @remove="deleteTarget = { ids: [...selectedIds] }"
          @clear="selectedIds = new Set()"
        />
      </template>
    </template>
    <AppDialog
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
      :dismissible="!batchBusy"
      @update:open="!$event && (deleteTarget = undefined)"
      ><div class="group-keys__dialog-actions">
        <AppButton variant="secondary" :disabled="batchBusy" @click="deleteTarget = undefined">{{
          t('group.keys.cancel')
        }}</AppButton
        ><AppButton variant="danger" :busy="batchBusy" @click="confirmDelete">{{
          t('group.keys.confirmDelete')
        }}</AppButton>
      </div></AppDialog
    >
  </section>
</template>

<style scoped>
.group-keys {
  display: grid;
  gap: var(--space-4);
  padding-top: var(--space-4);
}
.group-keys__header h2,
.group-keys__header p {
  margin: 0;
}
.group-keys__header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.group-keys__feedback {
  margin: 0;
  border: 1px solid var(--color-feedback-danger-border);
  border-radius: var(--radius-control);
  background: var(--color-danger-bg);
  color: var(--color-text);
  padding: var(--space-3);
}
.group-keys__filters {
  display: grid;
  max-width: 420px;
}
.group-keys__filters label {
  display: grid;
  gap: var(--space-1);
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.group-keys__search {
  position: relative;
  display: block;
}
.group-keys__search > svg {
  position: absolute;
  top: 50%;
  left: 10px;
  transform: translateY(-50%);
  color: var(--color-text-faint);
  pointer-events: none;
}
.group-keys__search input {
  width: 100%;
  min-height: var(--control-md);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 40px 0 32px;
  font: inherit;
}
.group-keys__search :deep(.icon-button) {
  position: absolute;
  top: 2px;
  right: 3px;
}
.group-keys__mobile {
  display: none;
  gap: var(--space-3);
}
.group-keys__dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}
@media (max-width: 767px) {
  .group-keys__desktop {
    display: none;
  }
  .group-keys__mobile {
    display: grid;
  }
}
</style>
