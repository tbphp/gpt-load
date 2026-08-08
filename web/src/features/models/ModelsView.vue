<script setup lang="ts">
import { RefreshCw, Search } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import {
  modelCollectionQueryOptions,
  type ModelCollectionGroupStatus,
  type ModelCollectionPricingStatus,
  type ModelUpstreamDto,
} from '@/app/resources/models'
import { constrainCollectionSearch } from '@/app/route-query'
import { useDebouncedAction } from '@/app/use-debounced-action'
import { useModelPriceSync } from '@/app/use-model-price-sync'
import { useVisibleRefetch } from '@/app/use-visible-refetch'
import { groupsLocation, modelsLocation } from '@/app/route-locations'
import CollectionFilterBar from '@/components/collection/CollectionFilterBar.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import ModelTree from './ModelTree.vue'
import ModelUpstreamDrawer from './ModelUpstreamDrawer.vue'
import {
  isCanonicalModelsRouteQuery,
  parseModelsRouteQuery,
  serializeModelsRouteQuery,
  type ModelsRouteState,
} from './models-route'

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const { locale, n, t } = useI18n()
const routeState = computed(() => parseModelsRouteQuery(route.query))
const filters = computed(() => routeState.value.filters)
const searchDraft = ref(filters.value.q ?? '')
const searchDebounce = useDebouncedAction(250)
const {
  pending: syncPending,
  failed: syncFailure,
  succeeded: syncSucceeded,
  run: runSync,
} = useModelPriceSync()

const drawerOpen = computed(() => routeState.value.selectedPriceID !== undefined)
const activePriceID = computed(() => routeState.value.selectedPriceID ?? null)
const drawer = ref<InstanceType<typeof ModelUpstreamDrawer>>()

const modelsQuery = useQuery(modelCollectionQueryOptions(client, filters))
const data = computed(() => modelsQuery.data.value)
const collectionBusy = computed(() => data.value !== undefined && modelsQuery.isFetching.value)
const hasConditions = computed(
  () =>
    filters.value.q !== undefined ||
    filters.value.group_status !== 'enabled' ||
    filters.value.pricing_status !== 'all',
)
const groupStatusOptions = computed(() =>
  (['enabled', 'all'] as const).map((value) => ({
    value,
    label: t(`models.filters.groupStatus.${value}`),
  })),
)
const pricingStatusOptions = computed(() =>
  (['all', 'pending', 'configured'] as const).map((value) => ({
    value,
    label: t(`models.filters.pricingStatus.${value}`),
  })),
)
const catalogTone = computed(() => {
  if (!data.value) return 'neutral' as const
  if (!data.value.catalog.available) return 'neutral' as const
  return data.value.catalog.error_code ? ('warning' as const) : ('success' as const)
})
const catalogLabel = computed(() => {
  if (!data.value) return ''
  if (!data.value.catalog.available) return t('models.catalog.unavailable')
  return data.value.catalog.error_code ? t('models.catalog.stale') : t('models.catalog.available')
})

watch(
  [
    () => data.value?.pagination.total_pages,
    () => filters.value.page,
    () => modelsQuery.isPlaceholderData.value,
  ],
  ([totalPages, currentPage, placeholder]) => {
    if (placeholder || totalPages === undefined) return
    const lastPage = Math.max(1, totalPages)
    if (currentPage > lastPage) {
      navigate({ filters: { ...filters.value, page: lastPage } }, true)
    }
  },
)

useVisibleRefetch([modelsQuery.refetch])

watch(
  () => route.query,
  (query) => {
    searchDebounce.cancel()
    const state = parseModelsRouteQuery(query)
    searchDraft.value = state.filters.q ?? ''
    if (!isCanonicalModelsRouteQuery(query, state)) {
      void router.replace(modelsLocation(serializeModelsRouteQuery(state)))
    }
  },
  { deep: true, immediate: true },
)

function navigate(patch: Partial<ModelsRouteState>, replace = false): void {
  const next = { ...routeState.value, ...patch }
  const location = modelsLocation(serializeModelsRouteQuery(next))
  void (replace ? router.replace(location) : router.push(location))
}

function scheduleSearch(): void {
  searchDebounce.schedule(() => {
    void applySearch()
  })
}

async function applySearch(): Promise<void> {
  const normalized = constrainCollectionSearch(searchDraft.value)
  if (normalized === filters.value.q) return
  if (!(await confirmDiscard())) {
    searchDraft.value = filters.value.q ?? ''
    return
  }
  navigate({
    filters: { ...filters.value, q: normalized, page: 1 },
    selectedPriceID: undefined,
  })
}

