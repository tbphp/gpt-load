<script setup lang="ts">
import { ArrowLeft, RefreshCw, Search } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  modelPriceCollectionQueryOptions,
  type ModelPriceDto,
  type ModelPriceFilters,
  type ModelPriceStatusFilter,
  type ModelPriceUsageFilter,
} from '@/app/resources/model-prices'
import { settingsLocation } from '@/app/route-locations'
import { useDebouncedAction } from '@/app/use-debounced-action'
import { useModelPriceSync } from '@/app/use-model-price-sync'
import { useVisibleRefetch } from '@/app/use-visible-refetch'
import CollectionFilterBar from '@/components/collection/CollectionFilterBar.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'

import ModelPriceCollection from './ModelPriceCollection.vue'
import ModelPriceDrawer from './ModelPriceDrawer.vue'

const client = useApiClient()
const { n, t } = useI18n()
const searchDraft = ref('')
const appliedSearch = ref<string | undefined>()
const usage = ref<ModelPriceUsageFilter>('in_use')
const status = ref<ModelPriceStatusFilter>('all')
const page = ref(1)
const pageSize = ref<20 | 50 | 100>(20)
const selected = ref<ModelPriceDto | null>(null)
const drawerOpen = ref(false)
let restoreFocus: HTMLElement | null = null
const searchDebounce = useDebouncedAction(250)
const {
  pending: syncPending,
  failed: syncFailure,
  succeeded: syncSucceeded,
  run: runSync,
} = useModelPriceSync()

const filters = computed<ModelPriceFilters>(() => ({
  usage: usage.value,
  status: status.value,
  search: appliedSearch.value,
  page: page.value,
  page_size: pageSize.value,
}))
const pricesQuery = useQuery(modelPriceCollectionQueryOptions(client, filters))
const data = computed(() => pricesQuery.data.value)
const collectionBusy = computed(() => data.value !== undefined && pricesQuery.isFetching.value)
const pendingRows = computed(() =>
  (data.value?.items ?? []).filter((row) => row.pricing_status === 'pending'),
)
const configuredRows = computed(() =>
  (data.value?.items ?? []).filter((row) => row.pricing_status === 'configured'),
)
const hasConditions = computed(
  () => appliedSearch.value !== undefined || usage.value !== 'in_use' || status.value !== 'all',
)
const usageOptions = computed(() =>
  (['in_use', 'unreferenced', 'all'] as const).map((value) => ({
    value,
    label: t(`modelPrices.filters.usage.${value}`),
  })),
)
const statusOptions = computed(() =>
  (['all', 'pending', 'configured'] as const).map((value) => ({
    value,
    label: t(`modelPrices.filters.status.${value}`),
  })),
)

watch(
  [() => data.value?.pagination.total_pages, page, () => pricesQuery.isPlaceholderData.value],
  ([totalPages, currentPage, placeholder]) => {
    if (placeholder || totalPages === undefined) return
    const lastPage = Math.max(1, totalPages)
    if (currentPage > lastPage) page.value = lastPage
  },
)

useVisibleRefetch([pricesQuery.refetch])

