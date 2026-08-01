<script setup lang="ts">
import { KeyRound, Plus, Search, TriangleAlert, X } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { lazySurface } from '@/app/async-surface'
import { accessKeyCollectionQueryOptions, accessKeyResources } from '@/app/resources/access-keys'
import { groupOptionsQueryOptions } from '@/app/resources/groups'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { accessKeysLocation } from '@/app/route-locations'
import { useVisibleRefetch } from '@/app/use-visible-refetch'
import CollectionFilterBar from '@/components/collection/CollectionFilterBar.vue'
import CollectionStatusSummary from '@/components/collection/CollectionStatusSummary.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import type {
  AccessKeyCollectionFilters,
  AccessKeyCollectionScope,
  AccessKeyCollectionStatus,
  AccessKeyDto,
} from '@/api/control/types'

import AccessKeyCollection from './AccessKeyCollection.vue'
import type { PendingAccessKeyCreateOperation } from './access-key-create-operation'
import {
  constrainAccessKeyCollectionSearchQuery,
  isCanonicalAccessKeyCollectionRouteQuery,
  parseAccessKeyCollectionRouteQuery,
  serializeAccessKeyCollectionRouteQuery,
} from './access-key-collection-route'
import type { PendingAccessKeyEditOperation } from './access-key-edit-operation'

const AccessKeyDrawer = lazySurface(() => import('./AccessKeyDrawer.vue'))

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const queryClient = useQueryClient()
const { n, t } = useI18n()
const filters = computed(() => parseAccessKeyCollectionRouteQuery(route.query))
const searchDraft = ref(filters.value.q ?? '')
const drawerOpen = ref(false)
const selected = ref<AccessKeyDto | null>(null)
const createOperation = ref<PendingAccessKeyCreateOperation | null>(null)
const editOperation = ref<PendingAccessKeyEditOperation | null>(null)
const viewRoot = ref<HTMLElement | null>(null)
const collection = ref<InstanceType<typeof AccessKeyCollection> | null>(null)
const deletionAnnouncement = ref('')
const accessKeysQuery = useQuery(accessKeyCollectionQueryOptions(client, filters))
const groupsQuery = useQuery(groupOptionsQueryOptions(client))
const data = computed(() => accessKeysQuery.data.value)
const collectionBusy = computed(() => data.value !== undefined && accessKeysQuery.isFetching.value)
const hasFilterCriteria = computed(
  () =>
    filters.value.q !== undefined ||
    filters.value.status !== undefined ||
    filters.value.scope !== undefined,
)
const statusSummaryItems = computed(() => {
  const summary = data.value?.summary
  if (!summary) return []
  return [
    {
      value: undefined,
      label: t('accessKeys.collection.status.all'),
      count: summary.total,
      tone: 'neutral' as const,
    },
    {
      value: 'active',
      label: t('accessKeys.collection.status.active'),
      count: summary.active,
      tone: 'success' as const,
    },
    {
      value: 'disabled',
      label: t('accessKeys.collection.status.disabled'),
      count: summary.disabled,
      tone: 'neutral' as const,
    },
  ]
})
const scopeSelectOptions = computed(() => [
  { value: 'all', label: t('accessKeys.collection.filters.allScopes') },
  { value: 'unlimited', label: t('accessKeys.collection.scope.unlimited') },
  { value: 'restricted', label: t('accessKeys.collection.scope.restricted') },
])
const groupCatalogState = computed(() => {
  if (groupsQuery.isError.value) return groupsQuery.data.value ? 'stale' : 'error'
  if (groupsQuery.isPending.value) return 'loading'
  return 'ready'
})
const operationNoticeKey = computed(() =>
  createOperation.value?.state === 'reconciling'
    ? 'accessKeys.operation.reconciling'
    : 'accessKeys.operation.indeterminate',
)
const editOperationNoticeKey = computed(() =>
  editOperation.value?.state === 'reconciling'
    ? 'accessKeys.operation.editReconciling'
    : 'accessKeys.operation.editIndeterminate',
)
const editOperationName = computed(
  () => editOperation.value?.patch.name ?? editOperation.value?.base.name ?? '',
)
let restoreFocus: HTMLElement | null = null
let searchTimer: ReturnType<typeof setTimeout> | undefined
let mounted = true

