<script setup lang="ts">
import { Boxes, RefreshCw, Search } from '@lucide/vue'
import { keepPreviousData, useIsFetching, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getModels,
  modelContextKey,
  modelsKey,
  syncModelPrices,
  type ModelFilters,
  type RequestModel,
} from '@modern/api/models'
import { useApiClient } from '@shared/http/client-context'
import { useAuthSession } from '@modern/features/auth/auth-session'
import { useMessages, useMessageSource } from '@modern/app/messages'
import { usePageRefresh } from '@modern/app/page-refresh'
import { useURLState } from '@modern/app/url-state'
import { useLoadingActivity } from '@modern/components/ui/loading'
import {
  AppButton,
  AppCollectionState,
  AppFilterSummary,
  AppListFrame,
  AppPagination,
  AppSegmentedControl,
  AppSelect,
  AppTextField,
} from '@modern/components/ui'
import { modelStateKeys, parseModelsState, serializeModelsState } from './models-state'
import ModelCard from './ModelCard.vue'
import ModelCatalogSettingsDialog from './ModelCatalogSettingsDialog.vue'
import ModelDetailPanel from './ModelDetailPanel.vue'

const { t } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const session = useAuthSession()
const admin = computed(() => session.state.principalType === 'admin')
const state = useURLState(modelStateKeys, parseModelsState, serializeModelsState)
const filters = computed<ModelFilters>(() => ({
  q: state.value.q,
  groups: admin.value ? state.value.groups : 'enabled',
  pricing: state.value.pricing,
  page: state.value.page,
  pageSize: state.value.pageSize,
}))
const query = useQuery(
  computed(() => ({
    queryKey: [...modelsKey, 'collection', filters.value],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getModels(client, { ...filters.value }, signal),
    placeholderData: keepPreviousData,
  })),
)
const data = computed(() => query.data.value)
const search = ref(state.value.q)
const profileModel = ref('')
const composing = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
const frame = ref<InstanceType<typeof AppListFrame>>()
const syncing = ref(false)
const messages = useMessages()
const controller = new AbortController()
const fetching = useIsFetching({ queryKey: modelsKey })
const pending = computed(() => Boolean(fetching.value) || syncing.value)
useLoadingActivity(() => query.isFetching.value || syncing.value)
const groupOptions = computed(() => [
  { value: 'enabled', label: t('modelManager.enabledGroups') },
  { value: 'all', label: t('modelManager.allGroups') },
])
const pricingOptions = computed(() => [
  { value: 'all', label: t('modelManager.allModels') },
  { value: 'pending', label: t('modelManager.pending') },
  { value: 'configured', label: t('modelManager.configured') },
])
const summary = computed(() => [
  ...(state.value.q ? [{ key: 'q', label: t('modelManager.search'), value: state.value.q }] : []),
  ...(admin.value && state.value.groups !== 'enabled'
    ? [{ key: 'groups', label: t('modelManager.groupStatus'), value: t('modelManager.allGroups') }]
    : []),
  ...(state.value.pricing !== 'all'
    ? [
        {
          key: 'pricing',
          label: t('modelManager.pricingStatus'),
          value: t('modelManager.' + state.value.pricing),
        },
      ]
    : []),
])
function change(patch: Partial<ModelFilters>): void {
  clearTimeout(timer)
  const q =
    patch.q ??
    (composing.value ? state.value.q : Array.from(search.value.trim()).slice(0, 200).join(''))
  state.value = {
    ...state.value,
    ...patch,
    q,
    page: q !== state.value.q ? 1 : (patch.page ?? 1),
  }
}
function scheduleSearch(): void {
  clearTimeout(timer)
  if (composing.value) return
  timer = setTimeout(
    () => change({ q: Array.from(search.value.trim()).slice(0, 200).join('') }),
    250,
  )
}
function finishComposition(): void {
  composing.value = false
  scheduleSearch()
}
function applySearch(): void {
  if (!composing.value) change({ q: Array.from(search.value.trim()).slice(0, 200).join('') })
}
function reset(key?: string): void {
  change({
    ...(key === undefined || key === 'q' ? { q: '' } : {}),
    ...(key === undefined || key === 'groups' ? { groups: 'enabled' } : {}),
    ...(key === undefined || key === 'pricing' ? { pricing: 'all' } : {}),
  })
}
function open(model: RequestModel, source = 0): void {
  if (query.isPlaceholderData.value) return
  if (filters.value.pricing === 'all')
    cache.setQueryData(modelContextKey(model.name, filters.value.groups), model)
  state.value = { ...state.value, model: model.name, source }
}
async function refresh(): Promise<void> {
  await cache.refetchQueries({ queryKey: modelsKey, type: 'active' })
}
async function changed(): Promise<void> {
  await Promise.all([
    cache.invalidateQueries({ queryKey: modelsKey }),
    cache.invalidateQueries({ queryKey: ['modern', 'groups'] }),
    cache.invalidateQueries({ queryKey: ['modern', 'group-models'] }),
  ])
}
async function sync(): Promise<void> {
  if (!admin.value || syncing.value) return
  syncing.value = true
  try {
    await syncModelPrices(client, controller.signal)
    if (controller.signal.aborted) return
    await changed()
    if (!controller.signal.aborted)
      messages.show({ tone: 'success', text: t('modelManager.syncSuccess') })
  } catch {
    if (!controller.signal.aborted)
      messages.show({ tone: 'danger', text: t('modelManager.syncFailed') })
  } finally {
    syncing.value = false
  }
}
watch(
  () => state.value.q,
  (value) => {
    clearTimeout(timer)
    search.value = value
  },
)
watch(
  () => JSON.stringify(filters.value),
  () => frame.value?.scrollToTop(),
)
watch([data, () => query.isPlaceholderData.value], ([result, placeholder]) => {
  if (!result || placeholder) return
  const page = Math.max(1, Math.min(state.value.page, result.pages))
  if (page !== state.value.page) state.value = { ...state.value, page }
})
watch(
  admin,
  (value) => {
    if (!value && state.value.groups !== 'enabled')
      state.value = { ...state.value, groups: 'enabled' }
  },
  { immediate: true },
)
onScopeDispose(() => {
  clearTimeout(timer)
  controller.abort()
})
usePageRefresh({
  refresh,
  pending,
  updatedAt: computed(() => query.dataUpdatedAt.value || undefined),
})
useMessageSource(() =>
  query.isError.value && data.value
    ? { tone: 'warning', text: t('modelManager.stale') }
    : undefined,
)
</script>

