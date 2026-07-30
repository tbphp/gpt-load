<script setup lang="ts">
import { Plus } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

export interface ModelDraftEditorItem {
  id: string
  alias: string
  selected: boolean
}

const props = defineProps<{ modelValue: ModelDraftEditorItem[]; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: ModelDraftEditorItem[]] }>()
const { t } = useI18n()
const manualID = ref('')
const manualAlias = ref('')

function update(index: number, patch: Partial<ModelDraftEditorItem>): void {
  emit(
    'update:modelValue',
    props.modelValue.map((model, current) =>
      current === index ? { ...model, ...patch } : { ...model },
    ),
  )
}

function addManual(): void {
  const id = manualID.value.trim()
  const alias = manualAlias.value.trim()
  if (!id) return
  const exactIndex = props.modelValue.findIndex((model) => model.id === id && model.alias === alias)
  const next =
    exactIndex >= 0
      ? props.modelValue.map((model, index) =>
          index === exactIndex ? { ...model, selected: true } : { ...model },
        )
      : [...props.modelValue.map((model) => ({ ...model })), { id, alias, selected: true }]
  emit('update:modelValue', next)
  manualID.value = ''
  manualAlias.value = ''
}
</script>

<template>
  <section class="model-editor">
    <div>
      <h3>{{ t('import.models.title') }}</h3>
      <p>{{ t('import.models.description') }}</p>
    </div>
    <div v-if="modelValue.length" class="model-list">
      <div
        v-for="(model, index) in modelValue"
        :key="index"
        class="model-row"
        :class="{ 'model-row--with-status': $slots.status }"
        :data-test="`model-row-${index}`"
      >
        <label class="model-row__check">
          <input
            :data-test="`model-selected-${index}`"
            type="checkbox"
            :checked="model.selected"
            :disabled="props.disabled"
            @change="update(index, { selected: ($event.target as HTMLInputElement).checked })"
          />
          <code>{{ model.id }}</code>
        </label>
        <label>
          <span class="sr-only">{{ t('import.models.aliasFor', { id: model.id }) }}</span>
          <input
            :data-test="`model-alias-${index}`"
            :value="model.alias"
            :placeholder="t('import.models.alias')"
            :disabled="props.disabled"
            @input="update(index, { alias: ($event.target as HTMLInputElement).value })"
          />
        </label>
        <slot name="status" :model="model" :index="index" />
      </div>
    </div>
    <div class="manual-model">
      <label>
        <span>{{ t('import.models.manualId') }}</span>
        <input
          v-model="manualID"
          data-test="manual-model-id"
          autocomplete="off"
          spellcheck="false"
          :disabled="props.disabled"
        />
      </label>
      <label>
        <span>{{ t('import.models.alias') }}</span>
        <input
          v-model="manualAlias"
          data-test="manual-model-alias"
          autocomplete="off"
          :disabled="props.disabled"
        />
      </label>
      <button
        data-test="add-manual-model"
        type="button"
        :disabled="props.disabled || !manualID.trim()"
        @click="addManual"
      >
        <Plus :size="16" aria-hidden="true" />{{ t('import.models.add') }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.model-editor {
  display: grid;
  gap: var(--space-4);
}
h3,
p {
  margin: 0;
}
h3 {
  font-size: 1rem;
}
p {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}
.model-list {
  display: grid;
  gap: var(--space-2);
}
.model-row {
  display: grid;
  grid-template-columns: minmax(160px, 1fr) minmax(160px, 0.8fr);
  align-items: center;
  gap: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
  padding-bottom: var(--space-2);
}
.model-row--with-status {
  grid-template-columns: minmax(160px, 1fr) minmax(160px, 0.8fr) auto;
}
.model-row__check {
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-3);
}
code {
  overflow-wrap: anywhere;
}
.model-row input:not([type='checkbox']),
.manual-model input {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
}
.manual-model {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  align-items: end;
  gap: var(--space-3);
}
.manual-model label {
  display: grid;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-size: 0.75rem;
}
.manual-model button {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-action);
  padding: var(--space-2) var(--space-3);
  font-weight: 650;
  cursor: pointer;
}
.manual-model button:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
@media (max-width: 759px) {
  .model-row,
  .model-row--with-status,
  .manual-model {
    grid-template-columns: 1fr;
  }
}
</style>