async function clearSearch(): Promise<void> {
  searchDebounce.cancel()
  if (filters.value.q === undefined) {
    searchDraft.value = ''
    return
  }
  if (!(await confirmDiscard())) {
    searchDraft.value = filters.value.q ?? ''
    return
  }
  searchDraft.value = ''
  navigate({
    filters: { ...filters.value, q: undefined, page: 1 },
    selectedPriceID: undefined,
  })
}

async function setGroupStatus(value: string): Promise<void> {
  if (value === filters.value.group_status || !(await confirmDiscard())) return
  navigate({
    filters: { ...filters.value, group_status: value as ModelCollectionGroupStatus, page: 1 },
    selectedPriceID: undefined,
  })
}

async function setPricingStatus(value: string): Promise<void> {
  if (value === filters.value.pricing_status || !(await confirmDiscard())) return
  navigate({
    filters: {
      ...filters.value,
      pricing_status: value as ModelCollectionPricingStatus,
      page: 1,
    },
    selectedPriceID: undefined,
  })
}

async function resetConditions(): Promise<void> {
  if (!hasConditions.value || !(await confirmDiscard())) return
  searchDebounce.cancel()
  searchDraft.value = ''
  navigate({
    filters: { group_status: 'enabled', pricing_status: 'all', page: 1, page_size: 10 },
    selectedPriceID: undefined,
  })
}

async function showPendingModels(): Promise<void> {
  await setPricingStatus('pending')
}

async function changePage(nextPage: number): Promise<void> {
  if (nextPage === filters.value.page || !(await confirmDiscard())) return
  navigate({ filters: { ...filters.value, page: nextPage }, selectedPriceID: undefined })
}

/** 列表条件变化会让抽屉里的草稿失去上下文，先确认再放弃并关闭。 */
async function confirmDiscard(): Promise<boolean> {
  if (!drawerOpen.value || !drawer.value) return true
  if (!(await drawer.value.confirmDiscardSwitch())) return false
  drawer.value.discardChanges()
  return true
}

async function openUpstream(upstream: ModelUpstreamDto): Promise<void> {
  if (upstream.price.id === activePriceID.value && drawerOpen.value) return
  if (drawerOpen.value && drawer.value && !(await drawer.value.confirmDiscardSwitch())) return
  drawer.value?.discardChanges()
  navigate({ selectedPriceID: upstream.price.id })
}

function closeDrawer(): void {
  navigate({ selectedPriceID: undefined })
}

function goToGroups(): void {
  void router.push(groupsLocation())
}
</script>

