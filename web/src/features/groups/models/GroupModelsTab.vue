<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { Info, Plus, RefreshCw } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { ApiError, RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import {
  cacheGroupModels,
  discoverGroupModels,
  groupModelsQueryOptions,
  replaceGroupModelsResource,
} from '@/app/resources/groups'
import { invalidateGroupSummary } from '@/app/resources/groups'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StickySaveBar from '@/components/ui/StickySaveBar.vue'
import ModelAliasEditor from '@/features/models/ModelAliasEditor.vue'
import ModelDiscoveryDrawer from '@/features/models/ModelDiscoveryDrawer.vue'
import type {
  ModelAliasEditorLabels,
  ModelDiscoveryDrawerLabels,
} from '@/features/models/model-draft'

import {
  createModelDraft,
  findModelNameConflicts,
  normalizedModels,
  sameModels,
  type ModelDraftItem,
  type ModelNameConflict,
} from './model-diff'

const props = defineProps<{ groupId: number }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const query = useQuery(groupModelsQueryOptions(client, () => props.groupId))
const saved = ref<ModelDraftItem[]>([])
const draft = ref<ModelDraftItem[]>([])
const pending = ref<'discover' | 'save' | null>(null)
const error = ref('')
const serverConflicts = ref<ModelNameConflict[]>([])
const drawerOpen = ref(false)
const modelEditor = ref<{ addManual: () => Promise<void> }>()
const emptyConfirmOpen = ref(false)
const candidates = ref<string[]>([])
const savedFeedback = ref(false)
let nextKey = 1
let controller: AbortController | undefined
let savedFeedbackTimer: ReturnType<typeof setTimeout> | undefined

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
const saveBarError = computed(() =>
  conflicts.value.length ? t('group.modelEditor.conflictSummary') : error.value,
)
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
const unpriced = computed(
  () => draft.value.filter((item) => item.pricing_status === 'unpriced').length,
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
    unpriced.value
      ? t('group.modelEditor.summary', { count, unpriced: unpriced.value })
      : t('group.modelEditor.total', { count }),
  empty: t('group.modelEditor.empty'),
  noMatches: t('group.modelEditor.noMatches'),
  conflictSummary: t('group.modelEditor.conflictSummary'),
  emptyAliasSummary: t('group.modelEditor.emptyAliasSummary'),
  locateFirstInvalid: t('group.modelEditor.locateFirstInvalid'),
  nameConflict: (name) => t('group.modelEditor.nameConflict', { name }),
}))
const discoveryDrawerLabels = computed<ModelDiscoveryDrawerLabels>(() => ({
  title: t('group.modelEditor.drawer.title'),
  description: t('group.modelEditor.drawer.description'),
  close: t('group.modelEditor.drawer.close'),
  loading: t('group.modelEditor.drawer.loading'),
  notice: t('group.modelEditor.discoveryNotice'),
  search: t('group.modelEditor.drawer.search'),
  filterLabel: t('group.modelEditor.drawer.filterLabel'),
  filterUnadded: t('group.modelEditor.drawer.filterAvailable'),
  filterAll: t('group.modelEditor.drawer.filterAll'),
  alreadyAdded: t('group.modelEditor.drawer.alreadyAdded'),
  unadded: t('group.modelEditor.drawer.filterAvailable'),
  noMatches: t('group.modelEditor.drawer.noMatches'),
  empty: t('group.modelEditor.drawer.empty'),
  selected: (count) => t('group.modelEditor.drawer.selected', { count }),
  selectAll: t('group.modelEditor.drawer.selectAll'),
  deselectAll: t('group.modelEditor.drawer.clearAll'),
  retry: t('common.retry'),
  cancel: t('common.cancel'),
  confirm: t('group.modelEditor.drawer.confirm'),
}))
useUnsavedChanges(dirty, { blocked: computed(() => pending.value !== null) })

watch(dirty, (isDirty) => {
  if (isDirty) clearSavedFeedback()
})

watch(
  () => query.data.value,
  (models) => {
    if (!models || dirty.value || pending.value) return
    const next = createModelDraft(models.items).map((item) => ({ ...item, key: nextKey++ }))
    saved.value = next
    draft.value = next.map((item) => ({ ...item }))
    serverConflicts.value = []
  },
  { immediate: true },
)

function updateModels(models: ModelDraftItem[]): void {
  serverConflicts.value = []
  const previousByKey = new Map(draft.value.map((item) => [item.key, item] as const))
  draft.value = models.map((item) => {
    const previous = previousByKey.get(item.key)
    return {
      ...item,
      pricing_status:
        previous && previous.id === item.id ? item.pricing_status : pricingStatusForID(item.id),
    }
  })
}

function pricingStatusForID(id: string): ModelDraftItem['pricing_status'] {
  return knownPricingStatusByID.value.get(id.trim()) ?? 'unpriced'
}

function createManualRow(): ModelDraftItem {
  return {
    id: '',
    alias: '',
    alias_enabled: false,
    pricing_status: 'unpriced',
    editable_id: true,
    key: nextKey++,
  }
}

function addManual(): void {
  void modelEditor.value?.addManual()
}

function requestDiscovery(): void {
  if (pending.value) return
  drawerOpen.value = true
  candidates.value = []
  error.value = ''
  void runDiscovery()
}

