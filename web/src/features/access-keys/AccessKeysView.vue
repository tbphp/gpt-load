<script setup lang="ts">
import { KeyRound, Plus, Search, TriangleAlert } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import type {
  AccessKeyCollectionFilters,
  AccessKeyCollectionStatus,
  AccessKeyDto,
} from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import { useCollectionLoading } from '@/app/loading-state'
import {
  accessKeyCollectionQueryOptions,
  accessKeyResources,
  updateAccessKey,
} from '@/app/resources/access-keys'
import { groupOptionsQueryOptions } from '@/app/resources/groups'
import { channelsQueryOptions } from '@/app/resources/channels'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { accessKeysLocation } from '@/app/route-locations'
import { useToast } from '@/app/toast'
import { useDebouncedAction } from '@/app/use-debounced-action'
import CollectionFilterBar from '@/components/collection/CollectionFilterBar.vue'
import CollectionStatusSummary from '@/components/collection/CollectionStatusSummary.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'

import AccessKeyCollection from './AccessKeyCollection.vue'
import AccessKeyDrawer from './AccessKeyDrawer.vue'
import type { PendingAccessKeyCreateOperation } from './access-key-create-operation'
import {
  constrainAccessKeyCollectionSearchQuery,
  isCanonicalAccessKeyCollectionRouteQuery,
  parseAccessKeyDrawerRoute,
  parseAccessKeyCollectionRouteQuery,
  serializeAccessKeyCollectionRouteQuery,
  type AccessKeyDrawerRoute,
} from './access-key-collection-route'
import type { PendingAccessKeyEditOperation } from './access-key-edit-operation'
import type { PendingAccessKeyRotateOperation } from './access-key-rotate-operation'

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const queryClient = useQueryClient()
const toast = useToast()
const { n, t } = useI18n()
const filters = computed(() => parseAccessKeyCollectionRouteQuery(route.query))
const drawerRoute = computed(() => parseAccessKeyDrawerRoute(route.query))
const searchDraft = ref(filters.value.q ?? '')
const drawerOpen = computed(() => drawerRoute.value !== undefined)
const selected = ref<AccessKeyDto | null>(null)
const createOperation = ref<PendingAccessKeyCreateOperation | null>(null)
const editOperation = ref<PendingAccessKeyEditOperation | null>(null)
const rotateOperation = ref<PendingAccessKeyRotateOperation | null>(null)
const viewRoot = ref<HTMLElement | null>(null)
const collection = ref<InstanceType<typeof AccessKeyCollection> | null>(null)
const deletionAnnouncement = ref('')
const pendingStatusIDs = ref(new Set<number>())
const lockedAccessKeyIDs = computed<ReadonlySet<number>>(() =>
  rotateOperation.value ? new Set([rotateOperation.value.base.id]) : new Set<number>(),
)
const statusControllers = new Map<number, AbortController>()
const accessKeysQuery = useQuery(accessKeyCollectionQueryOptions(client, filters))
const groupsQuery = useQuery(groupOptionsQueryOptions(client))
const channelsQuery = useQuery(channelsQueryOptions(client, ''))
const data = computed(() => accessKeysQuery.data.value)
const collectionBusy = computed(() => data.value !== undefined && accessKeysQuery.isFetching.value)
const {
  initial: initialLoading,
  transition: collectionTransition,
  refreshing: collectionRefreshing,
  rows: skeletonRows,
} = useCollectionLoading(
  {
    pending: () => accessKeysQuery.isPending.value,
    placeholder: () => accessKeysQuery.isPlaceholderData.value,
    fetching: () => accessKeysQuery.isFetching.value,
    hasData: () => data.value !== undefined,
    itemCount: () => data.value?.items.length ?? 0,
  },
  { fallbackRows: 20 },
)
const pageRefreshing = computed(
  () =>
    collectionRefreshing.value ||
    (groupsQuery.data.value !== undefined && groupsQuery.isFetching.value) ||
    (channelsQuery.data.value !== undefined && channelsQuery.isFetching.value),
)
const hasFilterCriteria = computed(
  () => filters.value.q !== undefined || filters.value.status !== undefined,
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
const groupCatalogState = computed(() => {
  if (groupsQuery.isError.value || channelsQuery.isError.value) {
    return groupsQuery.data.value && channelsQuery.data.value ? 'stale' : 'error'
  }
  if (groupsQuery.isPending.value || channelsQuery.isPending.value) return 'loading'
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
const rotateOperationNoticeKey = computed(() =>
  rotateOperation.value?.state === 'reconciling'
    ? 'accessKeys.operation.rotateReconciling'
    : 'accessKeys.operation.rotateIndeterminate',
)
let restoreFocus: HTMLElement | null = null
const searchDebounce = useDebouncedAction(300)
let mounted = true

watch(
  () => route.query,
  (query) => {
    searchDebounce.cancel()
    const parsed = parseAccessKeyCollectionRouteQuery(query)
    const drawer = parseAccessKeyDrawerRoute(query)
    searchDraft.value = parsed.q ?? ''
    if (!isCanonicalAccessKeyCollectionRouteQuery(query, parsed, drawer)) {
      void router.replace(
        accessKeysLocation(serializeAccessKeyCollectionRouteQuery(parsed, drawer)),
      )
    }
  },
  { deep: true, immediate: true },
)

watch(filters, () => collection.value?.conceal())

watch(
  [
    drawerRoute,
    data,
    editOperation,
    rotateOperation,
    () => accessKeysQuery.isPlaceholderData.value,
  ],
  ([drawer, pageData, pendingEdit, pendingRotate, placeholder]) => {
    if (drawer === undefined || drawer.mode === 'create') {
      selected.value = null
      return
    }
    const item = pageData?.items.find((accessKey) => accessKey.id === drawer.accessKeyID)
    if (item) {
      selected.value = item
      return
    }
    if (pendingEdit?.base.id === drawer.accessKeyID) {
      selected.value = pendingEdit.base
      return
    }
    if (pendingRotate?.base.id === drawer.accessKeyID) {
      selected.value = pendingRotate.base
      return
    }
    if (pageData && !placeholder) void setDrawerRoute(undefined, true)
  },
  { immediate: true },
)

watch(
  [
    () => data.value?.pagination.total_pages,
    () => filters.value.page,
    () => accessKeysQuery.isPlaceholderData.value,
  ],
  ([totalPages, page, isPlaceholderData]) => {
    if (isPlaceholderData || totalPages === undefined) return
    const lastValidPage = Math.max(1, totalPages)
    if (page > lastValidPage) {
      void router.replace(
        accessKeysLocation(
          serializeAccessKeyCollectionRouteQuery(
            { ...filters.value, page: lastValidPage },
            drawerRoute.value,
          ),
        ),
      )
    }
  },
)

onBeforeUnmount(() => {
  mounted = false
  for (const controller of statusControllers.values()) controller.abort()
  statusControllers.clear()
  queryClient.removeQueries({ queryKey: accessKeyResources.collection.queryKey })
})

function routeWithFilters(next: AccessKeyCollectionFilters, replace = false): void {
  collection.value?.conceal()
  const location = accessKeysLocation(
    serializeAccessKeyCollectionRouteQuery(next, drawerRoute.value),
  )
  void (replace ? router.replace(location) : router.push(location))
}

async function setDrawerRoute(
  drawer: AccessKeyDrawerRoute | undefined,
  replace = false,
): Promise<void> {
  const location = accessKeysLocation(serializeAccessKeyCollectionRouteQuery(filters.value, drawer))
  await (replace ? router.replace(location) : router.push(location))
}

function updateConditions(patch: Partial<Pick<AccessKeyCollectionFilters, 'q' | 'status'>>): void {
  routeWithFilters({
    ...filters.value,
    q: constrainAccessKeyCollectionSearchQuery(searchDraft.value),
    ...patch,
    page: 1,
  })
}

function scheduleSearch(): void {
  searchDebounce.schedule(() => {
    updateConditions({ q: constrainAccessKeyCollectionSearchQuery(searchDraft.value) })
  })
}

function clearSearch(): void {
  searchDebounce.cancel()
  searchDraft.value = ''
  updateConditions({ q: undefined })
}

function setStatus(status: string | undefined): void {
  updateConditions({ status: status as AccessKeyCollectionStatus | undefined })
}

function resetConditions(): void {
  searchDebounce.cancel()
  searchDraft.value = ''
  routeWithFilters({ page: 1, page_size: 20 })
}

function retryGroupCatalog(): void {
  void Promise.allSettled([groupsQuery.refetch(), channelsQuery.refetch()])
}

function setPage(page: number): void {
  routeWithFilters({ ...filters.value, page })
}

function createKey(): void {
  selected.value = null
  restoreFocus = null
  void setDrawerRoute({ mode: 'create' })
}

function openKey(accessKey: AccessKeyDto, trigger: HTMLElement): void {
  if (editOperation.value && editOperation.value.base.id !== accessKey.id) {
    checkEditOperation()
    return
  }
  if (rotateOperation.value && rotateOperation.value.base.id !== accessKey.id) {
    checkRotateOperation()
    return
  }
  selected.value = accessKey
  restoreFocus = trigger
  void setDrawerRoute({ mode: 'edit', accessKeyID: accessKey.id })
}

function setCreateOperation(operation: PendingAccessKeyCreateOperation | null): void {
  createOperation.value = operation
}

function setEditOperation(operation: PendingAccessKeyEditOperation | null): void {
  editOperation.value = operation
}

function setRotateOperation(operation: PendingAccessKeyRotateOperation | null): void {
  rotateOperation.value = operation
}

function checkCreateOperation(): void {
  if (!createOperation.value) return
  selected.value = null
  restoreFocus = null
  void setDrawerRoute({ mode: 'create' })
}

function checkEditOperation(): void {
  const operation = editOperation.value
  if (!operation) return
  selected.value =
    data.value?.items.find((accessKey) => accessKey.id === operation.base.id) ?? operation.base
  restoreFocus = null
  void setDrawerRoute({ mode: 'edit', accessKeyID: operation.base.id })
}

function checkRotateOperation(): void {
  const operation = rotateOperation.value
  if (!operation) return
  selected.value =
    data.value?.items.find((accessKey) => accessKey.id === operation.base.id) ?? operation.base
  restoreFocus = null
  void setDrawerRoute({ mode: 'edit', accessKeyID: operation.base.id })
}

async function setDrawerOpen(open: boolean): Promise<void> {
  if (open) return
  collection.value?.conceal()
  await setDrawerRoute(undefined)
  selected.value = null
  const target = restoreFocus
  restoreFocus = null
  await nextTick()
  target?.focus()
}

async function handleSaved(kind: 'created' | 'updated', name: string): Promise<void> {
  await setDrawerOpen(false)
  toast.show({ message: t(`accessKeys.toast.${kind}`, { name }) })
}

async function handleDeleted(name: string): Promise<void> {
  deletionAnnouncement.value = ''
  restoreFocus = null
  await setDrawerOpen(false)
  await nextTick()
  await applyInvalidationPlan(queryClient, mutationInvalidationPlans.accessKey.delete)
  await nextTick()
  if (!mounted) return
  deletionAnnouncement.value = t('accessKeys.delete.deletedAnnouncement', { name })
  toast.show({ message: t('accessKeys.toast.deleted', { name }) })
  const target = viewRoot.value?.querySelector('button.access-key-create')
  if (target instanceof HTMLButtonElement && target.isConnected) target.focus()
}

async function handleCostLimitsReset(name: string): Promise<void> {
  try {
    await applyInvalidationPlan(queryClient, mutationInvalidationPlans.accessKey.reset)
  } catch {
    void queryClient.invalidateQueries({ queryKey: accessKeyResources.collection.queryKey })
  }
  if (mounted) toast.show({ message: t('accessKeys.toast.reset', { name }) })
}

function handleRotated(name: string): void {
  toast.show({ message: t('accessKeys.toast.rotated', { name }) })
}

function setStatusPending(id: number, pending: boolean): void {
  const next = new Set(pendingStatusIDs.value)
  if (pending) next.add(id)
  else next.delete(id)
  pendingStatusIDs.value = next
}

async function toggleStatus(accessKey: AccessKeyDto): Promise<void> {
  if (statusControllers.has(accessKey.id)) return
  const status = accessKey.status === 'active' ? 'disabled' : 'active'
  const controller = new AbortController()
  statusControllers.set(accessKey.id, controller)
  setStatusPending(accessKey.id, true)
  try {
    try {
      await updateAccessKey(client, accessKey.id, { status }, controller.signal)
    } catch (error: unknown) {
      if (!(error instanceof RequestCancelledError) && mounted) {
        toast.show({ message: t('accessKeys.actions.updateFailed'), tone: 'danger' })
      }
      return
    }
    if (!mounted) return
    try {
      await applyInvalidationPlan(queryClient, mutationInvalidationPlans.accessKey.update)
    } catch {
      void queryClient.invalidateQueries({ queryKey: accessKeyResources.collection.queryKey })
    }
    if (!mounted) return
    toast.show({
      message: t(status === 'active' ? 'accessKeys.toast.enabled' : 'accessKeys.toast.disabled', {
        name: accessKey.name,
      }),
    })
  } finally {
    if (statusControllers.get(accessKey.id) === controller) {
      statusControllers.delete(accessKey.id)
      setStatusPending(accessKey.id, false)
    }
  }
}
</script>

<template>
  <section ref="viewRoot" aria-labelledby="access-keys-title">
    <PageFrame>
      <LedgerSheet class="access-keys-ledger" :aria-busy="collectionBusy ? 'true' : undefined">
        <PageHeader id="access-keys-title" :title="t('accessKeys.title')">
          <template #actions>
            <AppButton class="access-key-create" @click="createKey">
              <Plus :size="16" aria-hidden="true" />{{ t('accessKeys.create') }}
            </AppButton>
          </template>
        </PageHeader>

        <AsyncRefreshIndicator
          :active="pageRefreshing"
          :label="t('accessKeys.collection.loading')"
        />

        <InlineFeedback class="access-keys__login-notice" appearance="ledger" tone="info">
          <template #glyph><KeyRound :size="12" aria-hidden="true" /></template>
          <span class="access-keys__login-message">
            <strong>{{ t('accessKeys.loginNotice.title') }}</strong>
            <span>{{ t('accessKeys.loginNotice.description') }}</span>
          </span>
        </InlineFeedback>

        <AccessKeyDrawer
          v-if="drawerOpen && (drawerRoute?.mode === 'create' || selected)"
          :open="drawerOpen"
          :access-key="selected"
          :groups="groupsQuery.data.value ?? []"
          :channels="channelsQuery.data.value?.items ?? []"
          :total="data?.summary.total ?? 0"
          :group-catalog-state="groupCatalogState"
          :create-operation="createOperation"
          :edit-operation="selected?.id === editOperation?.base.id ? editOperation : null"
          :rotate-operation="selected?.id === rotateOperation?.base.id ? rotateOperation : null"
          @update:create-operation="setCreateOperation"
          @update:edit-operation="setEditOperation"
          @update:rotate-operation="setRotateOperation"
          @update:open="setDrawerOpen"
          @saved="handleSaved"
          @rotated="handleRotated"
          @deleted="handleDeleted"
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

        <section v-if="rotateOperation" class="access-keys__operation" aria-live="polite">
          <InlineFeedback tone="warning">{{
            t(rotateOperationNoticeKey, { name: rotateOperation.base.name })
          }}</InlineFeedback>
          <AppButton variant="secondary" @click="checkRotateOperation">
            {{ t('accessKeys.operation.checkResult') }}
          </AppButton>
        </section>

        <p class="sr-only" aria-live="polite" aria-atomic="true">{{ deletionAnnouncement }}</p>

        <SkeletonSurface
          v-if="accessKeysQuery.isPending.value || initialLoading"
          variant="collection"
          :rows="filters.page_size"
          :columns="7"
          row-height="96px"
          mobile-row-height="236px"
          show-controls
          :concealed="!initialLoading"
          :label="t('accessKeys.collection.loading')"
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
            v-if="groupsQuery.isError.value || channelsQuery.isError.value"
            class="stale-banner"
            state="stale"
            :message="t('accessKeys.groupsStale')"
            :retry-label="t('common.retry')"
            @retry="retryGroupCatalog"
          />

          <template v-if="data.summary.total > 0">
            <CollectionFilterBar
              :label="t('accessKeys.collection.filters.region')"
              :show-result="hasFilterCriteria"
              single-column
            >
              <label class="collection-filter-field collection-filter-field--search">
                <span class="collection-filter-label">
                  {{ t('accessKeys.collection.filters.searchLabel') }}
                </span>
                <AppSearchInput
                  v-model="searchDraft"
                  :label="t('accessKeys.collection.filters.searchLabel')"
                  :placeholder="t('accessKeys.collection.filters.searchPlaceholder')"
                  :clear-label="t('accessKeys.collection.filters.clearSearch')"
                  @update:model-value="scheduleSearch"
                  @clear="clearSearch"
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

          <SkeletonSurface
            v-if="collectionTransition"
            variant="collection"
            :rows="skeletonRows"
            :columns="7"
            row-height="96px"
            mobile-row-height="236px"
            :label="t('accessKeys.collection.loading')"
          />

          <EmptyState
            v-else-if="data.summary.total === 0"
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
              :busy-ids="pendingStatusIDs"
              :locked-ids="lockedAccessKeyIDs"
              @open="openKey"
              @toggle="toggleStatus"
              @deleted="handleDeleted"
              @reset="handleCostLimitsReset"
            />
            <PaginationBar
              :page="data.pagination.page"
              :page-size="data.pagination.page_size"
              :total-items="data.pagination.total_items"
              :total-pages="data.pagination.total_pages"
              :pending="collectionBusy"
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
.access-keys__login-notice {
  margin: var(--space-4) 0 var(--space-2);
}

.access-keys__login-message {
  display: grid;
  gap: 2px;
}

.access-keys__login-message strong {
  color: var(--color-text);
  font-weight: 650;
}

.access-keys__login-message > span {
  color: var(--color-text-muted);
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
