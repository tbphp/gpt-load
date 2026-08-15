<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { Plus, RefreshCw } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { ApiError, RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import { channelsQueryOptions } from '@/app/resources/channels'
import {
  cacheGroupModels,
  discoverGroupModels,
  groupModelsQueryOptions,
  replaceGroupModelsResource,
} from '@/app/resources/groups'
import { invalidateGroupSummary } from '@/app/resources/groups'
import type { ModelCandidate } from '@/app/resources/providers'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import { useTransientFlag } from '@/app/use-transient-flag'
import { constrainCollectionSearch } from '@/app/route-query'
import { groupDetailLocation } from '@/app/route-locations'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import StickySaveBar from '@/components/ui/StickySaveBar.vue'
import ModelAliasEditor from '@/features/models/ModelAliasEditor.vue'
import ModelDiscoveryDrawer from '@/features/models/ModelDiscoveryDrawer.vue'
import {
  appendSelectedCandidates,
  mergeCandidateMetadata,
  readModelNameConflicts,
  type ModelAliasEditorLabels,
  type ModelDiscoveryDrawerLabels,
} from '@/features/models/model-draft'
import ModelPricingStatus from '@/features/models/ModelPricingStatus.vue'

import {
  createModelDraft,
  findModelNameConflicts,
  normalizedModels,
  sameModels,
  type ModelDraftItem,
  type ModelNameConflict,
} from './model-diff'
import {
  parseGroupModelsRouteQuery,
  serializeGroupModelsRouteQuery,
  type GroupModelsRouteState,
  normalizeGroupTab,
} from '../group-route'

const props = defineProps<{ groupId: number; channelId: string }>()
const client = useApiClient()
const queryClient = useQueryClient()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const routeState = computed(() => parseGroupModelsRouteQuery(route.query))
const query = useQuery(groupModelsQueryOptions(client, () => props.groupId))
const channelsQuery = useQuery(channelsQueryOptions(client, ''))
const supportsModelDiscovery = computed(
  () =>
    channelsQuery.data.value?.items.find(({ channel_id }) => channel_id === props.channelId)
      ?.capabilities.model_discovery === true,
)
const initialLoading = useStableLoading(
  () => query.isPending.value && query.data.value === undefined,
)
const queryRefreshing = computed(() => query.data.value !== undefined && query.isFetching.value)
const saved = ref<ModelDraftItem[]>([])
const draft = ref<ModelDraftItem[]>([])
const pending = ref<'discover' | 'save' | null>(null)
const discoveryError = ref('')
const saveError = ref('')
const serverConflicts = ref<ModelNameConflict[]>([])
const drawerOpen = computed(() => routeState.value.discoveryOpen)
const modelEditor = ref<{
  addManual: () => Promise<void>
  focusFirstInvalid: () => Promise<void>
}>()
const emptyConfirmOpen = ref(false)
const candidates = ref<ModelCandidate[]>([])
const {
  value: savedFeedback,
  clear: clearSavedFeedback,
  show: showSavedFeedback,
} = useTransientFlag(1_600)
let nextKey = 1
let controller: AbortController | undefined

const conflicts = computed(() =>
  serverConflicts.value.length
    ? serverConflicts.value
    : findModelNameConflicts(normalizedModels(draft.value)),
)
const emptyAliasIndexes = computed(
  () =>
    new Set(
      draft.value.flatMap((item, index) =>
        item.alias_enabled && !item.alias.trim() ? [index] : [],
      ),
    ),
)
const emptyIDIndexes = computed(
  () => new Set(draft.value.flatMap((item, index) => (!item.id.trim() ? [index] : []))),
)
const invalidRowCount = computed(
  () =>
    new Set([
      ...conflicts.value.flatMap((item) => item.indexes),
      ...emptyAliasIndexes.value,
      ...emptyIDIndexes.value,
    ]).size,
)
const validationSummary = computed(() =>
  [
    conflicts.value.length ? t('group.modelEditor.conflictSummary') : '',
    emptyIDIndexes.value.size ? t('group.modelEditor.manualIdRequired') : '',
    emptyAliasIndexes.value.size ? t('group.modelEditor.emptyAliasSummary') : '',
  ]
    .filter(Boolean)
    .join(' · '),
)
const saveBarError = computed(() => validationSummary.value || saveError.value)
const dirty = computed(
  () =>
    !sameModels(saved.value, draft.value) ||
    draft.value.some((item) => item.editable_id && !item.id.trim()),
)
const empty = computed(() => normalizedModels(draft.value).length === 0)
const canSave = computed(
  () =>
    dirty.value &&
    pending.value === null &&
    conflicts.value.length === 0 &&
    emptyIDIndexes.value.size === 0 &&
    emptyAliasIndexes.value.size === 0,
)
const pendingPricingCount = computed(
  () => draft.value.filter((item) => item.pricing_status === 'pending').length,
)
const currentModelIDs = computed(() => draft.value.map((item) => item.id.trim()).filter(Boolean))
const knownPricingStatusByID = computed(
  () => new Map(saved.value.map((item) => [item.id, item.pricing_status] as const)),
)
const aliasEditorLabels = computed<ModelAliasEditorLabels>(() => ({
  tableLabel: t('group.modelEditor.tableLabel'),
  id: t('group.modelEditor.id'),
  alias: t('group.modelEditor.alias'),
  thirdColumn: t('group.modelEditor.pricing'),
  actions: t('group.modelEditor.actions'),
  search: t('group.modelEditor.searchPlaceholder'),
  searchLabel: t('group.modelEditor.searchLabel'),
  clearSearch: t('group.modelEditor.searchClear'),
  aliasEnabledFor: (id) => t('group.modelEditor.aliasEnabledFor', { id }),
  aliasFor: (id) => t('group.modelEditor.aliasFor', { id }),
  aliasPlaceholder: t('group.modelEditor.aliasPlaceholder'),
  aliasRequired: t('group.modelEditor.aliasRequired'),
  removeFor: (id) => t('group.modelEditor.removeFor', { id }),
  manualId: t('group.modelEditor.manualId'),
  manualIdRequired: t('group.modelEditor.manualIdRequired'),
  add: t('group.modelEditor.add'),
  addInline: t('group.modelEditor.addInline'),
  count: (count) =>
    pendingPricingCount.value
      ? t('group.modelEditor.summary', { count, pending: pendingPricingCount.value })
      : t('group.modelEditor.total', { count }),
  empty: t('group.modelEditor.empty'),
  noMatches: t('group.modelEditor.noMatches'),
  nameConflict: (name) => t('group.modelEditor.nameConflict', { name }),
}))
const discoveryDrawerLabels = computed<ModelDiscoveryDrawerLabels>(() => ({
  title: t('group.modelEditor.drawer.title'),
  description: t('group.modelEditor.drawer.description'),
  close: t('group.modelEditor.drawer.close'),
  loading: t('group.modelEditor.drawer.loading'),
  search: t('group.modelEditor.drawer.search'),
  clearSearch: t('group.modelEditor.clearSearch'),
  filterLabel: t('group.modelEditor.drawer.filterLabel'),
  filterUnadded: t('group.modelEditor.drawer.filterAvailable'),
  filterAll: t('group.modelEditor.drawer.filterAll'),
  alreadyAdded: t('group.modelEditor.drawer.alreadyAdded'),
  unadded: t('group.modelEditor.drawer.filterAvailable'),
  noMatches: t('group.modelEditor.drawer.noMatches'),
  empty: t('group.modelEditor.drawer.empty'),
  selected: (count) => t('group.modelEditor.drawer.selected', { count }),
  selectAll: t('group.modelEditor.drawer.selectAll'),
  deselectAll: t('group.modelEditor.drawer.deselectAll'),
  retry: t('common.retry'),
  cancel: t('common.cancel'),
  confirm: t('group.modelEditor.drawer.confirm'),
  pricingStatus: {
    pending: t('group.modelEditor.pricingStatus.pending'),
    configured: t('group.modelEditor.pricingStatus.configured'),
  },
  pricingDiscovered: (source) => t('group.modelEditor.pricingStatus.discovered', { source }),
  sources: {
    catalog: t('group.modelEditor.sources.catalog'),
    live: t('group.modelEditor.sources.live'),
  },
}))
useUnsavedChanges(dirty, {
  blocked: computed(() => pending.value !== null),
  allowRouteUpdate: (to, from) =>
    to.name === from.name &&
    String(to.params.id) === String(from.params.id) &&
    normalizeGroupTab(to.query.tab) === 'models' &&
    normalizeGroupTab(from.query.tab) === 'models',
})

watch(dirty, (isDirty) => {
  if (isDirty) clearSavedFeedback()
})

watch(
  () => query.data.value,
  (models) => {
    if (!models || dirty.value || pending.value) return
    const next = createModelDraft(models.items).map((item) => ({ ...item, key: nextKey++ }))
    saved.value = next
    draft.value = next.map((item) => ({ ...item, sources: [...item.sources] }))
    serverConflicts.value = []
    saveError.value = ''
  },
  { immediate: true },
)

watch(
  [() => routeState.value.discoveryOpen, () => query.data.value, supportsModelDiscovery],
  ([open, models, supported]) => {
    if (!open) {
      if (pending.value === 'discover') controller?.abort()
      return
    }
    if (supported && models && pending.value === null && candidates.value.length === 0)
      void runDiscovery()
  },
  { immediate: true },
)

function navigateRoute(state: GroupModelsRouteState, replace = false): void {
  const location = groupDetailLocation(props.groupId, serializeGroupModelsRouteQuery(state))
  void (replace ? router.replace(location) : router.push(location))
}

function updateRoute(patch: Partial<GroupModelsRouteState>, replace = false): void {
  navigateRoute({ ...routeState.value, ...patch }, replace)
}

function setModelSearch(value: string): void {
  updateRoute({ search: constrainCollectionSearch(value) }, true)
}

function setDiscoverySearch(value: string): void {
  updateRoute({ discoverySearch: constrainCollectionSearch(value) }, true)
}

function setDiscoveryFilter(value: 'unadded' | 'all'): void {
  updateRoute({ discoveryFilter: value })
}

function setDiscoveryOpen(open: boolean): void {
  updateRoute(
    open
      ? { discoveryOpen: true }
      : {
          discoveryOpen: false,
          discoverySearch: undefined,
          discoveryFilter: 'unadded',
        },
  )
}

function updateModels(models: ModelDraftItem[]): void {
  serverConflicts.value = []
  saveError.value = ''
  const previousByKey = new Map(draft.value.map((item) => [item.key, item] as const))
  draft.value = models.map((item) => {
    const previous = previousByKey.get(item.key)
    return {
      ...item,
      pricing_status:
        previous && previous.id === item.id ? item.pricing_status : pricingStatusForID(item.id),
      name: previous && previous.id === item.id ? item.name : item.id,
      sources: previous && previous.id === item.id ? [...item.sources] : [],
    }
  })
}

function pricingStatusForID(id: string): ModelDraftItem['pricing_status'] {
  return knownPricingStatusByID.value.get(id.trim()) ?? 'pending'
}

function createManualRow(): ModelDraftItem {
  return {
    id: '',
    name: '',
    sources: [],
    alias: '',
    alias_enabled: false,
    pricing_status: 'pending',
    editable_id: true,
    key: nextKey++,
  }
}

function addManual(): void {
  void modelEditor.value?.addManual()
}

function requestDiscovery(): void {
  if (!supportsModelDiscovery.value || pending.value) return
  candidates.value = []
  discoveryError.value = ''
  if (drawerOpen.value) void runDiscovery()
  else setDiscoveryOpen(true)
}

async function runDiscovery(): Promise<void> {
  if (!supportsModelDiscovery.value || pending.value !== null) return
  controller?.abort()
  const active = new AbortController()
  controller = active
  pending.value = 'discover'
  try {
    const result = await discoverGroupModels(client, props.groupId, active.signal)
    if (controller !== active) return
    candidates.value = result.models
    draft.value = mergeCandidateMetadata(draft.value, result.models)
  } catch (cause: unknown) {
    if (cause instanceof RequestCancelledError || controller !== active) return
    discoveryError.value =
      cause instanceof ApiError && cause.code === 'NO_ACTIVE_CREDENTIAL'
        ? t('group.modelEditor.noActiveCredential.title')
        : t('common.modelDiscoveryFailed')
  } finally {
    if (controller === active) {
      controller = undefined
      pending.value = null
    }
  }
}

function confirmCandidates(selectedCandidates: ModelCandidate[]): void {
  draft.value = appendSelectedCandidates(draft.value, selectedCandidates, (candidate) => ({
    id: candidate.id,
    name: candidate.name,
    sources: [...candidate.sources],
    alias: '',
    alias_enabled: false,
    pricing_status: candidate.pricing_status,
    key: nextKey++,
  }))
  serverConflicts.value = []
  saveError.value = ''
  setDiscoveryOpen(false)
}

function requestSave(): void {
  if (!canSave.value) return
  if (empty.value) {
    emptyConfirmOpen.value = true
    return
  }
  void save()
}

async function save(): Promise<void> {
  if (!canSave.value) return
  const active = new AbortController()
  controller = active
  pending.value = 'save'
  clearSavedFeedback()
  saveError.value = ''
  let shouldFocusInvalid = false
  try {
    const result = await replaceGroupModelsResource(
      client,
      props.groupId,
      {
        models: normalizedModels(draft.value),
      },
      active.signal,
    )
    if (controller !== active) return
    const next = createModelDraft(result.items).map((item) => ({ ...item, key: nextKey++ }))
    saved.value = next
    draft.value = next.map((item) => ({ ...item, sources: [...item.sources] }))
    serverConflicts.value = []
    emptyConfirmOpen.value = false
    cacheGroupModels(queryClient, props.groupId, result)
    await invalidateGroupSummary(queryClient, props.groupId)
    showSavedFeedback()
  } catch (cause: unknown) {
    if (cause instanceof RequestCancelledError || controller !== active) return
    const nextConflicts =
      cause instanceof ApiError && cause.code === 'MODEL_NAME_CONFLICT'
        ? readModelNameConflicts(cause.data)
        : []
    if (nextConflicts.length) {
      serverConflicts.value = nextConflicts
      shouldFocusInvalid = true
    } else {
      saveError.value = t('group.modelEditor.saveFailed')
    }
  } finally {
    if (controller === active) {
      controller = undefined
      pending.value = null
    }
  }
  if (shouldFocusInvalid) {
    await nextTick()
    await modelEditor.value?.focusFirstInvalid()
  }
}

function discard(): void {
  clearSavedFeedback()
  serverConflicts.value = []
  saveError.value = ''
  draft.value = saved.value.map((item) => ({ ...item, sources: [...item.sources] }))
}

onBeforeUnmount(() => {
  controller?.abort()
})
</script>

<template>
  <section class="group-models" aria-labelledby="group-models-heading">
    <PanelHeader heading-id="group-models-heading" :title="t('group.modelEditor.title')">
      <template #actions>
        <AppButton
          v-if="supportsModelDiscovery"
          variant="secondary"
          :busy="pending === 'discover'"
          :disabled="!query.data.value || pending !== null"
          @click="requestDiscovery"
        >
          <RefreshCw :size="16" aria-hidden="true" />{{ t('group.modelEditor.discover') }}
        </AppButton>
        <AppButton :disabled="!query.data.value || pending !== null" @click="addManual">
          <Plus :size="16" aria-hidden="true" />{{ t('group.modelEditor.add') }}
        </AppButton>
      </template>
    </PanelHeader>

    <AsyncRefreshIndicator :active="queryRefreshing" :label="t('group.modelEditor.loading')" />

    <SkeletonSurface
      v-if="(query.isPending.value && !query.data.value) || initialLoading"
      variant="collection"
      :rows="6"
      :columns="3"
      row-height="58px"
      mobile-row-height="112px"
      show-controls
      :show-pagination="false"
      :concealed="!initialLoading"
      :label="t('group.modelEditor.loading')"
    />
    <QueryFeedback
      v-else-if="query.isError.value && !query.data.value"
      state="error"
      :message="t('group.modelEditor.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="query.refetch()"
    />
    <template v-else-if="query.data.value">
      <ModelAliasEditor
        ref="modelEditor"
        :model-value="draft"
        :conflicts="conflicts"
        :labels="aliasEditorLabels"
        :create-row="createManualRow"
        :disabled="pending !== null"
        :search="routeState.search ?? ''"
        @update:model-value="updateModels"
        @update:search="setModelSearch"
      >
        <template #third-column="{ item }">
          <ModelPricingStatus
            :status="item.pricing_status"
            :labels="{
              pending: t('group.modelEditor.pricingStatus.pending'),
              configured: t('group.modelEditor.pricingStatus.configured'),
            }"
          />
        </template>
      </ModelAliasEditor>
      <ModelDiscoveryDrawer
        v-if="supportsModelDiscovery"
        :open="drawerOpen"
        :candidates="candidates"
        :current-ids="currentModelIDs"
        :loading="pending === 'discover'"
        :error="discoveryError"
        :labels="discoveryDrawerLabels"
        :dismissible="pending !== 'discover'"
        :search="routeState.discoverySearch ?? ''"
        :filter="routeState.discoveryFilter"
        @update:open="setDiscoveryOpen"
        @update:search="setDiscoverySearch"
        @update:filter="setDiscoveryFilter"
        @retry="requestDiscovery"
        @confirm="confirmCandidates"
      />

      <AppConfirmDialog
        appearance="ledger"
        :open="emptyConfirmOpen"
        :title="t('group.modelEditor.emptyConfirm.title')"
        :description="t('group.modelEditor.emptyConfirm.description')"
        :close-label="t('group.modelEditor.emptyConfirm.close')"
        :cancel-label="t('group.modelEditor.emptyConfirm.cancel')"
        :confirm-label="t('group.modelEditor.emptyConfirm.confirm')"
        tone="danger"
        :pending="pending === 'save'"
        @update:open="emptyConfirmOpen = $event"
        @confirm="save"
      />

      <StickySaveBar
        appearance="ledger"
        error-placement="floating"
        always-visible
        :dirty="dirty"
        :pending="pending === 'save'"
        :status="saveBarError ? 'error' : savedFeedback ? 'saved' : 'idle'"
        :error="saveBarError"
        :error-action-label="invalidRowCount ? t('group.modelEditor.locateFirstInvalid') : ''"
        @error-action="modelEditor?.focusFirstInvalid()"
        ><template #status
          ><div>
            <strong>
              {{
                pending === 'save'
                  ? t('group.modelEditor.saving')
                  : savedFeedback
                    ? t('group.modelEditor.savedFeedback')
                    : dirty
                      ? t('group.modelEditor.unsaved')
                      : t('group.modelEditor.saved')
              }}
            </strong>
            <span>
              {{
                pending === 'save'
                  ? t('group.modelEditor.savingNote')
                  : savedFeedback
                    ? t('group.modelEditor.savedFeedbackNote')
                    : dirty
                      ? invalidRowCount > 0
                        ? t('group.modelEditor.invalidNote', { count: invalidRowCount })
                        : t('group.modelEditor.dirtyNote')
                      : t('group.modelEditor.saveNote')
              }}
            </span>
          </div></template
        ><template #discard="{ disabled }"
          ><AppButton variant="ghost" size="sm" :disabled="disabled || !dirty" @click="discard">{{
            t('common.discard')
          }}</AppButton></template
        ><template #save="{ disabled }"
          ><AppButton size="sm" :disabled="disabled || !canSave" @click="requestSave">{{
            t('group.modelEditor.save')
          }}</AppButton></template
        ></StickySaveBar
      >
    </template>
  </section>
</template>

<style scoped>
.group-models {
  display: grid;
  gap: 0;
  min-width: 0;
  padding-top: var(--detail-panel-padding-top);
}

@media (max-width: 800px) {
  .group-models {
    padding-top: var(--detail-panel-padding-top-compact);
  }
}
</style>
