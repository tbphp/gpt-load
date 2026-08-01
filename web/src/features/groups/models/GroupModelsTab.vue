<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { Plus, RefreshCw, Trash2 } from '@lucide/vue'
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
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
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
const manualID = ref('')
let nextKey = 1
let controller: AbortController | undefined

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
const dirty = computed(() => !sameModels(saved.value, draft.value))
const empty = computed(() => normalizedModels(draft.value).length === 0)
const canSave = computed(
  () =>
    dirty.value &&
    pending.value === null &&
    conflicts.value.length === 0 &&
    emptyAliasIndexes.value.size === 0,
)
const unpriced = computed(
  () => draft.value.filter((item) => item.pricing_status === 'unpriced').length,
)
useUnsavedChanges(dirty, { blocked: computed(() => pending.value !== null) })

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
  draft.value = draft.value.map((item, current) =>
    current === index
      ? {
          ...item,
          ...patch,
          alias: patch.alias_enabled === false ? '' : (patch.alias ?? item.alias),
        }
      : { ...item },
  )
}

function removeRow(index: number): void {
  serverConflicts.value = []
  draft.value = draft.value.filter((_, current) => current !== index)
}

function addManual(): void {
  const id = manualID.value.trim()
  if (!id || draft.value.some((item) => item.id.trim() === id)) return
  serverConflicts.value = []
  draft.value = [
    ...draft.value,
    { id, alias: '', alias_enabled: false, pricing_status: 'unpriced', key: nextKey++ },
  ]
  manualID.value = ''
}

