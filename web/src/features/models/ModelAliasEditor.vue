<script setup lang="ts" generic="T extends ModelDraftValue">
import { Plus, Search, X } from '@lucide/vue'
import { computed, nextTick, ref, useId } from 'vue'

import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

import {
  modelDraftValidity,
  type ModelAliasEditorLabels,
  type ModelDraftValue,
  type ModelNameConflict,
} from './model-draft'

const props = withDefaults(
  defineProps<{
    modelValue: T[]
    conflicts: readonly ModelNameConflict[]
    labels: ModelAliasEditorLabels
    createRow?: () => T
    disabled?: boolean
    searchable?: boolean
    addable?: boolean
  }>(),
  {
    createRow: undefined,
    disabled: false,
    searchable: true,
    addable: true,
  },
)
const emit = defineEmits<{ 'update:modelValue': [value: T[]] }>()

const instanceId = useId()
const root = ref<HTMLElement>()
const search = ref('')
const validity = computed(() => modelDraftValidity(props.modelValue, props.conflicts))
const validationSummary = computed(() =>
  [
    props.conflicts.length ? props.labels.conflictSummary : '',
    validity.value.emptyIDIndexes.size ? props.labels.manualIdRequired : '',
    validity.value.emptyAliasIndexes.size ? props.labels.emptyAliasSummary : '',
  ]
    .filter(Boolean)
    .join(' · '),
)
const visibleRows = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return props.modelValue.flatMap((item, index) =>
    !query || `${item.id} ${item.alias}`.toLocaleLowerCase().includes(query)
      ? [{ item, index }]
      : [],
  )
})

function updateRow(
  index: number,
  patch: Partial<Pick<ModelDraftValue, 'id' | 'alias' | 'alias_enabled'>>,
): void {
  emit(
    'update:modelValue',
    props.modelValue.map((item, current) =>
      current === index
        ? ({
            ...item,
            ...patch,
            alias: patch.alias_enabled === false ? '' : (patch.alias ?? item.alias),
          } as T)
        : ({ ...item } as T),
    ),
  )
}

function removeRow(index: number): void {
  emit(
    'update:modelValue',
    props.modelValue.filter((_, current) => current !== index).map((item) => ({ ...item })),
  )
}

async function addManual(): Promise<void> {
  if (props.disabled || !props.createRow) return
  search.value = ''
  const index = props.modelValue.length
  emit('update:modelValue', [...props.modelValue.map((item) => ({ ...item })), props.createRow()])
  await nextTick()
  root.value?.querySelector<HTMLInputElement>(`[data-model-id-index="${index}"]`)?.focus()
}

function conflictMessage(index: number): string {
  const conflict = props.conflicts.find((item) => item.indexes.includes(index))
  return conflict ? props.labels.nameConflict(conflict.client_model) : ''
}

function modelIDError(item: ModelDraftValue, index: number): string {
  if (validity.value.emptyIDIndexes.has(index)) return props.labels.manualIdRequired
  if (!item.alias_enabled && validity.value.conflictIndexes.has(index)) {
    return conflictMessage(index)
  }
  return ''
}

function modelAliasError(item: ModelDraftValue, index: number): string {
  if (!item.alias_enabled) return ''
  if (validity.value.emptyAliasIndexes.has(index)) return props.labels.aliasRequired
  return validity.value.conflictIndexes.has(index) ? conflictMessage(index) : ''
}

async function setAliasEnabled(index: number, enabled: boolean): Promise<void> {
  updateRow(index, { alias_enabled: enabled })
  if (!enabled) return

  await nextTick()
  root.value?.querySelector<HTMLInputElement>(`[data-alias-input-index="${index}"]`)?.focus()
}

async function focusFirstInvalid(): Promise<void> {
  const index = Math.min(...validity.value.invalidIndexes)
  if (!Number.isFinite(index)) return
  search.value = ''
  await nextTick()
  const selector = validity.value.emptyIDIndexes.has(index)
    ? `[data-model-id-index="${index}"]`
    : props.modelValue[index]?.alias_enabled
      ? `[data-alias-input-index="${index}"]`
      : `[data-alias-toggle-index="${index}"]`
  root.value?.querySelector<HTMLInputElement>(selector)?.focus()
}

defineExpose({ addManual })
</script>

