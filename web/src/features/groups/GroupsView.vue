<script setup lang="ts">
import { ArrowRight, KeyRound, Layers3, Plus, Search, TriangleAlert } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type {
  CredentialCounts,
  GroupCollectionFilters,
  GroupCollectionSort,
  GroupCollectionStatus,
} from '@/api/control/types'
import { channelsQueryOptions, type ChannelDto } from '@/app/resources/channels'
import { groupCollectionQueryOptions } from '@/app/resources/groups'
import { groupDetailLocation, groupsLocation, importLocation } from '@/app/route-locations'
import { useCollectionLoading } from '@/app/loading-state'
import { useDebouncedAction } from '@/app/use-debounced-action'
import { useVisibleRefetch } from '@/app/use-visible-refetch'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import CollectionFilterBar from '@/components/collection/CollectionFilterBar.vue'
import CollectionStatusSummary from '@/components/collection/CollectionStatusSummary.vue'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import IconButton from '@/components/ui/IconButton.vue'
import CredentialHealthBar from '@/components/ui/CredentialHealthBar.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import {
  constrainGroupCollectionSearchQuery,
  isCanonicalGroupCollectionRouteQuery,
  parseGroupCollectionRouteQuery,
  serializeGroupCollectionRouteQuery,
} from './group-collection-route'

const sortOptions: readonly GroupCollectionSort[] = ['status', 'name', 'credentials', 'created']

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const { n, t } = useI18n()
const filters = computed(() => parseGroupCollectionRouteQuery(route.query))
const searchDraft = ref(filters.value.q ?? '')
const groupsQuery = useQuery(groupCollectionQueryOptions(client, filters))
const channelsQuery = useQuery(channelsQueryOptions(client, ''))
const channelsByID = computed<Record<string, ChannelDto>>(() =>
  Object.fromEntries(
    (channelsQuery.data.value?.items ?? []).map((channel) => [channel.channel_id, channel]),
  ),
)
const searchDebounce = useDebouncedAction(250)

const data = computed(() => groupsQuery.data.value)
const hasFilterCriteria = computed(
  () => filters.value.q !== undefined || filters.value.status !== undefined,
)
const hasChangedConditions = computed(
  () => hasFilterCriteria.value || filters.value.sort !== 'status',
)
const collectionBusy = computed(() => data.value !== undefined && groupsQuery.isFetching.value)
const {
  initial: initialLoading,
  transition: collectionTransition,
  refreshing: collectionRefreshing,
  rows: skeletonRows,
} = useCollectionLoading(
  {
    pending: () => groupsQuery.isPending.value,
    placeholder: () => groupsQuery.isPlaceholderData.value,
    fetching: () => groupsQuery.isFetching.value,
    hasData: () => data.value !== undefined,
    itemCount: () => data.value?.items.length ?? 0,
  },
  { fallbackRows: 20 },
)
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
    searchDebounce.cancel()
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
  patch: Partial<Pick<GroupCollectionFilters, 'q' | 'status' | 'sort'>>,
): void {
  const q = constrainGroupCollectionSearchQuery(searchDraft.value)
  routeWithFilters({ ...filters.value, q, ...patch, page: 1 })
}

function scheduleSearch(): void {
  searchDebounce.schedule(() => {
    updateConditions({ q: constrainGroupCollectionSearchQuery(searchDraft.value) })
  })
}

function clearSearch(): void {
  searchDebounce.cancel()
  searchDraft.value = ''
  updateConditions({ q: undefined })
}

function setStatus(status: string | undefined): void {
  updateConditions({ status: status as GroupCollectionStatus | undefined })
}

function setSort(value: string): void {
  updateConditions({ sort: value as GroupCollectionSort })
}

function resetConditions(): void {
  searchDebounce.cancel()
  searchDraft.value = ''
  routeWithFilters({ sort: 'status', page: 1, page_size: 20 })
}

function setPage(page: number): void {
  routeWithFilters({ ...filters.value, page })
}

function credentialHealthLabel(counts: CredentialCounts): string {
  return t('groups.collection.credentialHealthLabel', {
    total: n(counts.total),
    available: n(counts.available),
    cooldown: n(counts.cooldown),
    blacklisted: n(counts.blacklisted),
    disabled: n(counts.disabled),
  })
}

function channelName(channelID: string): string {
  return channelDefinition(channelID)?.name ?? channelID
}

function channelDefinition(channelID: string): ChannelDto | null {
  return channelsByID.value[channelID] ?? null
}
</script>

