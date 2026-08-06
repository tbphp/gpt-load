<script setup lang="ts">
import { ListFilter, RefreshCw, Search } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  modelCollectionQueryOptions,
  type ModelCollectionFilters,
  type ModelCollectionGroupStatus,
  type ModelCollectionPageSize,
  type ModelCollectionPricingStatus,
  type ModelPriceBranchDto,
} from '@/app/resources/models'
import { modelPricesLocation } from '@/app/route-locations'
import { useDebouncedAction } from '@/app/use-debounced-action'
import { useModelPriceSync } from '@/app/use-model-price-sync'
import { useVisibleRefetch } from '@/app/use-visible-refetch'
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
import ModelPriceDrawer from '@/features/model-prices/ModelPriceDrawer.vue'

import ModelTreeCollection from './ModelTreeCollection.vue'

const client = useApiClient()
const { locale, n, t } = useI18n()
const searchDraft = ref('')
const appliedSearch = ref<string | undefined>()
const groupStatus = ref<ModelCollectionGroupStatus>('enabled')
const pricingStatus = ref<ModelCollectionPricingStatus>('all')
const page = ref(1)
const pageSize = ref<ModelCollectionPageSize>(20)
const selected = ref<ModelPriceBranchDto | null>(null)
const drawerOpen = ref(false)
let restoreFocus: HTMLElement | null = null
const searchDebounce = useDebouncedAction(250)
const {
  pending: syncPending,
  failed: syncFailure,
  succeeded: syncSucceeded,
  run: runSync,
} = useModelPriceSync()

const filters = computed<ModelCollectionFilters>(() => ({
  group_status: groupStatus.value,
  pricing_status: pricingStatus.value,
  q: appliedSearch.value,
  page: page.value,
  page_size: pageSize.value,
}))
const modelsQuery = useQuery(modelCollectionQueryOptions(client, filters))
const data = computed(() => modelsQuery.data.value)
const collectionBusy = computed(() => data.value !== undefined && modelsQuery.isFetching.value)
const hasConditions = computed(
  () =>
    appliedSearch.value !== undefined ||
    groupStatus.value !== 'enabled' ||
    pricingStatus.value !== 'all',
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
  if (!data.value?.catalog.available) return 'neutral' as const
  return data.value.catalog.error_code ? ('warning' as const) : ('success' as const)
})
const catalogLabel = computed(() => {
  if (!data.value?.catalog.available) return t('models.catalog.unavailable')
  return data.value.catalog.error_code ? t('models.catalog.stale') : t('models.catalog.available')
})

watch(
  [() => data.value?.pagination.total_pages, page, () => modelsQuery.isPlaceholderData.value],
  ([totalPages, currentPage, placeholder]) => {
    if (placeholder || totalPages === undefined) return
    const lastPage = Math.max(1, totalPages)
    if (currentPage > lastPage) page.value = lastPage
  },
)

useVisibleRefetch([modelsQuery.refetch])

function scheduleSearch(): void {
  searchDebounce.schedule(() => {
    const normalized = [...searchDraft.value.trim()].slice(0, 200).join('')
    appliedSearch.value = normalized || undefined
    page.value = 1
  })
}

function clearSearch(): void {
  searchDebounce.cancel()
  searchDraft.value = ''
  appliedSearch.value = undefined
  page.value = 1
}

function setGroupStatus(value: string): void {
  groupStatus.value = value as ModelCollectionGroupStatus
  page.value = 1
}

function setPricingStatus(value: string): void {
  pricingStatus.value = value as ModelCollectionPricingStatus
  page.value = 1
}

function setPageSize(value: 20 | 50 | 100): void {
  pageSize.value = value
  page.value = 1
}

function resetConditions(): void {
  searchDebounce.cancel()
  searchDraft.value = ''
  appliedSearch.value = undefined
  groupStatus.value = 'enabled'
  pricingStatus.value = 'all'
  page.value = 1
}

function editScope(scope: ModelPriceBranchDto, trigger: HTMLElement): void {
  selected.value = scope
  restoreFocus = trigger
  drawerOpen.value = true
}

async function setDrawerOpen(open: boolean): Promise<void> {
  drawerOpen.value = open
  if (!open) {
    selected.value = null
    const target = restoreFocus
    restoreFocus = null
    await nextTick()
    target?.focus()
  }
}

onBeforeUnmount(() => searchDebounce.cancel())
</script>

