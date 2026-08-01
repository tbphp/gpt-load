<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { RefreshCw } from '@lucide/vue'
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
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StickySaveBar from '@/components/ui/StickySaveBar.vue'
import ModelAliasEditor from '@/features/models/ModelAliasEditor.vue'
import ModelDiscoveryDrawer from '@/features/models/ModelDiscoveryDrawer.vue'

import {
  createModelDraft,
  findModelNameConflicts,
  modelDraftValidity,
  normalizedModels,
  sameModels,
  type ModelAliasEditorLabels,
  type ModelDiscoveryDrawerLabels,
  type ModelDraftValue,
  type ModelNameConflict,
} from '@/features/models/model-draft'

interface GroupModelDraftItem extends ModelDraftValue {
  pricing_status: 'priced' | 'unpriced'
}

const props = defineProps<{ groupId: number }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const query = useQuery(groupModelsQueryOptions(client, () => props.groupId))
const saved = ref<GroupModelDraftItem[]>([])
const draft = ref<GroupModelDraftItem[]>([])
const pending = ref<'discover' | 'save' | null>(null)
const error = ref('')
const serverConflicts = ref<ModelNameConflict[]>([])
const drawerOpen = ref(false)
const emptyConfirmOpen = ref(false)
const candidates = ref<string[]>([])
let nextKey = 1
let controller: AbortController | undefined

const conflicts = computed(() =>
  serverConflicts.value.length
    ? serverConflicts.value
    : findModelNameConflicts(normalizedModels(draft.value)),
)
const validity = computed(() => modelDraftValidity(draft.value, conflicts.value))
const dirty = computed(() => !sameModels(saved.value, draft.value))
const empty = computed(() => normalizedModels(draft.value).length === 0)
const canSave = computed(
  () =>
    dirty.value &&
    pending.value === null &&
    conflicts.value.length === 0 &&
    validity.value.emptyAliasIndexes.size === 0,
)
const unpriced = computed(
  () => draft.value.filter((item) => item.pricing_status === 'unpriced').length,
)
const currentModelIDs = computed(() => draft.value.map((item) => item.id.trim()))
const aliasEditorLabels = computed<ModelAliasEditorLabels>(() => ({
  tableLabel: t('group.modelEditor.tableLabel'),
  id: t('group.modelEditor.id'),
  alias: t('group.modelEditor.alias'),
  thirdColumn: t('group.modelEditor.pricing'),
  actions: t('group.modelEditor.actions'),
  search: t('group.modelEditor.search'),
  clearSearch: t('group.modelEditor.clearSearch'),
  aliasEnabledFor: (id) => t('group.modelEditor.aliasEnabledFor', { id }),
  aliasFor: (id) => t('group.modelEditor.aliasFor', { id }),
  aliasPlaceholder: t('group.modelEditor.aliasPlaceholder'),
  aliasRequired: t('group.modelEditor.aliasRequired'),
  removeFor: (id) => t('group.modelEditor.removeFor', { id }),
  manualId: t('group.modelEditor.manualId'),
  add: t('group.modelEditor.add'),
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
  notice: t('group.modelEditor.drawer.notice'),
  search: t('group.modelEditor.drawer.search'),
  filterLabel: t('group.modelEditor.drawer.filterLabel'),
  filterUnadded: t('group.modelEditor.drawer.filterAvailable'),
  filterAll: t('group.modelEditor.drawer.filterAll'),
  alreadyAdded: t('group.modelEditor.drawer.alreadyAdded'),
  unadded: t('group.modelEditor.drawer.unadded'),
  noMatches: t('group.modelEditor.drawer.noMatches'),
  empty: t('group.modelEditor.drawer.empty'),
  selected: (count) => t('group.modelEditor.drawer.selected', { count }),
  selectAll: t('group.modelEditor.drawer.selectAll'),
  deselectAll: t('group.modelEditor.drawer.deselectAll'),
  retry: t('common.retry'),
  cancel: t('common.cancel'),
  confirm: t('group.modelEditor.drawer.confirm'),
}))
useUnsavedChanges(dirty, { blocked: computed(() => pending.value !== null) })