<template>
  <div class="modern-models-workspace">
    <form
      class="modern-models-toolbar"
      role="search"
      :aria-label="t('modelManager.search')"
      @submit.prevent="applySearch"
    >
      <AppTextField
        v-model="search"
        :label="t('modelManager.search')"
        :placeholder="t('modelManager.searchPlaceholder')"
        :icon="Search"
        type="search"
        label-hidden
        class="modern-models-search"
        @input="scheduleSearch"
        @compositionstart="composing = true"
        @compositionend="finishComposition"
      />
      <AppSelect
        v-if="admin"
        :model-value="state.groups"
        :options="groupOptions"
        :label="t('modelManager.groupStatus')"
        label-hidden
        class="modern-models-filter"
        @update:model-value="change({ groups: $event === 'all' ? 'all' : 'enabled' })"
      />
      <AppButton v-if="admin" :icon="RefreshCw" :loading="syncing" @click="sync">{{
        t('modelManager.sync')
      }}</AppButton>
    </form>
    <div class="modern-models-statusbar">
      <AppSegmentedControl
        :model-value="state.pricing"
        :options="pricingOptions"
        :label="t('modelManager.pricingStatus')"
        @update:model-value="
          change({ pricing: $event === 'pending' || $event === 'configured' ? $event : 'all' })
        "
      />
      <AppFilterSummary :items="summary" @remove="reset" @reset="reset()" />
    </div>
    <AppListFrame
      ref="frame"
      :label="t('pages.models.title')"
      :loading="Boolean(data) && query.isFetching.value"
      :scroll-key="JSON.stringify(filters)"
    >
      <AppCollectionState
        v-if="!data && query.isPending.value"
        :title="t('modelManager.loading')"
        loading
      />
      <AppCollectionState v-else-if="!data" :title="t('modelManager.failed')" error>
        <AppButton @click="refresh">{{ t('ui.retry') }}</AppButton>
      </AppCollectionState>
      <AppCollectionState
        v-else-if="!data.items.length"
        :icon="summary.length ? Search : Boxes"
        :title="t(summary.length ? 'modelManager.noMatches' : 'modelManager.empty')"
        :description="t(summary.length ? 'modelManager.noMatchesHelp' : 'modelManager.emptyHelp')"
      >
        <AppButton v-if="summary.length" @click="reset()">{{ t('collection.reset') }}</AppButton>
      </AppCollectionState>
      <div v-else class="modern-models-grid">
        <ModelCard
          v-for="model in data.items"
          :key="model.name"
          :model="model"
          :admin="admin"
          :selected="model.name === state.model"
          :disabled="query.isPlaceholderData.value"
          @open="open(model, $event)"
          @settings="profileModel = model.name"
        />
      </div>
      <template #footer>
        <AppPagination
          mode="total"
          :page="state.page"
          :page-size="state.pageSize"
          :total="data?.total"
          :pending="query.isFetching.value || syncing"
          @update:page="change({ page: $event })"
          @update:page-size="change({ pageSize: $event })"
        />
      </template>
    </AppListFrame>
    <ModelDetailPanel
      v-if="state.model"
      :key="state.model"
      :model="state.model"
      :source="state.source"
      :groups="filters.groups"
      :admin="admin"
      @close="state = { ...state, model: '', source: 0 }"
      @select="state = { ...state, source: $event }"
      @changed="changed"
    />
    <ModelCatalogSettingsDialog
      v-if="admin && profileModel"
      :key="profileModel"
      :model="profileModel"
      :editable="admin"
      @close="profileModel = ''"
    />
  </div>
</template>

<style scoped>
.modern-models-workspace {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
}
.modern-models-toolbar {
  display: flex;
  flex: none;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  padding: var(--modern-space-5) 0 var(--modern-space-3);
}
.modern-models-search {
  flex: 2 1 280px;
  min-width: 0;
}
.modern-models-filter {
  flex: 1 1 180px;
  min-width: 0;
}
.modern-models-statusbar {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  padding-bottom: var(--modern-space-4);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
/* 卡片改为价格表头 + 来源行的宽卡布局，需要足够宽度让 4 个价格列不挤压来源名。
   用多列而不是网格：来源数不同导致卡高不一，网格会按行内最高的卡留出空洞。 */
.modern-models-grid {
  column-width: 620px;
  column-gap: var(--modern-space-4);
  padding-block: var(--modern-space-1) var(--modern-space-4);
}
.modern-models-grid > * {
  margin-bottom: var(--modern-space-4);
  break-inside: avoid;
}
@media (max-width: 760px) {
  .modern-models-toolbar {
    gap: var(--modern-space-2);
  }
  .modern-models-search {
    flex-basis: 100%;
  }
  .modern-models-filter {
    flex-basis: 120px;
  }
}
</style>