function scheduleSearch(): void {
  searchDebounce.schedule(() => {
    const normalized = searchDraft.value.trim().slice(0, 200)
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

function setUsage(value: string): void {
  usage.value = value as ModelPriceUsageFilter
  page.value = 1
}

function setStatus(value: string): void {
  status.value = value as ModelPriceStatusFilter
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
  usage.value = 'in_use'
  status.value = 'all'
  page.value = 1
}

function editRow(row: ModelPriceDto, trigger: HTMLElement): void {
  selected.value = row
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

onBeforeUnmount(() => {
  searchDebounce.cancel()
})
</script>

<template>
  <PageFrame aria-labelledby="model-prices-title">
    <LedgerSheet class="model-prices" :aria-busy="collectionBusy ? 'true' : undefined">
      <PageHeader id="model-prices-title" :title="t('modelPrices.title')">
        <template #actions>
          <RouterLink v-slot="{ navigate }" :to="settingsLocation()" custom>
            <AppButton role="link" variant="ghost" size="compact" @click="navigate">
              <ArrowLeft :size="15" aria-hidden="true" />{{ t('modelPrices.back') }}
            </AppButton>
          </RouterLink>
          <AppButton size="compact" :busy="syncPending" @click="runSync">
            <RefreshCw :size="15" aria-hidden="true" />{{ t('modelPrices.sync.action') }}
          </AppButton>
        </template>
      </PageHeader>

      <InlineFeedback v-if="syncSucceeded" appearance="ledger" tone="success">
        {{ t('modelPrices.sync.succeeded') }}
      </InlineFeedback>
      <InlineFeedback v-if="syncFailure" appearance="ledger" tone="danger">
        {{ t('modelPrices.sync.failed') }}
        <template #action>
          <AppButton variant="link" size="inline" @click="runSync">
            {{ t('common.retry') }}
          </AppButton>
        </template>
      </InlineFeedback>

      <QueryFeedback
        v-if="pricesQuery.isPending.value"
        state="loading"
        :message="t('modelPrices.loading')"
      />
      <QueryFeedback
        v-else-if="pricesQuery.isError.value && !data"
        state="error"
        :message="t('modelPrices.loadFailed')"
        :retry-label="t('common.retry')"
        @retry="pricesQuery.refetch()"
      />
      <template v-else-if="data">
        <QueryFeedback
          v-if="pricesQuery.isError.value"
          state="stale"
          :message="t('modelPrices.stale')"
          :retry-label="t('common.retry')"
          @retry="pricesQuery.refetch()"
        />

        <InlineFeedback appearance="ledger" tone="neutral">
          {{ t('modelPrices.context') }}
        </InlineFeedback>

        <CollectionFilterBar :label="t('modelPrices.filters.region')" :show-result="hasConditions">
          <label class="collection-filter-field collection-filter-field--search">
            <span class="collection-filter-label">{{ t('modelPrices.filters.searchLabel') }}</span>
            <AppSearchInput
              v-model="searchDraft"
              :label="t('modelPrices.filters.searchLabel')"
              :placeholder="t('modelPrices.filters.searchPlaceholder')"
              :clear-label="t('modelPrices.filters.clearSearch')"
              @update:model-value="scheduleSearch"
              @clear="clearSearch"
            />
          </label>
          <label class="collection-filter-field">
            <span class="collection-filter-label">{{ t('modelPrices.filters.usageLabel') }}</span>
            <AppSelect
              size="compact"
              :label="t('modelPrices.filters.usageLabel')"
              :model-value="usage"
              :options="usageOptions"
              @update:model-value="setUsage"
            />
          </label>
          <label class="collection-filter-field">
            <span class="collection-filter-label">{{ t('modelPrices.filters.statusLabel') }}</span>
            <AppSelect
              size="compact"
              :label="t('modelPrices.filters.statusLabel')"
              :model-value="status"
              :options="statusOptions"
              @update:model-value="setStatus"
            />
          </label>
          <template #result>
            <span aria-live="polite">
              {{
                t('modelPrices.result', {
                  shown: n(data.items.length),
                  total: n(data.pagination.total_items),
                })
              }}
            </span>
            <AppButton variant="link" size="inline" @click="resetConditions">
              {{ t('modelPrices.filters.reset') }}
            </AppButton>
          </template>
        </CollectionFilterBar>

        <EmptyState
          v-if="data.pagination.total_items === 0"
          variant="ledger"
          :title="
            hasConditions ? t('modelPrices.empty.noResultsTitle') : t('modelPrices.empty.title')
          "
          :description="
            hasConditions
              ? t('modelPrices.empty.noResultsDescription')
              : t('modelPrices.empty.description')
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
              {{ t('modelPrices.filters.reset') }}
            </AppButton>
            <AppButton v-else size="compact" :busy="syncPending" @click="runSync">
              <RefreshCw :size="15" aria-hidden="true" />{{ t('modelPrices.sync.action') }}
            </AppButton>
          </template>
        </EmptyState>

        <template v-else>
          <ModelPriceCollection
            v-if="pendingRows.length > 0"
            :rows="pendingRows"
            status="pending"
            @edit="editRow"
          />
          <ModelPriceCollection
            v-if="configuredRows.length > 0"
            :rows="configuredRows"
            status="configured"
            @edit="editRow"
          />

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
        :row="selected"
        @update:open="setDrawerOpen"
      />
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.model-prices {
  display: grid;
  min-width: 0;
  gap: var(--space-4-5);
}

.model-prices :deep(.collection-filter-bar) {
  padding-top: var(--space-1);
}

@media (max-width: 680px) {
  .model-prices :deep(.page-header__actions) {
    width: 100%;
  }
}
</style>
