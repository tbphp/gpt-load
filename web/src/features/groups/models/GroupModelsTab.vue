<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { Info, Plus, RefreshCw, Search, X } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
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
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import StickySaveBar from '@/components/ui/StickySaveBar.vue'

import {
  createModelDraft,
  findModelNameConflicts,
  indexesWithConflicts,
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
const emptyConfirmOpen = ref(false)
const candidates = ref<string[]>([])
const selectedCandidates = ref<string[]>([])
const drawerFilter = ref<'available' | 'all'>('available')
const drawerSearch = ref('')
const modelSearch = ref('')
const savedFeedback = ref(false)
let nextKey = 1
let controller: AbortController | undefined
let savedFeedbackTimer: ReturnType<typeof setTimeout> | undefined

const conflicts = computed(() =>
  serverConflicts.value.length
    ? serverConflicts.value
    : findModelNameConflicts(normalizedModels(draft.value)),
)
const conflictIndexes = computed(() => indexesWithConflicts(conflicts.value))
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
    new Set([...conflictIndexes.value, ...emptyAliasIndexes.value, ...emptyIDIndexes.value]).size,
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
const currentModelIDs = computed(() => new Set(draft.value.map((item) => item.id.trim())))
const availableCandidates = computed(() =>
  candidates.value.filter((candidate) => !currentModelIDs.value.has(candidate)),
)
const visibleCandidates = computed(() => {
  const query = drawerSearch.value.trim().toLocaleLowerCase()
  const source = drawerFilter.value === 'available' ? availableCandidates.value : candidates.value
  return query
    ? source.filter((candidate) => candidate.toLocaleLowerCase().includes(query))
    : source
})
const selectableVisibleCandidates = computed(() =>
  visibleCandidates.value.filter((candidate) => !currentModelIDs.value.has(candidate)),
)
const allVisibleCandidatesSelected = computed(
  () =>
    selectableVisibleCandidates.value.length > 0 &&
    selectableVisibleCandidates.value.every((candidate) =>
      selectedCandidates.value.includes(candidate),
    ),
)
const visibleDraftRows = computed(() => {
  const search = modelSearch.value.trim().toLocaleLowerCase()
  return draft.value
    .map((item, index) => ({ item, index }))
    .filter(
      ({ item }) =>
        !search ||
        item.id.toLocaleLowerCase().includes(search) ||
        item.alias.toLocaleLowerCase().includes(search),
    )
})
const knownPricingStatusByID = computed(
  () => new Map(saved.value.map((item) => [item.id, item.pricing_status] as const)),
)
const drawerFilterOptions = computed(() => [
  { value: 'all', label: t('group.modelEditor.drawer.filterAll') },
  { value: 'available', label: t('group.modelEditor.drawer.filterAvailable') },
])
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

function updateRow(index: number, patch: Partial<ModelDraftItem>): void {
  serverConflicts.value = []
  const nextPatch =
    patch.id === undefined ? patch : { ...patch, pricing_status: pricingStatusForID(patch.id) }
  draft.value = draft.value.map((item, current) =>
    current === index
      ? {
          ...item,
          ...nextPatch,
          alias: nextPatch.alias_enabled === false ? '' : (nextPatch.alias ?? item.alias),
        }
      : { ...item },
  )
}

async function setAliasEnabled(index: number, key: number, enabled: boolean): Promise<void> {
  updateRow(index, { alias_enabled: enabled })
  if (!enabled) return

  await nextTick()
  document.getElementById(`group-model-alias-${key}`)?.focus()
}

function pricingStatusForID(id: string): ModelDraftItem['pricing_status'] {
  return knownPricingStatusByID.value.get(id.trim()) ?? 'unpriced'
}

function removeRow(index: number): void {
  serverConflicts.value = []
  draft.value = draft.value.filter((_, current) => current !== index)
}

async function addManual(): Promise<void> {
  serverConflicts.value = []
  modelSearch.value = ''
  const key = nextKey++
  draft.value = [
    ...draft.value,
    {
      id: '',
      alias: '',
      alias_enabled: false,
      pricing_status: 'unpriced',
      editable_id: true,
      key,
    },
  ]
  await nextTick()
  const input = document.querySelector<HTMLInputElement>('[data-model-key="' + key + '"]')
  input?.scrollIntoView({ block: 'nearest' })
  input?.focus()
}

function requestDiscovery(): void {
  if (pending.value) return
  drawerOpen.value = true
  candidates.value = []
  selectedCandidates.value = []
  drawerFilter.value = 'available'
  drawerSearch.value = ''
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
    selectedCandidates.value = []
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

function confirmCandidates(): void {
  const present = new Set(draft.value.map((item) => item.id.trim()))
  const additions = selectedCandidates.value
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

function toggleVisibleCandidates(): void {
  const next = new Set(selectedCandidates.value)
  if (allVisibleCandidatesSelected.value) {
    for (const candidate of selectableVisibleCandidates.value) next.delete(candidate)
  } else {
    for (const candidate of selectableVisibleCandidates.value) next.add(candidate)
  }
  selectedCandidates.value = [...next]
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

function conflictMessage(index: number): string {
  const conflict = conflicts.value.find((item) => item.indexes.includes(index))
  return conflict ? t('group.modelEditor.nameConflict', { name: conflict.client_model }) : ''
}

function modelIDError(item: ModelDraftItem, index: number): string {
  if (emptyIDIndexes.value.has(index)) return t('group.modelEditor.manualIdRequired')
  if (!item.alias_enabled && conflictIndexes.value.has(index)) return conflictMessage(index)
  return ''
}

function modelAliasError(item: ModelDraftItem, index: number): string {
  if (!item.alias_enabled) return ''
  if (emptyAliasIndexes.value.has(index)) return t('group.modelEditor.aliasRequired')
  return conflictIndexes.value.has(index) ? conflictMessage(index) : ''
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
      <div class="group-models__toolbar">
        <label class="group-models__search">
          <span>{{ t('group.modelEditor.searchLabel') }}</span>
          <span class="group-models__search-control">
            <Search :size="15" aria-hidden="true" />
            <input
              v-model="modelSearch"
              type="search"
              :placeholder="t('group.modelEditor.searchPlaceholder')"
            />
            <IconButton
              v-if="modelSearch"
              class="group-models__search-clear"
              size="xs"
              variant="ghost"
              :label="t('group.modelEditor.searchClear')"
              @click="modelSearch = ''"
            >
              <X :size="14" aria-hidden="true" />
            </IconButton>
          </span>
        </label>
        <div class="group-models__summary" aria-live="polite">
          <span>{{
            unpriced
              ? t('group.modelEditor.summary', { count: draft.length, unpriced })
              : t('group.modelEditor.total', { count: draft.length })
          }}</span>
        </div>
      </div>
      <LedgerRecordList
        :label="t('group.modelEditor.tableLabel')"
        :row-count="visibleDraftRows.length + 1"
        grid-class="group-model-record-grid"
      >
        <template #header>
          <span role="columnheader">{{ t('group.modelEditor.id') }}</span>
          <span role="columnheader">{{ t('group.modelEditor.alias') }}</span>
          <span role="columnheader">{{ t('group.modelEditor.pricing') }}</span>
          <span role="columnheader">
            <span class="sr-only">{{ t('group.modelEditor.actions') }}</span>
          </span>
        </template>

        <article
          v-for="({ item, index }, visibleIndex) in visibleDraftRows"
          :key="item.key"
          class="ledger-record-list__record group-model-record"
          :class="{ 'group-model-record--conflict': conflictIndexes.has(index) }"
          role="row"
          :aria-rowindex="visibleIndex + 2"
        >
          <div class="ledger-record-list__cell group-model-record__id" role="cell">
            <span class="group-model-record__mobile-label">{{ t('group.modelEditor.id') }}</span>
            <CompactFieldError
              :id="`group-model-id-${item.key}`"
              class="group-model-record__id-field"
              :error="modelIDError(item, index)"
            >
              <template #default="{ invalid, describedBy }">
                <input
                  v-if="item.editable_id"
                  :id="`group-model-id-${item.key}`"
                  :data-model-key="item.key"
                  class="group-model-record__id-input"
                  type="text"
                  :value="item.id"
                  :placeholder="t('group.modelEditor.manualId')"
                  :aria-label="t('group.modelEditor.id')"
                  :aria-invalid="invalid || undefined"
                  :aria-describedby="describedBy"
                  :disabled="pending !== null"
                  @input="updateRow(index, { id: ($event.target as HTMLInputElement).value })"
                />
                <code v-else :aria-describedby="describedBy">{{ item.id }}</code>
              </template>
            </CompactFieldError>
          </div>

          <div class="ledger-record-list__cell group-model-record__alias-cell" role="cell">
            <span class="group-model-record__mobile-label">{{ t('group.modelEditor.alias') }}</span>
            <div class="group-models__alias">
              <label
                class="group-models__alias-toggle"
                :class="{ 'group-models__alias-toggle--disabled': pending !== null }"
              >
                <span class="sr-only">
                  {{ t('group.modelEditor.aliasEnabledFor', { id: item.id }) }}
                </span>
                <input
                  type="checkbox"
                  :checked="item.alias_enabled"
                  :disabled="pending !== null"
                  @change="
                    setAliasEnabled(index, item.key, ($event.target as HTMLInputElement).checked)
                  "
                />
              </label>
              <CompactFieldError
                v-if="item.alias_enabled"
                :id="`group-model-alias-${item.key}`"
                class="group-models__alias-field"
                :error="modelAliasError(item, index)"
              >
                <template #default="{ invalid, describedBy }">
                  <input
                    :id="`group-model-alias-${item.key}`"
                    type="text"
                    :value="item.alias"
                    :disabled="pending !== null"
                    :placeholder="t('group.modelEditor.aliasPlaceholder')"
                    :aria-label="t('group.modelEditor.alias')"
                    :aria-invalid="invalid || undefined"
                    :aria-describedby="describedBy"
                    @input="updateRow(index, { alias: ($event.target as HTMLInputElement).value })"
                  />
                </template>
              </CompactFieldError>
            </div>
          </div>

          <div class="ledger-record-list__cell group-model-record__pricing-cell" role="cell">
            <span class="group-model-record__mobile-label">{{
              t('group.modelEditor.pricing')
            }}</span>
            <span
              :class="['group-models__pricing', `group-models__pricing--${item.pricing_status}`]"
            >
              {{ t(`group.modelEditor.pricingStatus.${item.pricing_status}`) }}
            </span>
          </div>

          <div class="ledger-record-list__cell group-model-record__actions" role="cell">
            <IconButton
              variant="ghost"
              size="compact"
              :disabled="pending !== null"
              :label="t('group.modelEditor.removeFor', { id: item.id })"
              @click="removeRow(index)"
            >
              <X :size="16" aria-hidden="true" />
            </IconButton>
          </div>
        </article>
      </LedgerRecordList>
      <AppButton
        class="group-models__inline-add"
        variant="link"
        size="inline"
        :disabled="pending !== null"
        @click="addManual"
      >
        <Plus :size="16" aria-hidden="true" />{{ t('group.modelEditor.addInline') }}
      </AppButton>
      <div class="group-models__note">
        <Info :size="16" aria-hidden="true" />
        <span>{{ t('group.modelEditor.discoveryNotice') }}</span>
      </div>

      <AppDrawer
        appearance="ledger"
        :open="drawerOpen"
        :title="t('group.modelEditor.drawer.title')"
        :description="t('group.modelEditor.drawer.description')"
        :close-label="t('group.modelEditor.drawer.close')"
        :dismissible="pending !== 'discover'"
        show-description
        @update:open="drawerOpen = $event"
      >
        <template #filters>
          <label class="group-models__drawer-search" data-input-shell>
            <span class="sr-only">{{ t('group.modelEditor.drawer.search') }}</span>
            <Search :size="15" aria-hidden="true" />
            <input
              v-model="drawerSearch"
              data-input-inner
              type="search"
              :placeholder="t('group.modelEditor.drawer.search')"
            />
          </label>
          <SegmentedControl
            v-model="drawerFilter"
            :label="t('group.modelEditor.drawer.filterLabel')"
            :options="drawerFilterOptions"
            appearance="drawer"
          />
        </template>
        <QueryFeedback
          v-if="pending === 'discover'"
          state="loading"
          :message="t('group.modelEditor.drawer.loading')"
        />
        <InlineFeedback v-else-if="error" tone="danger">{{ error }}</InlineFeedback>
        <template v-else>
          <div class="group-models__candidate-list">
            <label
              v-for="candidate in visibleCandidates"
              :key="candidate"
              class="group-models__candidate"
              :class="{ 'group-models__candidate--added': currentModelIDs.has(candidate) }"
            >
              <input
                v-model="selectedCandidates"
                type="checkbox"
                :value="candidate"
                :disabled="currentModelIDs.has(candidate)"
              />
              <code>{{ candidate }}</code>
              <span>{{
                currentModelIDs.has(candidate)
                  ? t('group.modelEditor.drawer.alreadyAdded')
                  : t('group.modelEditor.drawer.filterAvailable')
              }}</span>
            </label>
            <InlineFeedback v-if="!visibleCandidates.length" tone="warning">{{
              candidates.length
                ? t('group.modelEditor.drawer.noMatches')
                : t('group.modelEditor.drawer.empty')
            }}</InlineFeedback>
          </div>
        </template>
        <template #footer>
          <div class="group-models__drawer-footer">
            <div class="group-models__drawer-selection">
              <AppButton
                variant="secondary"
                size="compact"
                :disabled="pending === 'discover' || !selectableVisibleCandidates.length"
                @click="toggleVisibleCandidates"
              >
                {{
                  allVisibleCandidatesSelected
                    ? t('group.modelEditor.drawer.clearAll')
                    : t('group.modelEditor.drawer.selectAll')
                }}
              </AppButton>
              <i18n-t tag="span" keypath="group.modelEditor.drawer.selected">
                <template #count>
                  <strong class="group-models__drawer-count">{{
                    selectedCandidates.length
                  }}</strong>
                </template>
              </i18n-t>
            </div>
            <div class="group-models__drawer-actions">
              <AppButton
                variant="secondary"
                :disabled="pending === 'discover'"
                @click="drawerOpen = false"
                >{{ t('common.cancel') }}</AppButton
              ><AppButton
                :disabled="pending === 'discover' || !selectedCandidates.length"
                @click="confirmCandidates"
                >{{ t('group.modelEditor.drawer.confirm') }}</AppButton
              >
            </div>
          </div>
        </template>
      </AppDrawer>

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
.group-models__drawer-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}
.group-models__drawer-actions + p {
  color: var(--color-text-muted);
}
.group-models__toolbar {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 10px;
  padding-bottom: 13px;
}
.group-models__summary {
  display: flex;
  min-height: 32px;
  align-items: center;
  gap: var(--space-3);
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.group-models__search {
  display: grid;
  min-width: 260px;
  flex: 1;
  gap: 5px;
  color: var(--color-text-faint);
  font-size: var(--text-meta);
}
.group-models__search-control {
  position: relative;
  display: block;
}
.group-models__search-control > svg {
  position: absolute;
  top: 50%;
  left: 11px;
  transform: translateY(-50%);
  pointer-events: none;
}
.group-models__search-control > input {
  width: 100%;
  height: 32px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 34px;
  font: inherit;
  font-size: var(--text-meta);
}
.group-models__search-clear {
  position: absolute;
  top: 2px;
  right: 3px;
}
.group-model-record-grid {
  --ledger-record-list-record-min-height: 58px;
  --ledger-record-list-record-padding: 9px 0;
  --ledger-record-list-grid: minmax(180px, 1.05fr) minmax(280px, 1.6fr) 140px 48px;
  --ledger-record-list-column-gap: 16px;
}
.group-model-record--conflict {
  background: var(--color-danger-bg);
}
.group-model-record__id,
.group-model-record__pricing-cell {
  min-width: 0;
}
.group-model-record__id {
  display: grid;
  align-content: center;
  gap: 5px;
}
.group-model-record__id code {
  display: block;
  padding-inline-end: 38px;
  overflow-wrap: anywhere;
  font-size: var(--text-sm);
}
.group-model-record__id-field {
  display: flex;
  min-height: 34px;
  align-items: center;
  max-width: 300px;
}
.group-model-record__id-field code {
  width: 100%;
}
.group-model-record__id-input {
  width: 100%;
  max-width: 300px;
  min-height: 34px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 5px 10px;
  font: var(--text-sm) var(--font-mono);
}
.group-model-record__alias-cell {
  display: grid;
  gap: var(--space-1);
}
.group-models__alias {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.group-models__alias-field {
  width: 100%;
  max-width: 300px;
  min-width: 0;
  flex: 1;
}
.group-models__alias-toggle {
  display: grid;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  place-items: center;
  cursor: pointer;
}
.group-models__alias-toggle input {
  width: 16px;
  height: 16px;
  margin: 0;
  accent-color: var(--color-action);
  cursor: pointer;
}
.group-models__alias-toggle--disabled,
.group-models__alias-toggle input:disabled {
  cursor: not-allowed;
}
.group-models__alias-toggle input:disabled {
  opacity: 0.55;
}
.group-models__alias input[type='text'] {
  width: 100%;
  max-width: 300px;
  min-height: 34px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 5px 10px;
  font: inherit;
  font-size: var(--text-meta);
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
.group-model-record__actions {
  display: flex;
  justify-content: flex-end;
}
.group-model-record__actions :deep(.icon-button:hover:not(:disabled)) {
  border-color: var(--color-danger);
  color: var(--color-danger);
}
.group-model-record__mobile-label {
  display: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}
.group-models__inline-add {
  width: fit-content;
  min-height: var(--control-md);
  justify-content: flex-start;
  margin-top: 13px;
  padding: 5px 1px;
  font-size: inherit;
  font-weight: 560;
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
.group-models__candidate {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 12px 1px;
}
.group-models__drawer-search {
  display: flex;
  width: auto;
  min-width: 0;
  height: 32px;
  flex: 1;
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: 0 10px;
  color: var(--color-text-muted);
}
.group-models__drawer-search input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--color-text);
  font: inherit;
}
.group-models__candidate-list {
  display: grid;
}
.group-models__candidate input {
  width: 16px;
  height: 16px;
  accent-color: var(--color-action);
}
.group-models__candidate code {
  min-width: 0;
  overflow: hidden;
  font-size: var(--text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.group-models__candidate > span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.group-models__candidate--added {
  color: var(--color-text-muted);
}
.group-models__drawer-footer {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.group-models__drawer-selection {
  display: flex;
  align-items: center;
  gap: 10px;
}
.group-models__drawer-count {
  color: var(--color-text);
  font-family: var(--font-mono);
}
.group-models__drawer-selection :deep(.app-button) {
  min-height: 32px;
  padding: 5px 9px;
  font-size: var(--text-sm);
}
.group-models__drawer-actions {
  justify-content: flex-end;
}
@media (max-width: 520px) {
  .group-models__drawer-footer {
    align-items: stretch;
    flex-direction: column;
  }
  .group-models__drawer-actions :deep(.app-button) {
    flex: 1;
  }
  .group-models__drawer-actions {
    display: grid;
    grid-template-columns: 1fr 1fr;
  }
}
@media (max-width: 860px) {
  .group-model-record-grid {
    --ledger-record-list-card-grid: minmax(0, 0.7fr) minmax(0, 1.3fr);
  }
  .group-model-record {
    padding-right: 58px;
  }
  .group-model-record__id,
  .group-model-record__alias-cell {
    grid-column: 1 / -1;
  }
  .group-model-record__id,
  .group-model-record__alias-cell,
  .group-model-record__pricing-cell {
    display: grid;
    align-content: start;
    gap: 5px;
  }
  .group-model-record__alias-cell {
    border-top: 1px solid var(--color-border-subtle);
    padding-top: 11px;
  }
  .group-model-record__actions {
    position: absolute;
    top: 4px;
    right: 4px;
  }
  .group-model-record__actions :deep(.icon-button) {
    min-height: var(--touch-target);
  }
  .group-model-record__mobile-label {
    display: inline;
  }
  .group-model-record__id-input,
  .group-models__alias input[type='text'] {
    max-width: none;
    min-height: var(--touch-target);
    font-size: 16px;
  }
  .group-model-record__id-field,
  .group-models__alias-field {
    max-width: none;
  }
  .group-model-record__id-field {
    min-height: var(--touch-target);
  }
  .group-models__alias-toggle {
    width: var(--touch-target);
    height: var(--touch-target);
    flex-basis: var(--touch-target);
  }
}
@media (max-width: 800px) {
  .group-models__toolbar {
    align-items: stretch;
    flex-direction: column;
  }
  .group-models__search {
    min-width: 0;
  }
  .group-models__search-control > input {
    height: var(--touch-target);
    font-size: 16px;
  }
  .group-models__search-clear {
    top: 0;
    right: 0;
    width: var(--touch-target);
    height: var(--touch-target);
  }
  .group-models__drawer-search {
    height: var(--touch-target);
  }
  .group-models__drawer-search input {
    font-size: 16px;
  }
  .group-models__drawer-selection :deep(.app-button),
  .group-models__drawer-actions :deep(.app-button) {
    min-height: var(--touch-target);
  }
}
@media (max-width: 800px) {
  .group-models {
    padding-top: var(--detail-panel-padding-top-compact);
  }
}
</style>