<template>
  <PageFrame aria-labelledby="models-title">
    <LedgerSheet class="models-page" :aria-busy="collectionBusy ? 'true' : undefined">
      <PageHeader id="models-title" :title="t('models.title')">
        <template #actions>
          <RouterLink v-slot="{ navigate }" :to="modelPricesLocation()" custom>
            <AppButton role="link" variant="secondary" size="compact" @click="navigate">
              <ListFilter :size="15" aria-hidden="true" />{{ t('models.actions.priceRecords') }}
            </AppButton>
          </RouterLink>
          <AppButton size="compact" :busy="syncPending" @click="runSync">
            <RefreshCw :size="15" aria-hidden="true" />{{ t('models.actions.sync') }}
          </AppButton>
        </template>
      </PageHeader>

      <p class="models-page__description">{{ t('models.description') }}</p>

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

        <InlineFeedback appearance="ledger" tone="neutral">
          {{ t('models.context') }}
        </InlineFeedback>

        <section class="models-page__overview" :aria-label="t('models.summary.label')">
          <dl class="models-page__summary">
            <div>
              <dt>{{ t('models.summary.clientModels') }}</dt>
              <dd>{{ n(data.summary.client_model_count) }}</dd>
            </div>
            <div>
              <dt>{{ t('models.summary.upstreamModels') }}</dt>
              <dd>{{ n(data.summary.upstream_model_count) }}</dd>
            </div>
            <div>
              <dt>{{ t('models.summary.prices') }}</dt>
              <dd>{{ n(data.summary.price_count) }}</dd>
            </div>
            <div>
              <dt>{{ t('models.summary.pendingPrices') }}</dt>
              <dd>{{ n(data.summary.pending_price_count) }}</dd>
            </div>
            <div>
              <dt>{{ t('models.summary.unreferencedPrices') }}</dt>
              <dd>{{ n(data.summary.unreferenced_price_count) }}</dd>
            </div>
          </dl>
          <div class="models-page__catalog-status">
            <StatusBadge size="compact" :tone="catalogTone">{{ catalogLabel }}</StatusBadge>
            <span v-if="data.catalog.successful_fetch_at_ms > 0">
              {{ t('models.catalog.lastSuccess') }}
              <AppDateTime :instant="data.catalog.successful_fetch_at_ms" :locale="locale" />
            </span>
            <span v-if="data.catalog.checked_at_ms > 0">
              {{ t('models.catalog.lastCheck') }}
              <AppDateTime :instant="data.catalog.checked_at_ms" :locale="locale" />
            </span>
            <code v-if="data.catalog.error_code">{{ data.catalog.error_code }}</code>
          </div>
        </section>

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
              :model-value="groupStatus"
              :options="groupStatusOptions"
              @update:model-value="setGroupStatus"
            />
          </label>
          <label class="collection-filter-field">
            <span class="collection-filter-label">{{
              t('models.filters.pricingStatusLabel')
            }}</span>
            <AppSelect
              size="compact"
              :label="t('models.filters.pricingStatusLabel')"
              :model-value="pricingStatus"
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
            <AppButton v-else size="compact" :busy="syncPending" @click="runSync">
              <RefreshCw :size="15" aria-hidden="true" />{{ t('models.actions.sync') }}
            </AppButton>
          </template>
        </EmptyState>

        <template v-else>
          <ModelTreeCollection :items="data.items" @edit="editScope" />
          <PaginationBar
            :page="data.pagination.page"
            :page-size="data.pagination.page_size"
            :total-items="data.pagination.total_items"
            :total-pages="data.pagination.total_pages"
            show-page-size
            @previous="page -= 1"
            @next="page += 1"
            @update:page-size="setPageSize"
          />
        </template>
      </template>

      <ModelPriceDrawer
        v-if="drawerOpen && selected"
        :open="drawerOpen"
        :row="selected.price"
        :affected-groups="selected.affected_groups"
        @update:open="setDrawerOpen"
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

.models-page__description {
  max-width: 78ch;
  margin: calc(var(--space-4-5) * -0.35) 0 0;
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}

.models-page__overview {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-4);
  border-block: 1px solid var(--color-border-subtle);
  padding-block: var(--space-3);
}

.models-page__summary {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(5, minmax(96px, 1fr));
  gap: var(--space-3);
  margin: 0;
}

.models-page__summary div {
  display: grid;
  gap: 2px;
}

.models-page__summary dt,
.models-page__catalog-status span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.models-page__summary dd {
  margin: 0;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 650;
}

.models-page__catalog-status {
  display: grid;
  justify-items: end;
  gap: 3px;
}

.models-page__catalog-status code {
  color: var(--color-warning);
  font-size: var(--text-label-xs);
}

.models-page :deep(.models-page__filters.collection-filter-bar) {
  grid-template-columns: minmax(240px, 1fr) repeat(2, minmax(142px, 0.42fr));
  padding-top: var(--space-1);
}

@media (max-width: 980px) {
  .models-page__overview {
    grid-template-columns: minmax(0, 1fr);
  }

  .models-page__catalog-status {
    justify-items: start;
  }

  .models-page :deep(.models-page__filters.collection-filter-bar) {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .models-page :deep(.models-page__filters .collection-filter-field--search) {
    grid-column: 1 / -1;
  }
}

@media (max-width: 680px) {
  .models-page__summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .models-page :deep(.page-header__actions) {
    width: 100%;
  }

  .models-page :deep(.models-page__filters.collection-filter-bar) {
    grid-template-columns: minmax(0, 1fr);
  }

  .models-page :deep(.models-page__filters .collection-filter-field--search) {
    grid-column: auto;
  }
}
</style>
