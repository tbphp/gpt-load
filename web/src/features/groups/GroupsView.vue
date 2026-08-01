<script setup lang="ts">
import { ArrowRight, KeyRound, Layers3, Plus, Search, TriangleAlert, X } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type {
  GroupCollectionFilters,
  GroupCollectionSort,
  GroupCollectionStatus,
  GroupProtocol,
  KeyCounts,
} from '@/api/control/types'
import { groupCollectionQueryOptions } from '@/app/resources/groups'
import { groupDetailLocation, groupsLocation, importLocation } from '@/app/route-locations'
import { useVisibleRefetch } from '@/app/use-visible-refetch'
import CollectionFilterBar from '@/components/collection/CollectionFilterBar.vue'
import CollectionStatusSummary from '@/components/collection/CollectionStatusSummary.vue'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import IconButton from '@/components/ui/IconButton.vue'
import KeyHealthBar from '@/components/ui/KeyHealthBar.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import {
  constrainGroupCollectionSearchQuery,
  isCanonicalGroupCollectionRouteQuery,
  parseGroupCollectionRouteQuery,
  serializeGroupCollectionRouteQuery,
} from './group-collection-route'

const protocolOptions: readonly GroupProtocol[] = [
  'openai-completions',
  'openai-responses',
  'anthropic',
  'gemini',
]
const sortOptions: readonly GroupCollectionSort[] = ['status', 'name', 'keys', 'created']

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const { n, t } = useI18n()
const filters = computed(() => parseGroupCollectionRouteQuery(route.query))
const searchDraft = ref(filters.value.q ?? '')
const groupsQuery = useQuery(groupCollectionQueryOptions(client, filters))
let searchTimer: ReturnType<typeof setTimeout> | undefined

const data = computed(() => groupsQuery.data.value)
const hasFilterCriteria = computed(
  () =>
    filters.value.q !== undefined ||
    filters.value.status !== undefined ||
    filters.value.protocol !== undefined,
)
const hasChangedConditions = computed(
  () => hasFilterCriteria.value || filters.value.sort !== 'status',
)
const collectionBusy = computed(() => data.value !== undefined && groupsQuery.isFetching.value)
const protocolSelectOptions = computed(() => [
  { value: 'all', label: t('groups.collection.filters.allProtocols') },
  ...protocolOptions.map((protocol) => ({ value: protocol, label: protocol })),
])
const sortSelectOptions = computed(() =>
  sortOptions.map((sort) => ({
    value: sort,
    label: t(`groups.collection.sort.${sort}`),
  })),
)
const statusSummaryItems = computed(() => {
  const summary = data.value?.summary
  if (!summary) return []

  return [
    {
      value: undefined,
      label: t('groups.collection.status.all'),
      count: summary.total,
      tone: 'neutral' as const,
    },
    {
      value: 'available',
      label: t('groups.collection.status.available'),
      count: summary.available,
      tone: 'success' as const,
    },
    {
      value: 'unavailable',
      label: t('groups.collection.status.unavailable'),
      count: summary.unavailable,
      tone: 'danger' as const,
    },
    {
      value: 'disabled',
      label: t('groups.collection.status.disabled'),
      count: summary.disabled,
      tone: 'neutral' as const,
    },
  ]
})

watch(
  () => route.query,
  (query) => {
    if (searchTimer !== undefined) {
      clearTimeout(searchTimer)
      searchTimer = undefined
    }
    const parsed = parseGroupCollectionRouteQuery(query)
    searchDraft.value = parsed.q ?? ''
    if (!isCanonicalGroupCollectionRouteQuery(query, parsed)) {
      void router.replace(groupsLocation(serializeGroupCollectionRouteQuery(parsed)))
    }
  },
  { deep: true, immediate: true },
)

