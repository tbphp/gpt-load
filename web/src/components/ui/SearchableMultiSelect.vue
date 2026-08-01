<script setup lang="ts">
import { Search, X } from '@lucide/vue'
import { computed, ref } from 'vue'

export type SearchableMultiSelectValue = string | number

export interface SearchableMultiSelectOption {
  value: SearchableMultiSelectValue
  label: string
  description?: string
  disabled?: boolean
}

const props = withDefaults(
  defineProps<{
    id: string
    label: string
    searchLabel: string
    searchPlaceholder: string
    emptyLabel: string
    loadingLabel: string
    selectedLabel: string
    clearLabel: string
    removeLabel: (label: string) => string
    options: SearchableMultiSelectOption[]
    modelValue: SearchableMultiSelectValue[]
    disabled?: boolean
    loading?: boolean
  }>(),
  { disabled: false, loading: false },
)
const emit = defineEmits<{ 'update:modelValue': [values: SearchableMultiSelectValue[]] }>()

const query = ref('')
const selected = computed(() => new Set(props.modelValue))
const normalizedQuery = computed(() => query.value.trim().toLocaleLowerCase())
const filteredOptions = computed(() => {
  if (!normalizedQuery.value) return props.options
  return props.options.filter((option) =>
    `${option.label} ${option.description ?? ''}`
      .toLocaleLowerCase()
      .includes(normalizedQuery.value),
  )
})
const selectedOptions = computed(() =>
  props.modelValue.map((value) => {
    const option = props.options.find((candidate) => candidate.value === value)
    return { value, label: option?.label ?? String(value) }
  }),
)
const selectedCountText = computed(() =>
  props.selectedLabel.replace('{count}', String(props.modelValue.length)),
)

function toggle(value: SearchableMultiSelectValue, checked: boolean): void {
  if (props.disabled || props.loading) return
  const next = checked
    ? [...new Set([...props.modelValue, value])]
    : props.modelValue.filter((current) => current !== value)
  emit('update:modelValue', next)
}

function remove(value: SearchableMultiSelectValue): void {
  if (props.disabled || props.loading) return
  emit(
    'update:modelValue',
    props.modelValue.filter((current) => current !== value),
  )
}

function clear(): void {
  if (props.disabled || props.loading) return
  emit('update:modelValue', [])
}
</script>

<template>
  <section class="searchable-multi-select" :aria-labelledby="`${id}-label`">
    <div class="searchable-multi-select__head">
      <span :id="`${id}-label`" class="searchable-multi-select__label">{{ label }}</span>
      <span class="searchable-multi-select__count" aria-live="polite">
        {{ selectedCountText }}
      </span>
    </div>

    <label class="searchable-multi-select__search" :for="`${id}-search`">
      <span class="sr-only">{{ searchLabel }}</span>
      <Search :size="16" aria-hidden="true" />
      <input
        :id="`${id}-search`"
        v-model="query"
        type="search"
        :placeholder="searchPlaceholder"
        :disabled="disabled || loading"
      />
    </label>

    <div
      v-if="modelValue.length"
      class="searchable-multi-select__chips"
      :aria-label="selectedCountText"
    >
      <span
        v-for="option in selectedOptions"
        :key="String(option.value)"
        class="searchable-multi-select__chip"
      >
        <span>{{ option.label }}</span>
        <button
          type="button"
          :aria-label="removeLabel(option.label)"
          :disabled="disabled || loading"
          @click="remove(option.value)"
        >
          <X :size="15" aria-hidden="true" />
        </button>
      </span>
      <button
        type="button"
        class="searchable-multi-select__clear"
        :disabled="disabled || loading"
        @click="clear"
      >
        {{ clearLabel }}
      </button>
    </div>

    <p v-if="loading" class="searchable-multi-select__feedback" role="status">{{ loadingLabel }}</p>
    <p v-else-if="filteredOptions.length === 0" class="searchable-multi-select__feedback">
      {{ emptyLabel }}
    </p>
    <div v-else class="searchable-multi-select__options" role="group" :aria-label="label">
      <label
        v-for="option in filteredOptions"
        :key="String(option.value)"
        class="searchable-multi-select__option"
        :class="{ 'searchable-multi-select__option--disabled': option.disabled }"
      >
        <input
          type="checkbox"
          :checked="selected.has(option.value)"
          :disabled="disabled || loading || Boolean(option.disabled)"
          @change="toggle(option.value, ($event.target as HTMLInputElement).checked)"
        />
        <span class="searchable-multi-select__option-content">
          <span>{{ option.label }}</span>
          <small v-if="option.description">{{ option.description }}</small>
        </span>
      </label>
    </div>
  </section>
</template>

<style scoped>
.searchable-multi-select {
  display: grid;
  gap: var(--space-2);
}
.searchable-multi-select__head,
.searchable-multi-select__chips,
.searchable-multi-select__search,
.searchable-multi-select__option {
  display: flex;
  align-items: center;
}
.searchable-multi-select__head {
  justify-content: space-between;
  gap: var(--space-2);
}
.searchable-multi-select__label {
  font-weight: 700;
}
.searchable-multi-select__count,
.searchable-multi-select__feedback,
.searchable-multi-select__option small {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.searchable-multi-select__search {
  min-height: var(--touch-target);
  gap: var(--space-2);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 0 var(--space-3);
  color: var(--color-text-muted);
}
.searchable-multi-select__search:focus-within {
  border-color: var(--color-action);
  box-shadow: var(--focus-ring);
}
.searchable-multi-select__search input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--color-text);
  font: inherit;
}
.searchable-multi-select__chips {
  flex-wrap: wrap;
  gap: var(--space-2);
}
.searchable-multi-select__chip {
  display: inline-flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-1);
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding-left: var(--space-2);
  font-size: var(--text-sm);
}
.searchable-multi-select__chip button,
.searchable-multi-select__clear {
  min-width: var(--touch-target);
  min-height: var(--touch-target);
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  font: inherit;
  cursor: pointer;
}
.searchable-multi-select__chip button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.searchable-multi-select__clear {
  color: var(--color-action);
  text-decoration: underline;
}
.searchable-multi-select__options {
  display: grid;
  max-height: 252px;
  overflow-y: auto;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
}
.searchable-multi-select__option {
  min-height: var(--touch-target);
  gap: var(--space-2);
  padding: 0 var(--space-3);
}
.searchable-multi-select__option + .searchable-multi-select__option {
  border-top: 1px solid var(--color-border-subtle);
}
.searchable-multi-select__option input {
  width: 20px;
  height: 20px;
  flex: none;
}
.searchable-multi-select__option-content {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
}
.searchable-multi-select__option--disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
.searchable-multi-select__feedback {
  margin: 0;
  min-height: var(--touch-target);
  display: flex;
  align-items: center;
  padding: 0 var(--space-3);
  border: 1px dashed var(--color-border-subtle);
  border-radius: var(--radius-control);
}
.searchable-multi-select button:disabled,
.searchable-multi-select input:disabled {
  cursor: not-allowed;
}
</style>