<template>
  <div ref="root" class="model-alias-editor">
    <div v-if="validity.invalidIndexes.size" class="model-alias-editor__validation">
      <InlineFeedback tone="danger">{{ validationSummary }}</InlineFeedback>
      <AppButton variant="link" size="inline" :disabled="disabled" @click="focusFirstInvalid">
        {{ labels.locateFirstInvalid }}
      </AppButton>
    </div>

    <div v-if="searchable" class="model-alias-editor__toolbar">
      <label class="model-alias-editor__search">
        <span>{{ labels.searchLabel }}</span>
        <AppTextInput
          v-model="search"
          type="search"
          appearance="surface"
          size="compact"
          :label="labels.search"
          :placeholder="labels.search"
          :clear-label="labels.clearSearch"
          :disabled="disabled"
        >
          <template #leading><Search :size="15" /></template>
        </AppTextInput>
      </label>
      <span class="model-alias-editor__count" aria-live="polite">
        {{ labels.count(modelValue.length) }}
      </span>
    </div>

    <LedgerRecordList
      :label="labels.tableLabel"
      :row-count="(visibleRows.length || 1) + 1"
      grid-class="model-alias-editor__grid"
    >
      <template #header>
        <span role="columnheader">{{ labels.id }}</span>
        <span role="columnheader">{{ labels.alias }}</span>
        <span role="columnheader">{{ labels.thirdColumn }}</span>
        <span role="columnheader"
          ><span class="sr-only">{{ labels.actions }}</span></span
        >
      </template>

      <article
        v-for="({ item, index }, visibleIndex) in visibleRows"
        :key="item.key"
        class="ledger-record-list__record model-alias-editor__record"
        :class="{ 'model-alias-editor__record--invalid': validity.invalidIndexes.has(index) }"
        role="row"
        :aria-rowindex="visibleIndex + 2"
      >
        <div class="ledger-record-list__cell model-alias-editor__id" role="cell">
          <span class="model-alias-editor__mobile-label">{{ labels.id }}</span>
          <CompactFieldError
            :id="`${instanceId}-model-id-${index}`"
            class="model-alias-editor__id-field"
            :error="modelIDError(item, index)"
          >
            <template #default="{ invalid, describedBy }">
              <AppTextInput
                v-if="item.editable_id"
                :id="`${instanceId}-model-id-${index}`"
                :model-value="item.id"
                appearance="surface"
                size="sm"
                monospace
                :label="labels.id"
                :placeholder="labels.manualId"
                :invalid="invalid"
                :described-by="describedBy"
                :data-model-id-index="index"
                :spellcheck="false"
                :disabled="disabled"
                @update:model-value="updateRow(index, { id: $event })"
              />
              <code v-else :aria-describedby="describedBy">{{ item.id }}</code>
            </template>
          </CompactFieldError>
        </div>

        <div class="ledger-record-list__cell model-alias-editor__alias-cell" role="cell">
          <span class="model-alias-editor__mobile-label">{{ labels.alias }}</span>
          <div class="model-alias-editor__alias-control">
            <label
              class="model-alias-editor__alias-toggle"
              :class="{ 'model-alias-editor__alias-toggle--disabled': disabled }"
            >
              <span class="sr-only">{{ labels.aliasEnabledFor(item.id) }}</span>
              <input
                :data-alias-toggle-index="index"
                type="checkbox"
                :checked="item.alias_enabled"
                :disabled="disabled"
                @change="setAliasEnabled(index, ($event.target as HTMLInputElement).checked)"
              />
            </label>
            <CompactFieldError
              v-if="item.alias_enabled"
              :id="`${instanceId}-model-alias-${index}`"
              class="model-alias-editor__alias-field"
              :error="modelAliasError(item, index)"
            >
              <template #default="{ invalid, describedBy }">
                <AppTextInput
                  :id="`${instanceId}-model-alias-${index}`"
                  :model-value="item.alias"
                  appearance="surface"
                  size="sm"
                  :label="labels.aliasFor(item.id)"
                  :disabled="disabled"
                  :placeholder="labels.aliasPlaceholder"
                  :invalid="invalid"
                  :described-by="describedBy"
                  :data-alias-input-index="index"
                  :spellcheck="false"
                  @update:model-value="updateRow(index, { alias: $event })"
                />
              </template>
            </CompactFieldError>
          </div>
        </div>

        <div class="ledger-record-list__cell model-alias-editor__third-column" role="cell">
          <span class="model-alias-editor__mobile-label">{{ labels.thirdColumn }}</span>
          <slot name="third-column" :item="item" :index="index" />
        </div>

        <div class="ledger-record-list__cell model-alias-editor__actions" role="cell">
          <IconButton
            variant="ghost"
            size="compact"
            :disabled="disabled"
            :label="labels.removeFor(item.id || labels.manualId)"
            @click="removeRow(index)"
          >
            <X :size="16" aria-hidden="true" />
          </IconButton>
        </div>
      </article>

      <div
        v-if="visibleRows.length === 0"
        class="ledger-record-list__record model-alias-editor__empty"
        role="row"
        aria-rowindex="2"
      >
        <span class="ledger-record-list__cell" role="cell">
          {{ modelValue.length ? labels.noMatches : labels.empty }}
        </span>
      </div>
    </LedgerRecordList>

    <AppButton
      v-if="addable"
      class="model-alias-editor__add"
      variant="link"
      size="inline"
      :disabled="disabled || !createRow"
      @click="addManual"
    >
      <Plus :size="16" aria-hidden="true" />{{ labels.addInline }}
    </AppButton>
  </div>