watch(
  [
    () => data.value?.pagination.total_pages,
    () => filters.value.page,
    () => groupsQuery.isPlaceholderData.value,
  ],
  ([totalPages, page, isPlaceholderData]) => {
    if (!isPlaceholderData && totalPages !== undefined && totalPages > 0 && page > totalPages) {
      void router.replace(
        groupsLocation(serializeGroupCollectionRouteQuery({ ...filters.value, page: totalPages })),
      )
    }
  },
)

useVisibleRefetch([groupsQuery.refetch])

function routeWithFilters(next: GroupCollectionFilters, replace = false): void {
  const location = groupsLocation(serializeGroupCollectionRouteQuery(next))
  void (replace ? router.replace(location) : router.push(location))
}

function updateConditions(
  patch: Partial<Pick<GroupCollectionFilters, 'q' | 'status' | 'protocol' | 'sort'>>,
): void {
  const q = constrainGroupCollectionSearchQuery(searchDraft.value)
  routeWithFilters({ ...filters.value, q, ...patch, page: 1 })
}

function scheduleSearch(): void {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchTimer = undefined
    updateConditions({ q: constrainGroupCollectionSearchQuery(searchDraft.value) })
  }, 250)
}

function clearSearch(): void {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  searchTimer = undefined
  searchDraft.value = ''
  updateConditions({ q: undefined })
}

function setStatus(status: string | undefined): void {
  updateConditions({ status: status as GroupCollectionStatus | undefined })
}

function setProtocol(value: string): void {
  updateConditions({ protocol: value === 'all' ? undefined : (value as GroupProtocol) })
}

function setSort(value: string): void {
  updateConditions({ sort: value as GroupCollectionSort })
}

function resetConditions(): void {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  searchTimer = undefined
  searchDraft.value = ''
  routeWithFilters({ sort: 'status', page: 1, page_size: 20 })
}

function setPage(page: number): void {
  routeWithFilters({ ...filters.value, page })
}

function keyHealthLabel(counts: KeyCounts): string {
  return t('groups.collection.keyHealthLabel', {
    total: n(counts.total),
    available: n(counts.available),
    cooldown: n(counts.cooldown),
    blacklisted: n(counts.blacklisted),
    disabled: n(counts.disabled),
  })
}

onBeforeUnmount(() => {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
})
</script>