<template>
  <PageFrame aria-labelledby="groups-title">
    <LedgerSheet class="groups-ledger" :aria-busy="collectionBusy ? 'true' : undefined">
      <PageHeader id="groups-title" :title="t('groups.title')">
        <template #actions>
          <RouterLink v-slot="{ navigate }" :to="importLocation()" custom>
            <AppButton role="link" @click="navigate">
              <KeyRound :size="16" aria-hidden="true" />
              {{ t('groups.collection.importCredentials') }}
            </AppButton>
          </RouterLink>
        </template>
      </PageHeader>

      <AsyncRefreshIndicator
        :active="collectionRefreshing"
        :label="t('groups.collection.loading')"
      />

      <SkeletonSurface
        v-if="groupsQuery.isPending.value || initialLoading"
        variant="collection"
        :rows="filters.page_size"
        :columns="6"
        row-height="96px"
        show-controls
        :concealed="!initialLoading"
        :label="t('groups.collection.loading')"
      />

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
              <AppSearchInput
                v-model="searchDraft"
                :label="t('groups.collection.filters.searchLabel')"
                :placeholder="t('groups.collection.filters.searchPlaceholder')"
                :clear-label="t('groups.collection.filters.clearSearch')"
                @update:model-value="scheduleSearch"
                @clear="clearSearch"
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

        <SkeletonSurface
          v-if="collectionTransition"
          variant="collection"
          :rows="skeletonRows"
          :columns="6"
          row-height="96px"
          :label="t('groups.collection.loading')"
        />

        <EmptyState
          v-else-if="data.summary.total === 0"
          :title="t('groups.collection.emptyTitle')"
          :description="t('groups.collection.emptyDescription')"
          variant="ledger"
        >
          <template #icon><Layers3 :size="20" /></template>
          <template #actions>
            <RouterLink class="button-link" :to="importLocation()">
              <KeyRound :size="15" aria-hidden="true" />
              {{ t('groups.collection.importCredentials') }}
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
              <span role="columnheader">{{ t('groups.collection.columns.channel') }}</span>
              <span role="columnheader">{{ t('groups.collection.columns.models') }}</span>
              <span role="columnheader">{{ t('groups.collection.columns.credentialHealth') }}</span>
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
                <OverflowTooltip
                  :as="RouterLink"
                  class="group-name"
                  :content="group.name"
                  measure-selector=".group-name__label"
                  :to="groupDetailLocation(group.id)"
                  :aria-label="t('groups.collection.openDetail', { name: group.name })"
                >
                  <span class="group-id">#{{ group.id }}</span>
                  <span class="group-name__label">{{ group.name }}</span>
                </OverflowTooltip>
              </div>

              <div class="ledger-record-list__cell group-status" role="cell">
                <StatusBadge :status="group.status">
                  {{ t(`groups.collection.status.${group.status}`) }}
                </StatusBadge>
              </div>

              <div class="ledger-record-list__cell endpoint" role="cell">
                <span class="channel-heading">
                  <ChannelIcon
                    v-if="channelDefinition(group.channel_id)"
                    class="channel-icon"
                    :icon="channelDefinition(group.channel_id)!.icon"
                    :mark="channelDefinition(group.channel_id)!.mark"
                  />
                  <OverflowTooltip
                    as="strong"
                    class="channel-name"
                    :content="channelName(group.channel_id)"
                    :focusable="false"
                  >
                    {{ channelName(group.channel_id) }}
                  </OverflowTooltip>
                </span>
                <CopyChip
                  v-if="group.params.base_url"
                  :value="group.params.base_url"
                  :label="t('groups.collection.copyUrl', { url: group.params.base_url })"
                  :success-label="t('groups.collection.copySuccess')"
                  :failure-label="t('groups.collection.copyFailure')"
                />
              </div>

              <div class="ledger-record-list__cell model-count" role="cell">
                <span class="mobile-label">{{ t('groups.collection.columns.models') }}</span>
                <strong>{{ n(group.model_count) }}</strong>
              </div>

              <div class="ledger-record-list__cell credential-health" role="cell">
                <span class="mobile-label">{{
                  t('groups.collection.columns.credentialHealth')
                }}</span>
                <CredentialHealthBar
                  :counts="group.credential_counts"
                  :label="credentialHealthLabel(group.credential_counts)"
                />
              </div>

              <div class="ledger-record-list__cell record-actions" role="cell">
                <RouterLink
                  v-slot="{ navigate }"
                  :to="importLocation({ mode: 'existing', group_id: group.id })"
                  custom
                >
                  <AppButton
                    class="append-credential"
                    role="link"
                    variant="secondary"
                    size="compact"
                    :aria-label="t('groups.collection.appendCredentialFor', { name: group.name })"
                    @click="navigate"
                  >
                    <Plus :size="15" aria-hidden="true" />
                    <span class="append-credential__label">{{
                      t('groups.collection.appendCredential')
                    }}</span>
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
            :pending="collectionBusy"
            @previous="setPage(filters.page - 1)"
            @next="setPage(filters.page + 1)"
          />
        </template>
      </template>
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
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

.channel-heading {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: 6px;
  overflow: hidden;
}

.channel-icon {
  flex: none;
  font-size: 18px;
}

.channel-name {
  min-width: 0;
  overflow: hidden;
  color: var(--color-text);
  font-size: var(--title-section);
  line-height: 1.25;
  text-overflow: ellipsis;
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

.credential-health {
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

.collection-error {
  margin-top: var(--space-5);
}

@media (max-width: 1040px) {
  .groups-record-grid {
    --ledger-record-list-grid: minmax(0, 1fr) 84px minmax(0, 1.25fr) 76px minmax(0, 1.15fr) 72px;
    --ledger-record-list-column-gap: 12px;
  }

  .append-credential {
    width: 34px;
    min-width: 34px;
    padding: 0;
  }

  .append-credential__label {
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