async function runDiscovery(): Promise<void> {
  controller?.abort()
  const active = new AbortController()
  controller = active
  pending.value = 'discover'
  try {
    const result = await discoverGroupModels(client, props.groupId, active.signal)
    if (controller !== active) return
    candidates.value = [...new Set(result.models.map((id) => id.trim()).filter(Boolean))]
  } catch (cause: unknown) {
    if (cause instanceof RequestCancelledError || controller !== active) return
    error.value =
      cause instanceof ApiError && cause.code === 'NO_ACTIVE_UPSTREAM_KEY'
        ? t('group.modelEditor.noActiveKey.title')
        : t('group.modelEditor.discoveryFailed')
  } finally {
    if (controller === active) {
      controller = undefined
      pending.value = null
    }
  }
}

function confirmCandidates(selectedCandidates: string[]): void {
  const present = new Set(draft.value.map((item) => item.id.trim()))
  const additions = selectedCandidates
    .map((id) => id.trim())
    .filter((id) => id && !present.has(id))
    .map((id) => ({
      id,
      alias: '',
      alias_enabled: false,
      pricing_status: pricingStatusForID(id),
      key: nextKey++,
    }))
  draft.value = [...draft.value, ...additions]
  drawerOpen.value = false
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
  error.value = ''
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
    draft.value = next.map((item) => ({ ...item }))
    emptyConfirmOpen.value = false
    cacheGroupModels(queryClient, props.groupId, result)
    await invalidateGroupSummary(queryClient, props.groupId)
    showSavedFeedback()
  } catch (cause: unknown) {
    if (cause instanceof RequestCancelledError || controller !== active) return
    if (cause instanceof ApiError && cause.code === 'MODEL_NAME_CONFLICT') {
      const data = cause.data as { conflicts?: ModelNameConflict[] }
      if (Array.isArray(data.conflicts)) serverConflicts.value = data.conflicts
    }
    error.value = t('group.modelEditor.saveFailed')
  } finally {
    if (controller === active) {
      controller = undefined
      pending.value = null
    }
  }
}

function discard(): void {
  clearSavedFeedback()
  serverConflicts.value = []
  error.value = ''
  draft.value = saved.value.map((item) => ({ ...item }))
}

function clearSavedFeedback(): void {
  if (savedFeedbackTimer !== undefined) clearTimeout(savedFeedbackTimer)
  savedFeedbackTimer = undefined
  savedFeedback.value = false
}

function showSavedFeedback(): void {
  clearSavedFeedback()
  savedFeedback.value = true
  savedFeedbackTimer = setTimeout(() => {
    savedFeedback.value = false
    savedFeedbackTimer = undefined
  }, 1_600)
}

onBeforeUnmount(() => {
  controller?.abort()
  if (savedFeedbackTimer !== undefined) clearTimeout(savedFeedbackTimer)
})
</script>

<template>
  <section class="group-models" aria-labelledby="group-models-heading">
    <PanelHeader heading-id="group-models-heading" :title="t('group.modelEditor.title')">
      <template #actions>
        <AppButton
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
    <QueryFeedback
      v-if="query.isPending.value && !query.data.value"
      state="loading"
      :message="t('group.modelEditor.loading')"
    />
    <QueryFeedback
      v-else-if="query.isError.value && !query.data.value"
      state="error"
      :message="t('group.modelEditor.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="query.refetch()"
    />
    <template v-else-if="query.data.value">
      <InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback>
      <ModelAliasEditor
        ref="modelEditor"
        :model-value="draft"
        :conflicts="conflicts"
        :labels="aliasEditorLabels"
        :create-row="createManualRow"
        :disabled="pending !== null"
        @update:model-value="updateModels"
      >
        <template #third-column="{ item }">
          <span :class="['group-models__pricing', `group-models__pricing--${item.pricing_status}`]">
            {{ t(`group.modelEditor.pricingStatus.${item.pricing_status}`) }}
          </span>
        </template>
      </ModelAliasEditor>
      <div class="group-models__note">
        <Info :size="16" aria-hidden="true" />
        <span>{{ t('group.modelEditor.discoveryNotice') }}</span>
      </div>

      <ModelDiscoveryDrawer
        :open="drawerOpen"
        :candidates="candidates"
        :current-ids="currentModelIDs"
        :loading="pending === 'discover'"
        :error="error"
        :labels="discoveryDrawerLabels"
        :dismissible="pending !== 'discover'"
        @update:open="drawerOpen = $event"
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
          ><AppButton variant="ghost" :disabled="disabled || !dirty" @click="discard">{{
            t('common.discard')
          }}</AppButton></template
        ><template #save="{ disabled }"
          ><AppButton :disabled="disabled || !canSave" @click="requestSave">{{
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

.group-models__pricing--priced {
  color: var(--color-success);
}

.group-models__pricing--unpriced {
  color: var(--color-warning);
}

.group-models__pricing {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  white-space: nowrap;
}
.group-models__pricing::before {
  width: 6px;
  height: 6px;
  flex: none;
  border-radius: 50%;
  background: currentColor;
  content: '';
}

.group-models__note {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-top: 18px;
  border: 1px solid var(--color-border-subtle);
  border-radius: 8px;
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 10px 12px;
  font-size: var(--text-sm);
}
.group-models__note > svg {
  flex: none;
}

@media (max-width: 800px) {
  .group-models {
    padding-top: var(--detail-panel-padding-top-compact);
  }
}
</style>