<template>
  <PageFrame aria-labelledby="groups-title">
    <LedgerSheet class="groups-ledger" :aria-busy="collectionBusy ? 'true' : undefined">
      <PageHeader id="groups-title" :title="t('groups.title')" />

      <section
        v-if="groupsQuery.isPending.value"
        class="loading-state"
        :aria-label="t('groups.collection.loading')"
        aria-busy="true"
      >
        <span class="sr-only">{{ t('groups.collection.loading') }}</span>
        <div v-for="row in 4" :key="row" class="skeleton-row" aria-hidden="true">
          <SkeletonBlock v-for="cell in 6" :key="cell" height="11px" />
        </div>
      </section>

      <div v-else-if="groupsQuery.isError.value && !data" class="collection-error" role="alert">
        <EmptyState
          :title="t('groups.collection.errorTitle')"
          :description="t('groups.collection.errorDescription')"
          variant="ledger"
        >
          <template #icon><TriangleAlert :size="20" /></template>
          <template #actions>
            <AppButton variant="secondary" size="compact" @click="groupsQuery.refetch()">
              {{ t('groups.collection.retry') }}
            </AppButton>
          </template>
        </EmptyState>
      </div>

      <template v-else-if="data">
        <CollectionStatusSummary
          v-if="data.summary.total > 0"
          :total="data.summary.total"
          :items="statusSummaryItems"
          :model-value="filters.status"
          :label="t('groups.collection.summary.region')"
          :total-label="t('groups.collection.summary.current')"
          @update:model-value="setStatus"
        />

        <QueryFeedback
          v-if="groupsQuery.isError.value"
          class="stale-banner"
          state="stale"
          :message="t('groups.collection.stale')"
          :retry-label="t('groups.collection.retry')"
          @retry="groupsQuery.refetch()"
        />

        <template v-if="data.summary.total > 0">
          <CollectionFilterBar
            :label="t('groups.collection.filters.region')"
            :show-result="hasChangedConditions"
          >
            <label class="collection-filter-field collection-filter-field--search">
              <span class="collection-filter-label">
                {{ t('groups.collection.filters.searchLabel') }}
              </span>
              <span class="collection-filter-search-control">
                <Search :size="15" aria-hidden="true" />
                <input
                  v-model="searchDraft"
                  class="collection-filter-control"
                  type="search"
                  :aria-label="t('groups.collection.filters.searchLabel')"
                  :placeholder="t('groups.collection.filters.searchPlaceholder')"
                  @input="scheduleSearch"
                />
                <IconButton
                  v-if="searchDraft"
                  class="collection-filter-search-clear"
                  size="xs"
                  variant="ghost"
                  :label="t('groups.collection.filters.clearSearch')"
                  @click="clearSearch"
                >
                  <X :size="14" aria-hidden="true" />
                </IconButton>
              </span>
            </label>

            <label class="collection-filter-field collection-filter-field--monospace">
              <span class="collection-filter-label">
                {{ t('groups.collection.filters.protocolLabel') }}
              </span>
              <AppSelect
                size="compact"
                :label="t('groups.collection.filters.protocolLabel')"
                :model-value="filters.protocol ?? 'all'"
                :options="protocolSelectOptions"
                @update:model-value="setProtocol"
              />
            </label>

            <label class="collection-filter-field">
              <span class="collection-filter-label">
                {{ t('groups.collection.filters.sortLabel') }}
              </span>
              <AppSelect
                size="compact"
                :label="t('groups.collection.filters.sortLabel')"
                :model-value="filters.sort"
                :options="sortSelectOptions"
                @update:model-value="setSort"
              />
            </label>
            <template #result>
              <span aria-live="polite">
                {{
                  t('groups.collection.result', {
                    shown: n(data.items.length),
                    total: n(data.pagination.total_items),
                  })
                }}
              </span>
              <AppButton variant="link" size="inline" @click="resetConditions">
                {{ t('groups.collection.filters.reset') }}
              </AppButton>
            </template>
          </CollectionFilterBar>
        </template>

        <EmptyState
          v-if="data.summary.total === 0"
          :title="t('groups.collection.emptyTitle')"
          :description="t('groups.collection.emptyDescription')"
          variant="ledger"
        >
          <template #icon><Layers3 :size="20" /></template>
          <template #actions>
            <RouterLink class="button-link" :to="importLocation()">
              <KeyRound :size="15" aria-hidden="true" />
              {{ t('groups.collection.importKeys') }}
            </RouterLink>
          </template>
        </EmptyState>

        <EmptyState
          v-else-if="data.pagination.total_items === 0 && hasFilterCriteria"
          :title="t('groups.collection.noResultsTitle')"
          :description="t('groups.collection.noResultsDescription')"
          variant="ledger"
        >
          <template #icon><Search :size="20" /></template>
          <template #actions>
            <AppButton variant="secondary" size="compact" @click="resetConditions">
              {{ t('groups.collection.filters.reset') }}
            </AppButton>
          </template>
        </EmptyState>

        <template v-else-if="data.items.length > 0">
          <LedgerRecordList
            :label="t('groups.collection.tableLabel')"
            :row-count="data.pagination.total_items + 1"
            grid-class="groups-record-grid"
          >
            <template #header>
              <span role="columnheader">{{ t('groups.collection.columns.group') }}</span>
              <span role="columnheader">{{ t('groups.collection.columns.status') }}</span>
              <span role="columnheader">{{ t('groups.collection.columns.upstream') }}</span>
              <span role="columnheader">{{ t('groups.collection.columns.models') }}</span>
              <span role="columnheader">{{ t('groups.collection.columns.keyHealth') }}</span>
              <span role="columnheader">{{ t('groups.collection.columns.actions') }}</span>
            </template>

            <article
              v-for="(group, index) in data.items"
              :key="group.id"
              class="ledger-record-list__record group-record"
              role="row"
              :aria-rowindex="(data.pagination.page - 1) * data.pagination.page_size + index + 2"
            >
              <div class="ledger-record-list__cell identity" role="cell">
                <RouterLink
                  class="group-name"
                  :to="groupDetailLocation(group.id)"
                  :aria-label="t('groups.collection.openDetail', { name: group.name })"
                >
                  <span class="group-id">#{{ group.id }}</span>
                  <span>{{ group.name }}</span>
                </RouterLink>
              </div>

              <div class="ledger-record-list__cell group-status" role="cell">
                <StatusBadge :status="group.status">
                  {{ t(`groups.collection.status.${group.status}`) }}
                </StatusBadge>
              </div>

              <div class="ledger-record-list__cell endpoint" role="cell">
                <CopyChip
                  :value="group.upstream_url"
                  :label="t('groups.collection.copyUrl', { url: group.upstream_url })"
                  :success-label="t('groups.collection.copySuccess')"
                  :failure-label="t('groups.collection.copyFailure')"
                />
                <div class="protocols">
                  <span v-for="protocol in group.protocols" :key="protocol" class="protocol">
                    {{ protocol }}
                  </span>
                </div>
              </div>

              <div class="ledger-record-list__cell model-count" role="cell">
                <span class="mobile-label">{{ t('groups.collection.columns.models') }}</span>
                <strong>{{ n(group.model_count) }}</strong>
              </div>

              <div class="ledger-record-list__cell key-health" role="cell">
                <span class="mobile-label">{{ t('groups.collection.columns.keyHealth') }}</span>
                <KeyHealthBar
                  :counts="group.key_counts"
                  :label="keyHealthLabel(group.key_counts)"
                />
              </div>

              <div class="ledger-record-list__cell record-actions" role="cell">
                <RouterLink
                  v-slot="{ navigate }"
                  :to="importLocation({ mode: 'existing', group_id: group.id })"
                  custom
                >
                  <AppButton
                    class="append-key"
                    role="link"
                    variant="secondary"
                    size="compact"
                    :aria-label="t('groups.collection.appendKeyFor', { name: group.name })"
                    @click="navigate"
                  >
                    <Plus :size="15" aria-hidden="true" />
                    <span class="append-key__label">{{ t('groups.collection.appendKey') }}</span>
                  </AppButton>
                </RouterLink>
                <RouterLink v-slot="{ navigate }" :to="groupDetailLocation(group.id)" custom>
                  <IconButton
                    role="link"
                    variant="ghost"
                    size="compact"
                    :label="t('groups.collection.openDetail', { name: group.name })"
                    @click="navigate"
                  >
                    <ArrowRight :size="15" aria-hidden="true" />
                  </IconButton>
                </RouterLink>
              </div>
            </article>
          </LedgerRecordList>

          <PaginationBar
            :page="data.pagination.page"
            :page-size="data.pagination.page_size"
            :total-items="data.pagination.total_items"
            :total-pages="data.pagination.total_pages"
            @previous="setPage(filters.page - 1)"
            @next="setPage(filters.page + 1)"
          />
        </template>
      </template>
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.groups-ledger :deep(.page-header) {
  border-bottom: 1px solid var(--color-border-control);
  padding-bottom: var(--space-5);
}