watch(
  () => route.query,
  (query) => {
    if (searchTimer !== undefined) {
      clearTimeout(searchTimer)
      searchTimer = undefined
    }
    const parsed = parseAccessKeyCollectionRouteQuery(query)
    searchDraft.value = parsed.q ?? ''
    if (!isCanonicalAccessKeyCollectionRouteQuery(query, parsed)) {
      void router.replace(accessKeysLocation(serializeAccessKeyCollectionRouteQuery(parsed)))
    }
  },
  { deep: true, immediate: true },
)

watch(filters, () => collection.value?.conceal())

watch(
  [
    () => data.value?.pagination.total_pages,
    () => filters.value.page,
    () => accessKeysQuery.isPlaceholderData.value,
  ],
  ([totalPages, page, isPlaceholderData]) => {
    if (!isPlaceholderData && totalPages !== undefined && totalPages > 0 && page > totalPages) {
      void router.replace(
        accessKeysLocation(
          serializeAccessKeyCollectionRouteQuery({ ...filters.value, page: totalPages }),
        ),
      )
    }
  },
)

useVisibleRefetch([accessKeysQuery.refetch])

onBeforeUnmount(() => {
  mounted = false
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  queryClient.removeQueries({ queryKey: accessKeyResources.collection.queryKey })
})

function routeWithFilters(next: AccessKeyCollectionFilters, replace = false): void {
  collection.value?.conceal()
  const location = accessKeysLocation(serializeAccessKeyCollectionRouteQuery(next))
  void (replace ? router.replace(location) : router.push(location))
}

function updateConditions(
  patch: Partial<Pick<AccessKeyCollectionFilters, 'q' | 'status' | 'scope'>>,
): void {
  routeWithFilters({
    ...filters.value,
    q: constrainAccessKeyCollectionSearchQuery(searchDraft.value),
    ...patch,
    page: 1,
  })
}

function scheduleSearch(): void {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchTimer = undefined
    updateConditions({ q: constrainAccessKeyCollectionSearchQuery(searchDraft.value) })
  }, 300)
}

function clearSearch(): void {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  searchTimer = undefined
  searchDraft.value = ''
  updateConditions({ q: undefined })
}

function setStatus(status: string | undefined): void {
  updateConditions({ status: status as AccessKeyCollectionStatus | undefined })
}

function setScope(scope: string): void {
  updateConditions({ scope: scope === 'all' ? undefined : (scope as AccessKeyCollectionScope) })
}

function resetConditions(): void {
  if (searchTimer !== undefined) clearTimeout(searchTimer)
  searchTimer = undefined
  searchDraft.value = ''
  routeWithFilters({ page: 1, page_size: 20 })
}

function setPage(page: number): void {
  routeWithFilters({ ...filters.value, page })
}

function createKey(): void {
  selected.value = null
  restoreFocus = null
  drawerOpen.value = true
}

function editKey(accessKey: AccessKeyDto, trigger: HTMLElement): void {
  if (editOperation.value && editOperation.value.base.id !== accessKey.id) {
    checkEditOperation()
    return
  }
  selected.value = accessKey
  restoreFocus = trigger
  drawerOpen.value = true
}

function setCreateOperation(operation: PendingAccessKeyCreateOperation | null): void {
  createOperation.value = operation
}

function setEditOperation(operation: PendingAccessKeyEditOperation | null): void {
  editOperation.value = operation
}

function checkCreateOperation(): void {
  if (!createOperation.value) return
  selected.value = null
  restoreFocus = null
  drawerOpen.value = true
}

function checkEditOperation(): void {
  const operation = editOperation.value
  if (!operation) return
  selected.value =
    data.value?.items.find((accessKey) => accessKey.id === operation.base.id) ?? operation.base
  restoreFocus = null
  drawerOpen.value = true
}

