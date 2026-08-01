<script setup lang="ts" generic="T extends ModelDraftValue">
import { Plus, Search, Trash2 } from '@lucide/vue'
import { computed, nextTick, ref, useId } from 'vue'

import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
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
    createRow?: (id: string) => T
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
const manualID = ref('')
const validity = computed(() => modelDraftValidity(props.modelValue, props.conflicts))
const validationSummary = computed(() =>
  [
    props.conflicts.length ? props.labels.conflictSummary : '',
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
  patch: Partial<Pick<ModelDraftValue, 'alias' | 'alias_enabled'>>,
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

function addManual(): void {
  const id = manualID.value.trim()
  if (!id || !props.createRow) return
  emit('update:modelValue', [...props.modelValue.map((item) => ({ ...item })), props.createRow(id)])
  manualID.value = ''
}

function conflictMessage(index: number): string {
  const conflict = props.conflicts.find((item) => item.indexes.includes(index))
  return conflict ? props.labels.nameConflict(conflict.client_model) : ''
}

async function focusFirstInvalid(): Promise<void> {
  const index = Math.min(...validity.value.invalidIndexes)
  if (!Number.isFinite(index)) return
  search.value = ''
  await nextTick()
  const selector = props.modelValue[index]?.alias_enabled
    ? `[data-alias-input-index="${index}"]`
    : `[data-alias-toggle-index="${index}"]`
  root.value?.querySelector<HTMLInputElement>(selector)?.focus()
}
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
      <AppTextInput
        v-model="search"
        type="search"
        appearance="sunken"
        size="touch"
        :label="labels.search"
        :placeholder="labels.search"
        :clear-label="labels.clearSearch"
        :disabled="disabled"
      >
        <template #leading><Search :size="16" /></template>
      </AppTextInput>
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
          <code>{{ item.id }}</code>
        </div>

        <div class="ledger-record-list__cell model-alias-editor__alias-cell" role="cell">
          <span class="model-alias-editor__mobile-label">{{ labels.alias }}</span>
          <div class="model-alias-editor__alias-control">
            <label class="model-alias-editor__alias-toggle">
              <span class="sr-only">{{ labels.aliasEnabledFor(item.id) }}</span>
              <input
                :data-alias-toggle-index="index"
                type="checkbox"
                :checked="item.alias_enabled"
                :disabled="disabled"
                :aria-invalid="validity.invalidIndexes.has(index) || undefined"
                :aria-describedby="
                  validity.invalidIndexes.has(index) ? `${instanceId}-error-${index}` : undefined
                "
                @change="
                  updateRow(index, {
                    alias_enabled: ($event.target as HTMLInputElement).checked,
                  })
                "
              />
            </label>
            <AppTextInput
              :model-value="item.alias"
              appearance="sunken"
              :label="labels.aliasFor(item.id)"
              :disabled="disabled || !item.alias_enabled"
              :placeholder="labels.aliasPlaceholder"
              :invalid="validity.invalidIndexes.has(index)"
              :described-by="
                validity.invalidIndexes.has(index) ? `${instanceId}-error-${index}` : undefined
              "
              :data-alias-input-index="index"
              :spellcheck="false"
              @update:model-value="updateRow(index, { alias: $event })"
            />
          </div>
          <small
            v-if="validity.conflictIndexes.has(index)"
            :id="`${instanceId}-error-${index}`"
            class="model-alias-editor__error"
          >
            {{ conflictMessage(index) }}
          </small>
          <small
            v-else-if="validity.emptyAliasIndexes.has(index)"
            :id="`${instanceId}-error-${index}`"
            class="model-alias-editor__error"
          >
            {{ labels.aliasRequired }}
          </small>
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
            :label="labels.removeFor(item.id)"
            @click="removeRow(index)"
          >
            <Trash2 :size="16" aria-hidden="true" />
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

    <form v-if="addable" class="model-alias-editor__add" @submit.prevent="addManual">
      <div class="model-alias-editor__manual-input">
        <AppTextInput
          :id="`${instanceId}-manual-id`"
          v-model="manualID"
          appearance="sunken"
          monospace
          :label="labels.manualId"
          :placeholder="labels.manualId"
          :spellcheck="false"
          :disabled="disabled"
        />
      </div>
      <AppButton
        type="submit"
        variant="secondary"
        :disabled="disabled || !createRow || !manualID.trim()"
      >
        <Plus :size="16" aria-hidden="true" />{{ labels.add }}
      </AppButton>
    </form>
  </div>
</template>

<style scoped>
.model-alias-editor {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.model-alias-editor__toolbar {
  display: flex;
  width: min(100%, 420px);
  align-items: center;
}

.model-alias-editor__validation {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.model-alias-editor__grid {
  --ledger-record-list-grid: minmax(180px, 1.05fr) minmax(280px, 1.6fr) 140px 48px;
  --ledger-record-list-column-gap: 16px;
}

.model-alias-editor__record--invalid {
  background: var(--color-danger-bg);
}

.model-alias-editor__id,
.model-alias-editor__third-column {
  min-width: 0;
}

.model-alias-editor__id code {
  overflow-wrap: anywhere;
}

.model-alias-editor__alias-cell {
  display: grid;
  gap: var(--space-1);
}

.model-alias-editor__alias-control {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.model-alias-editor__alias-toggle {
  display: grid;
  width: var(--touch-target);
  height: var(--touch-target);
  flex: 0 0 var(--touch-target);
  place-items: center;
  cursor: pointer;
}

.model-alias-editor__alias-toggle:has(input:disabled) {
  cursor: not-allowed;
}

.model-alias-editor__alias-control :deep(.app-text-input) {
  flex: 1;
}

.model-alias-editor__error {
  color: var(--color-danger);
}

.model-alias-editor__actions {
  display: flex;
  justify-content: flex-end;
}

.model-alias-editor__mobile-label {
  display: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 560;
}

.model-alias-editor__empty {
  grid-template-columns: minmax(0, 1fr);
  min-height: 68px;
  color: var(--color-text-muted);
  text-align: center;
}

.model-alias-editor__empty .ledger-record-list__cell {
  grid-column: 1 / -1;
}

.model-alias-editor__add {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: var(--space-3);
}

.model-alias-editor__manual-input {
  width: 100%;
  max-width: 360px;
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
}

@media (max-width: 640px) {
  .model-alias-editor__validation,
  .model-alias-editor__add {
    align-items: stretch;
    flex-direction: column;
  }

  .model-alias-editor__manual-input {
    max-width: none;
  }

  .model-alias-editor__add :deep(.app-button) {
    min-height: var(--touch-target);
  }
}
</style>