watch(
  () => query.data.value,
  (models) => {
    if (!models || dirty.value || pending.value) return
    const next = createModelDraft(models.items, () => nextKey++)
    saved.value = next
    draft.value = next.map((item) => ({ ...item }))
    serverConflicts.value = []
  },
  { immediate: true },
)

function updateDraft(value: GroupModelDraftItem[]): void {
  serverConflicts.value = []
  draft.value = value
}

function createManualRow(id: string): GroupModelDraftItem {
  return {
    id,
    alias: '',
    alias_enabled: false,
    pricing_status: 'unpriced',
    key: nextKey++,
  }
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
  candidates.value = []
  error.value = ''
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
      pricing_status: 'unpriced' as const,
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
    const next = createModelDraft(result.items, () => nextKey++)
    saved.value = next
    draft.value = next.map((item) => ({ ...item }))
    emptyConfirmOpen.value = false
    cacheGroupModels(queryClient, props.groupId, result)
    await invalidateGroupSummary(queryClient, props.groupId)
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
  serverConflicts.value = []
  error.value = ''
  draft.value = saved.value.map((item) => ({ ...item }))
}

onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <section class="group-models" aria-labelledby="group-models-heading">
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
      <header class="group-models__header">
        <div>
          <h2 id="group-models-heading">{{ t('group.modelEditor.title') }}</h2>
          <p>{{ t('group.modelEditor.description') }}</p>
        </div>
        <AppButton
          variant="secondary"
          :busy="pending === 'discover'"
          :disabled="pending !== null"
          @click="requestDiscovery"
        >
          <RefreshCw :size="16" aria-hidden="true" />{{ t('group.modelEditor.discover') }}
        </AppButton>
      </header>

      <InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback>

      <div class="group-models__summary" aria-live="polite">
        <span>{{ t('group.modelEditor.total', { count: draft.length }) }}</span>
        <span v-if="unpriced">{{ t('group.modelEditor.unpriced', { count: unpriced }) }}</span>
      </div>
      <ModelAliasEditor
        :model-value="draft"
        :conflicts="conflicts"
        :labels="aliasEditorLabels"
        :create-row="createManualRow"
        :disabled="pending !== null"
        @update:model-value="updateDraft"
      >
        <template #third-column="{ item }">
          <span :class="['group-models__pricing', `group-models__pricing--${item.pricing_status}`]">
            {{ t(`group.modelEditor.pricingStatus.${item.pricing_status}`) }}
          </span>
        </template>
      </ModelAliasEditor>

      <ModelDiscoveryDrawer
        :open="drawerOpen"
        :candidates="candidates"
        :current-ids="currentModelIDs"
        :loading="pending === 'discover'"
        :error="error"
        :labels="discoveryDrawerLabels"
        :dismissible="pending !== 'discover'"
        @update:open="drawerOpen = $event"
        @retry="runDiscovery"
        @confirm="confirmCandidates"
      />

      <AppConfirmDialog
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
        :dirty="dirty"
        :pending="pending === 'save'"
        :status="error ? 'error' : 'idle'"
        :error="error"
        ><template #status
          ><span>{{
            dirty ? t('group.modelEditor.unsaved') : t('group.modelEditor.saved')
          }}</span></template
        ><template #discard="{ disabled }"
          ><AppButton variant="secondary" :disabled="disabled || !dirty" @click="discard">{{
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
  gap: var(--space-4);
  min-width: 0;
}
.group-models__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}
.group-models__header h2,
.group-models__header p {
  margin: 0;
}
.group-models__header p {
  color: var(--color-text-muted);
}
.group-models__summary {
  display: flex;
  gap: var(--space-3);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.group-models__pricing--priced {
  color: var(--color-success);
}
.group-models__pricing--unpriced {
  color: var(--color-warning);
}
@media (max-width: 640px) {
  .group-models__header {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