async function setDrawerOpen(open: boolean): Promise<void> {
  drawerOpen.value = open
  if (!open) {
    collection.value?.conceal()
    selected.value = null
    const target = restoreFocus
    restoreFocus = null
    await nextTick()
    target?.focus()
  }
}

async function focusCreateAfterDelete(name: string): Promise<void> {
  deletionAnnouncement.value = ''
  await nextTick()
  await applyInvalidationPlan(queryClient, mutationInvalidationPlans.accessKey.delete)
  await nextTick()
  if (!mounted) return
  deletionAnnouncement.value = t('accessKeys.delete.deletedAnnouncement', { name })
  const target = viewRoot.value?.querySelector('button.access-key-create')
  if (target instanceof HTMLButtonElement && target.isConnected) target.focus()
}
</script>

<template>
  <section ref="viewRoot" aria-labelledby="access-keys-title">
    <PageFrame>
      <LedgerSheet class="access-keys-ledger" :aria-busy="collectionBusy ? 'true' : undefined">
        <PageHeader
          id="access-keys-title"
          :title="t('accessKeys.title')"
          :description="t('accessKeys.description')"
        >
          <template #actions>
            <AppButton class="access-key-create" @click="createKey">
              <Plus :size="16" aria-hidden="true" />{{ t('accessKeys.create') }}
            </AppButton>
          </template>
        </PageHeader>

        <AccessKeyDrawer
          v-if="drawerOpen"
          :open="drawerOpen"
          :access-key="selected"
          :groups="groupsQuery.data.value ?? []"
          :group-catalog-state="groupCatalogState"
          :create-operation="createOperation"
          :edit-operation="selected?.id === editOperation?.base.id ? editOperation : null"
          @update:create-operation="setCreateOperation"
          @update:edit-operation="setEditOperation"
          @update:open="setDrawerOpen"
        />

        <section v-if="createOperation" class="access-keys__operation" aria-live="polite">
          <InlineFeedback tone="warning">{{ t(operationNoticeKey) }}</InlineFeedback>
          <AppButton variant="secondary" @click="checkCreateOperation">
            {{ t('accessKeys.operation.checkResult') }}
          </AppButton>
        </section>

        <section v-if="editOperation" class="access-keys__operation" aria-live="polite">
          <InlineFeedback tone="warning">{{
            t(editOperationNoticeKey, { name: editOperationName })
          }}</InlineFeedback>
          <AppButton variant="secondary" @click="checkEditOperation">
            {{ t('accessKeys.operation.checkResult') }}
          </AppButton>
        </section>

        <p class="sr-only" aria-live="polite" aria-atomic="true">{{ deletionAnnouncement }}</p>

        <QueryFeedback
          v-if="accessKeysQuery.isPending.value"
          state="loading"
          :message="t('accessKeys.collection.loading')"
        />

        <div
          v-else-if="accessKeysQuery.isError.value && !data"
          class="collection-error"
          role="alert"
        >
          <EmptyState
            :title="t('accessKeys.collection.errorTitle')"
            :description="t('accessKeys.collection.errorDescription')"
            variant="ledger"
          >
            <template #icon><TriangleAlert :size="20" /></template>
            <template #actions>
              <AppButton variant="secondary" size="compact" @click="accessKeysQuery.refetch()">
                {{ t('common.retry') }}
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
            :label="t('accessKeys.collection.summary.region')"
            :total-label="t('accessKeys.collection.summary.current')"
            @update:model-value="setStatus"
          />

          <QueryFeedback
            v-if="accessKeysQuery.isError.value"
            class="stale-banner"
            state="stale"
            :message="t('accessKeys.stale')"
            :retry-label="t('common.retry')"
            @retry="accessKeysQuery.refetch()"
          />
          <QueryFeedback
            v-if="groupsQuery.isError.value"
            class="stale-banner"
            state="stale"
            :message="t('accessKeys.groupsStale')"
            :retry-label="t('common.retry')"
            @retry="groupsQuery.refetch()"
          />

          <template v-if="data.summary.total > 0">
            <CollectionFilterBar
              :label="t('accessKeys.collection.filters.region')"
              :show-result="hasFilterCriteria"
            >
              <label class="collection-filter-field collection-filter-field--search">
                <span class="collection-filter-label">
                  {{ t('accessKeys.collection.filters.searchLabel') }}
                </span>
                <span class="collection-filter-search-control">
                  <Search :size="15" aria-hidden="true" />
                  <input
                    v-model="searchDraft"
                    class="collection-filter-control"
                    type="search"
                    :aria-label="t('accessKeys.collection.filters.searchLabel')"
                    :placeholder="t('accessKeys.collection.filters.searchPlaceholder')"
                    @input="scheduleSearch"
                  />
                  <IconButton
                    v-if="searchDraft"
                    class="collection-filter-search-clear"
                    size="xs"
                    variant="ghost"
                    :label="t('accessKeys.collection.filters.clearSearch')"
                    @click="clearSearch"
                  >
                    <X :size="14" aria-hidden="true" />
                  </IconButton>
                </span>
              </label>

              <label class="collection-filter-field">
                <span class="collection-filter-label">
                  {{ t('accessKeys.collection.filters.scopeLabel') }}
                </span>
                <AppSelect
                  size="compact"
                  :label="t('accessKeys.collection.filters.scopeLabel')"
                  :model-value="filters.scope ?? 'all'"
                  :options="scopeSelectOptions"
                  @update:model-value="setScope"
                />
              </label>

              <template #result>
                <span aria-live="polite">
                  {{
                    t('accessKeys.collection.result', {
                      shown: n(data.items.length),
                      total: n(data.pagination.total_items),
                    })
                  }}
                </span>
                <AppButton variant="link" size="inline" @click="resetConditions">
                  {{ t('accessKeys.collection.filters.reset') }}
                </AppButton>
              </template>
            </CollectionFilterBar>
          </template>

          <EmptyState
            v-if="data.summary.total === 0"
            :title="t('accessKeys.emptyTitle')"
            :description="t('accessKeys.emptyDescription')"
            variant="ledger"
          >
            <template #icon><KeyRound :size="20" /></template>
            <template #actions>
              <AppButton class="access-key-create" @click="createKey">
                <Plus :size="15" aria-hidden="true" />{{ t('accessKeys.create') }}
              </AppButton>
            </template>
          </EmptyState>

          <EmptyState
            v-else-if="data.pagination.total_items === 0 && hasFilterCriteria"
            :title="t('accessKeys.collection.noResultsTitle')"
            :description="t('accessKeys.collection.noResultsDescription')"
            variant="ledger"
          >
            <template #icon><Search :size="20" /></template>
            <template #actions>
              <AppButton variant="secondary" size="compact" @click="resetConditions">
                {{ t('accessKeys.collection.filters.reset') }}
              </AppButton>
            </template>
          </EmptyState>

          <template v-else-if="data.items.length > 0">
            <AccessKeyCollection
              ref="collection"
              :access-keys="data.items"
              :groups="groupsQuery.data.value ?? []"
              :total="data.summary.total"
              :filtered-total="data.pagination.total_items"
              :page="data.pagination.page"
              :page-size="data.pagination.page_size"
              @edit="editKey"
              @deleted="focusCreateAfterDelete"
            />
            <PaginationBar
              :page="data.pagination.page"
              :page-size="data.pagination.page_size"
              :total-items="data.pagination.total_items"
              :total-pages="data.pagination.total_pages"
              @previous="setPage(data.pagination.page - 1)"
              @next="setPage(data.pagination.page + 1)"
            />
          </template>
        </template>
      </LedgerSheet>
    </PageFrame>
  </section>
</template>

<style scoped>
.access-keys-ledger :deep(.page-header) {
  border-bottom: 1px solid var(--color-border-control);
  padding-bottom: var(--space-5);
}

.access-keys__operation {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-card);
  background: var(--color-warning-bg);
  padding: var(--space-3) var(--space-4);
}

.stale-banner,
.collection-error {
  margin-top: var(--space-5);
}

@media (max-width: 640px) {
  .access-keys__operation {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