</template>

<style scoped>
.model-alias-editor {
  min-width: 0;
}

.model-alias-editor__validation {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.model-alias-editor__toolbar {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-3);
  padding: 15px 0 13px;
}

.model-alias-editor__search {
  display: grid;
  width: min(100%, 420px);
  min-width: 0;
  gap: 5px;
  color: var(--color-text-faint);
  font-size: var(--text-meta);
}

.model-alias-editor__count {
  flex: none;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: 10.8px;
}

.model-alias-editor__grid {
  --ledger-record-list-record-min-height: 58px;
  --ledger-record-list-record-padding: 9px 0;
  --ledger-record-list-grid: minmax(180px, 24fr) minmax(280px, 52fr) minmax(120px, 18fr) 40px;
  --ledger-record-list-column-gap: 16px;
}

.model-alias-editor__grid :deep(.ledger-record-list__header) {
  font-size: var(--text-label-xs);
  font-weight: 500;
}

.model-alias-editor__record--invalid {
  background: var(--color-danger-bg);
}

.model-alias-editor__id,
.model-alias-editor__third-column {
  min-width: 0;
}

.model-alias-editor__id {
  display: grid;
  align-content: center;
  gap: 5px;
}

.model-alias-editor__id code {
  display: block;
  width: 100%;
  padding-inline-end: 38px;
  overflow-wrap: anywhere;
  font-size: var(--text-sm);
}

.model-alias-editor__id-field {
  display: flex;
  width: 100%;
  min-height: var(--control-sm);
  max-width: 300px;
  align-items: center;
}

.model-alias-editor__id-field :deep(.app-text-input) {
  width: 100%;
}

.model-alias-editor__alias-cell {
  display: grid;
  gap: var(--space-1);
}

.model-alias-editor__alias-control {
  display: flex;
  min-height: var(--control-sm);
  align-items: center;
  gap: 9px;
}

.model-alias-editor__alias-toggle {
  display: grid;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  place-items: center;
  cursor: pointer;
}

.model-alias-editor__alias-toggle input {
  width: 16px;
  height: 16px;
  margin: 0;
  accent-color: var(--color-action);
  cursor: pointer;
}

.model-alias-editor__alias-toggle--disabled,
.model-alias-editor__alias-toggle input:disabled {
  cursor: not-allowed;
}

.model-alias-editor__alias-toggle input:disabled {
  opacity: 0.55;
}

.model-alias-editor__alias-field {
  width: min(100%, 300px);
  min-width: 0;
  flex: 1;
}

.model-alias-editor__actions {
  display: flex;
  justify-content: flex-end;
}

.model-alias-editor__actions :deep(.icon-button:hover:not(:disabled)) {
  border-color: var(--color-danger);
  color: var(--color-danger);
}

.model-alias-editor__mobile-label {
  display: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}

.model-alias-editor__empty {
  grid-template-columns: minmax(0, 1fr);
  min-height: 58px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  text-align: center;
}

.model-alias-editor__empty .ledger-record-list__cell {
  grid-column: 1 / -1;
}

.model-alias-editor__add {
  width: fit-content;
  min-height: 36px;
  justify-content: flex-start;
  margin-top: 10px;
  padding: 4px 1px;
  font-size: var(--text-sm);
  font-weight: 600;
}

@media (max-width: 860px) {
  .model-alias-editor__grid {
    --ledger-record-list-card-grid: minmax(0, 0.7fr) minmax(0, 1.3fr);
  }

  .model-alias-editor__record {
    padding-right: 58px;
  }

  .model-alias-editor__id,
  .model-alias-editor__alias-cell {
    grid-column: 1 / -1;
  }

  .model-alias-editor__id,
  .model-alias-editor__alias-cell,
  .model-alias-editor__third-column {
    display: grid;
    align-content: start;
    gap: 5px;
  }

  .model-alias-editor__alias-cell {
    border-top: 1px solid var(--color-border-subtle);
    padding-top: 11px;
  }

  .model-alias-editor__actions {
    position: absolute;
    top: 4px;
    right: 4px;
  }

  .model-alias-editor__actions :deep(.icon-button) {
    width: var(--touch-target);
    height: var(--touch-target);
  }

  .model-alias-editor__mobile-label {
    display: inline;
  }

  .model-alias-editor__alias-toggle {
    width: var(--touch-target);
    height: var(--touch-target);
    flex-basis: var(--touch-target);
  }

  .model-alias-editor__id-field,
  .model-alias-editor__alias-field {
    max-width: none;
  }

  .model-alias-editor__id-field {
    min-height: var(--touch-target);
  }
}

@media (max-width: 640px) {
  .model-alias-editor__validation,
  .model-alias-editor__toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .model-alias-editor__search {
    width: 100%;
  }
}
</style>