.stale-banner {
  margin-top: 14px;
}

.identity {
  min-width: 0;
}

.groups-record-grid {
  --ledger-record-list-grid: minmax(0, 1fr) 96px minmax(0, 1.55fr) 92px minmax(0, 1.25fr) 164px;
}

.group-name {
  display: inline-flex;
  max-width: 100%;
  min-width: 0;
  align-items: baseline;
  gap: 7px;
  overflow: hidden;
  color: var(--color-text);
  font-size: var(--text-body);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group-name:hover {
  color: var(--color-action);
}

.group-name > span:last-child {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.group-id {
  flex: none;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}

.endpoint {
  display: grid;
  justify-items: start;
  gap: var(--space-2);
}

.endpoint :deep(.copy-chip-wrap) {
  width: 100%;
}

.protocols {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px;
}

.protocol {
  display: inline-flex;
  min-height: 23px;
  align-items: center;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface-sunken);
  color: var(--color-text-faint);
  padding: 3px 7px;
  font-family: var(--font-mono);
  font-size: 11px;
  white-space: nowrap;
}

.model-count {
  display: grid;
  justify-items: start;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.model-count strong {
  color: var(--color-text);
  font-family: var(--font-mono);
  font-size: 16px;
  font-weight: 600;
  line-height: 1.25;
}

.key-health {
  display: grid;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}

.record-actions {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 6px;
}

.mobile-label {
  display: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}

.loading-state {
  display: grid;
  border-bottom: 1px solid var(--color-border-control);
}

.skeleton-row {
  display: grid;
  min-height: 96px;
  grid-template-columns: minmax(0, 1fr) 96px minmax(0, 1.55fr) 92px minmax(0, 1.25fr) 164px;
  align-items: center;
  gap: 16px;
  border-top: 1px solid var(--color-border-subtle);
}

.skeleton-row :deep(.skeleton-block:nth-child(2)) {
  width: 82%;
}

.skeleton-row :deep(.skeleton-block:nth-child(3)) {
  width: 58%;
}

.skeleton-row :deep(.skeleton-block:nth-child(4)) {
  width: 76%;
}

.skeleton-row :deep(.skeleton-block:nth-child(5)) {
  width: 64%;
}

.skeleton-row :deep(.skeleton-block:nth-child(6)) {
  width: 52%;
}

.collection-error {
  margin-top: var(--space-5);
}

@media (max-width: 1040px) {
  .groups-record-grid {
    --ledger-record-list-grid: minmax(0, 1fr) 84px minmax(0, 1.25fr) 76px minmax(0, 1.15fr) 72px;
    --ledger-record-list-column-gap: 12px;
  }

  .skeleton-row {
    grid-template-columns: minmax(0, 1fr) 84px minmax(0, 1.25fr) 76px minmax(0, 1.15fr) 72px;
    gap: 12px;
  }

  .append-key {
    width: 34px;
    min-width: 34px;
    padding: 0;
  }

  .append-key__label {
    display: none;
  }
}

@media (max-width: 860px) {
  .identity {
    grid-column: 1 / -1;
    padding-right: 72px;
  }

  .group-status {
    grid-column: 1 / -1;
  }

  .endpoint {
    grid-column: 1 / -1;
    border-top: 1px solid var(--color-border-subtle);
    border-bottom: 1px solid var(--color-border-subtle);
    padding: 12px 0;
  }

  .model-count {
    align-self: stretch;
    border-right: 1px solid var(--color-border-subtle);
    padding-right: 16px;
  }

  .model-count strong {
    font-size: 20px;
  }

  .mobile-label {
    display: inline;
  }

  .record-actions {
    position: absolute;
    top: 12px;
    right: 10px;
  }

  .record-actions :deep(.app-button),
  .record-actions :deep(.icon-button) {
    width: var(--touch-target);
    min-width: var(--touch-target);
    height: var(--touch-target);
    padding: 0;
  }

  .groups-ledger :deep(.empty-state .app-button),
  .groups-ledger :deep(.empty-state .button-link) {
    min-height: var(--touch-target);
  }

  .loading-state {
    gap: 10px;
    border: 0;
    padding-top: 10px;
  }

  .skeleton-row {
    min-height: 164px;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: 18px 16px;
    border: 1px solid var(--color-border-subtle);
    border-radius: var(--radius-control);
    padding: 16px;
  }
}

@media (max-width: 560px) {
  .groups-record-grid {
    --ledger-record-list-card-grid: 76px minmax(0, 1fr);
  }

  .identity {
    padding-right: 52px;
  }

  .group-name {
    font-size: 14px;
  }

  .model-count {
    padding-right: 12px;
  }

  .record-actions {
    gap: 2px;
  }
}
</style>