<template>
  <PageFrame aria-labelledby="models-title">
    <LedgerSheet class="models-page" :aria-busy="collectionBusy ? 'true' : undefined">
      <PageHeader id="models-title" :title="t('models.title')">
        <template #actions>
          <AppButton size="compact" :busy="syncPending" @click="runSync">
            <RefreshCw :size="15" aria-hidden="true" />{{ t('models.actions.sync') }}
          </AppButton>
        </template>
      </PageHeader>

      <InlineFeedback v-if="syncSucceeded" appearance="ledger" tone="success">
        {{ t('models.sync.succeeded') }}
      </InlineFeedback>
      <InlineFeedback v-if="syncFailure" appearance="ledger" tone="danger">
        {{ t('models.sync.failed') }}
        <template #action>
          <AppButton variant="link" size="inline" @click="runSync">
            {{ t('common.retry') }}
          </AppButton>
        </template>
      </InlineFeedback>

      <QueryFeedback
        v-if="modelsQuery.isPending.value"
        state="loading"
        :message="t('models.loading')"
      />
      <QueryFeedback
        v-else-if="modelsQuery.isError.value && !data"
        state="error"
        :message="t('models.loadFailed')"
        :retry-label="t('common.retry')"
        @retry="modelsQuery.refetch()"
      />
      <template v-else-if="data">
        <QueryFeedback
          v-if="modelsQuery.isError.value"
          state="stale"
          :message="t('models.stale')"
          :retry-label="t('common.retry')"
          @retry="modelsQuery.refetch()"
        />

        <p class="models-page__status" aria-live="polite">
          <span>{{
            t('models.status.models', { count: n(data.summary.client_model_count) })
          }}</span>
          <span aria-hidden="true">·</span>
          <span>{{
            t('models.status.upstreams', { count: n(data.summary.upstream_model_count) })
          }}</span>
          <span aria-hidden="true">·</span>
          <AppButton
            v-if="data.summary.pending_price_count > 0"
            variant="link"
            size="inline"
            @click="showPendingModels"
          >
            {{ t('models.status.pending', { count: n(data.summary.pending_price_count) }) }}
          </AppButton>
          <span v-else>
            {{ t('models.status.pending', { count: n(data.summary.pending_price_count) }) }}
          </span>
          <span aria-hidden="true">·</span>
          <span :title="t('models.context')">{{ t('models.status.unit') }}</span>
          <span aria-hidden="true">·</span>
          <StatusBadge size="compact" :tone="catalogTone">{{ catalogLabel }}</StatusBadge>
          <span v-if="data.catalog.successful_fetch_at_ms > 0" class="models-page__status-time">
            <AppDateTime :instant="data.catalog.successful_fetch_at_ms" :locale="locale" />
          </span>
        </p>

        <CollectionFilterBar
          class="models-page__filters"
          :label="t('models.filters.region')"
          :show-result="hasConditions"
        >
          <label class="collection-filter-field collection-filter-field--search">
            <span class="collection-filter-label">{{ t('models.filters.searchLabel') }}</span>
            <AppSearchInput
              v-model="searchDraft"
              :label="t('models.filters.searchLabel')"
              :placeholder="t('models.filters.searchPlaceholder')"
              :clear-label="t('models.filters.clearSearch')"
              @update:model-value="scheduleSearch"
              @clear="clearSearch"
            />
          </label>
          <label class="collection-filter-field">
            <span class="collection-filter-label">{{ t('models.filters.groupStatusLabel') }}</span>
            <AppSelect
              size="compact"
              :label="t('models.filters.groupStatusLabel')"
              :model-value="filters.group_status"
              :options="groupStatusOptions"
              @update:model-value="setGroupStatus"
            />
          </label>
          <label class="collection-filter-field">
            <span class="collection-filter-label">
              {{ t('models.filters.pricingStatusLabel') }}
            </span>
            <AppSelect
              size="compact"
              :label="t('models.filters.pricingStatusLabel')"
              :model-value="filters.pricing_status"
              :options="pricingStatusOptions"
              @update:model-value="setPricingStatus"
            />
          </label>
          <template #result>
            <span aria-live="polite">
              {{
                t('models.result', {
                  shown: n(data.items.length),
                  total: n(data.pagination.total_items),
                })
              }}
            </span>
            <AppButton variant="link" size="inline" @click="resetConditions">
              {{ t('models.filters.reset') }}
            </AppButton>
          </template>
        </CollectionFilterBar>

        <EmptyState
          v-if="data.pagination.total_items === 0"
          variant="ledger"
          :title="hasConditions ? t('models.empty.noResultsTitle') : t('models.empty.title')"
          :description="
            hasConditions ? t('models.empty.noResultsDescription') : t('models.empty.description')
          "
        >
          <template #icon>
            <Search v-if="hasConditions" :size="20" aria-hidden="true" />
            <RefreshCw v-else :size="20" aria-hidden="true" />
          </template>
          <template #actions>
            <AppButton
              v-if="hasConditions"
              variant="secondary"
              size="compact"
              @click="resetConditions"
            >
              {{ t('models.filters.reset') }}
            </AppButton>
            <AppButton v-else size="compact" @click="goToGroups">
              {{ t('models.actions.configureGroups') }}
            </AppButton>
          </template>
        </EmptyState>

        <template v-else>
          <ModelTree :items="data.items" @open="openUpstream" />
          <PaginationBar
            :page="data.pagination.page"
            :page-size="data.pagination.page_size"
            :total-items="data.pagination.total_items"
            :total-pages="data.pagination.total_pages"
            @previous="changePage(filters.page - 1)"
            @next="changePage(filters.page + 1)"
          />
        </template>
      </template>

      <ModelUpstreamDrawer
        ref="drawer"
        :open="drawerOpen"
        :price-id="activePriceID"
        @close="closeDrawer"
      />
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.models-page {
  display: grid;
  min-width: 0;
  gap: var(--space-4-5);
}

.models-page__status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1-75);
  margin: calc(var(--space-4-5) * -0.35) 0 0;
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}

.models-page__status > span[aria-hidden='true'] {
  color: var(--color-text-faint);
}

.models-page__status-time {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.models-page :deep(.models-page__filters.collection-filter-bar) {
  grid-template-columns: minmax(240px, 1fr) repeat(2, minmax(142px, 0.42fr));
  padding-top: var(--space-1);
}

@media (max-width: 980px) {
  .models-page :deep(.models-page__filters.collection-filter-bar) {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .models-page :deep(.models-page__filters .collection-filter-field--search) {
    grid-column: 1 / -1;
  }
}

@media (max-width: 680px) {
  .models-page :deep(.models-page__filters.collection-filter-bar) {
    grid-template-columns: minmax(0, 1fr);
  }

  .models-page :deep(.models-page__filters .collection-filter-field--search) {
    grid-column: auto;
  }
}
</style>