function requestDiscovery(): void {
  if (pending.value) return
  drawerOpen.value = true
  candidates.value = []
  selectedCandidates.value = []
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
    selectedCandidates.value = candidates.value.filter(
      (candidate) => !draft.value.some((item) => item.id.trim() === candidate),
    )
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
    const next = createModelDraft(result.items).map((item) => ({ ...item, key: nextKey++ }))
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

function conflictMessage(index: number): string {
  const conflict = conflicts.value.find((item) => item.indexes.includes(index))
  return conflict ? t('group.modelEditor.nameConflict', { name: conflict.client_model }) : ''
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
      <InlineFeedback v-if="conflicts.length" tone="danger">{{
        t('group.modelEditor.conflictSummary')
      }}</InlineFeedback>

      <div class="group-models__summary" aria-live="polite">
        <span>{{ t('group.modelEditor.total', { count: draft.length }) }}</span>
        <span v-if="unpriced">{{ t('group.modelEditor.unpriced', { count: unpriced }) }}</span>
      </div>
      <LedgerRecordList
        :label="t('group.modelEditor.tableLabel')"
        :row-count="draft.length + 1"
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
          v-for="(item, index) in draft"
          :key="item.key"
          class="ledger-record-list__record group-model-record"
          :class="{ 'group-model-record--conflict': conflictIndexes.has(index) }"
          role="row"
          :aria-rowindex="index + 2"
        >
          <div class="ledger-record-list__cell group-model-record__id" role="cell">
            <span class="group-model-record__mobile-label">{{ t('group.modelEditor.id') }}</span>
            <code>{{ item.id }}</code>
          </div>

          <div class="ledger-record-list__cell group-model-record__alias-cell" role="cell">
            <span class="group-model-record__mobile-label">{{ t('group.modelEditor.alias') }}</span>
            <div class="group-models__alias">
              <label class="group-models__alias-toggle">
                <span class="sr-only">
                  {{ t('group.modelEditor.aliasEnabledFor', { id: item.id }) }}
                </span>
                <input
                  type="checkbox"
                  :checked="item.alias_enabled"
                  :disabled="pending !== null"
                  @change="
                    updateRow(index, {
                      alias_enabled: ($event.target as HTMLInputElement).checked,
                    })
                  "
                />
              </label>
              <input
                type="text"
                :value="item.alias"
                :disabled="pending !== null || !item.alias_enabled"
                :placeholder="t('group.modelEditor.aliasPlaceholder')"
                :aria-invalid="conflictIndexes.has(index) || undefined"
                @input="updateRow(index, { alias: ($event.target as HTMLInputElement).value })"
              />
            </div>
            <small v-if="conflictIndexes.has(index)" class="group-models__error">
              {{ conflictMessage(index) }}
            </small>
            <small v-else-if="emptyAliasIndexes.has(index)" class="group-models__error">
              {{ t('group.modelEditor.aliasRequired') }}
            </small>
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
              <Trash2 :size="16" aria-hidden="true" />
            </IconButton>
          </div>
        </article>
      </LedgerRecordList>
      <div class="group-models__add">
        <input
          v-model="manualID"
          :disabled="pending !== null"
          :placeholder="t('group.modelEditor.manualId')"
          @keydown.enter.prevent="addManual"
        /><AppButton
          variant="secondary"
          :disabled="pending !== null || !manualID.trim()"
          @click="addManual"
          ><Plus :size="16" aria-hidden="true" />{{ t('group.modelEditor.add') }}</AppButton
        >
      </div>

      <AppDrawer
        :open="drawerOpen"
        :title="t('group.modelEditor.drawer.title')"
        :description="t('group.modelEditor.drawer.description')"
        :close-label="t('group.modelEditor.drawer.close')"
        :dismissible="pending !== 'discover'"
        @update:open="drawerOpen = $event"
      >
        <QueryFeedback
          v-if="pending === 'discover'"
          state="loading"
          :message="t('group.modelEditor.drawer.loading')"
        />
        <InlineFeedback v-else-if="error" tone="danger">{{ error }}</InlineFeedback>
        <template v-else
          ><p>{{ t('group.modelEditor.drawer.notice') }}</p>
          <label v-for="candidate in candidates" :key="candidate" class="group-models__candidate"
            ><input v-model="selectedCandidates" type="checkbox" :value="candidate" />
            <code>{{ candidate }}</code></label
          ><InlineFeedback v-if="!candidates.length" tone="warning">{{
            t('group.modelEditor.drawer.empty')
          }}</InlineFeedback>
          <div class="group-models__drawer-actions">
            <AppButton variant="secondary" @click="drawerOpen = false">{{
              t('common.cancel')
            }}</AppButton
            ><AppButton :disabled="!selectedCandidates.length" @click="confirmCandidates">{{
              t('group.modelEditor.drawer.confirm')
            }}</AppButton>
          </div></template
        >
      </AppDrawer>

      <AppDialog
        :open="emptyConfirmOpen"
        :title="t('group.modelEditor.emptyConfirm.title')"
        :description="t('group.modelEditor.emptyConfirm.description')"
        :close-label="t('group.modelEditor.emptyConfirm.close')"
        :dismissible="pending === null"
        @update:open="emptyConfirmOpen = $event"
        ><div class="group-models__drawer-actions">
          <AppButton variant="secondary" @click="emptyConfirmOpen = false">{{
            t('common.cancel')
          }}</AppButton
          ><AppButton variant="danger" :busy="pending === 'save'" @click="save">{{
            t('group.modelEditor.emptyConfirm.confirm')
          }}</AppButton>
        </div></AppDialog
      >

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
.group-models__header,
.group-models__add,
.group-models__drawer-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}
.group-models__header h2,
.group-models__header p {
  margin: 0;
}
.group-models__header p,
.group-models__drawer-actions + p {
  color: var(--color-text-muted);
}
.group-models__summary {
  display: flex;
  gap: var(--space-3);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.group-model-record-grid {
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
.group-model-record__id code {
  overflow-wrap: anywhere;
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
.group-models__alias-toggle {
  display: grid;
  width: var(--touch-target);
  height: var(--touch-target);
  flex: 0 0 var(--touch-target);
  place-items: center;
  cursor: pointer;
}
.group-models__alias input[type='text'],
.group-models__add input {
  width: 100%;
  min-height: var(--control-md);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: 0 var(--space-3);
  font: inherit;
}
.group-models__error {
  color: var(--color-danger);
}
.group-models__pricing--priced {
  color: var(--color-success);
}
.group-models__pricing--unpriced {
  color: var(--color-warning);
}
.group-model-record__actions {
  display: flex;
  justify-content: flex-end;
}
.group-model-record__mobile-label {
  display: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}
.group-models__add {
  justify-content: flex-start;
}
.group-models__add input {
  max-width: 360px;
  font-family: var(--font-mono);
}
.group-models__candidate {
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
}
.group-models__drawer-actions {
  margin-top: var(--space-4);
  justify-content: flex-end;
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
}
@media (max-width: 640px) {
  .group-models__header,
  .group-models__add {
    align-items: stretch;
    flex-direction: column;
  }
  .group-models__add input {
    max-width: none;
  }
}
</style>
