<script setup lang="ts">
import { RadioGroupItem, RadioGroupRoot } from 'reka-ui'
import type { SelectOption } from './types'

defineProps<{
  label: string
  modelValue: string
  options: readonly (SelectOption & { count?: string | number })[]
  disabled?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
function select(value: unknown): void {
  if (typeof value === 'string') emit('update:modelValue', value)
}
</script>

<template>
  <RadioGroupRoot
    class="modern-segmented"
    :aria-label="label"
    :model-value="modelValue"
    :disabled="disabled"
    orientation="horizontal"
    @update:model-value="select"
  >
    <RadioGroupItem
      v-for="option in options"
      :key="option.value"
      class="modern-segmented-option"
      :value="option.value"
      :disabled="option.disabled"
    >
      {{ option.label }}
      <span v-if="option.count !== undefined">{{ option.count }}</span>
    </RadioGroupItem>
  </RadioGroupRoot>
</template>

<style scoped>
.modern-segmented {
  display: inline-flex;
  width: fit-content;
  max-width: 100%;
  flex-wrap: wrap;
  gap: var(--modern-space-0-5);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  padding: var(--modern-space-1);
}
.modern-segmented-option {
  display: inline-flex;
  min-height: var(--modern-control-xs);
  align-items: center;
  justify-content: center;
  gap: var(--modern-space-2);
  border: 0;
  border-radius: var(--modern-radius-small);
  background: transparent;
  padding: var(--modern-space-1) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  line-height: var(--modern-leading-compact);
  white-space: nowrap;
}
.modern-segmented-option:hover:not(:disabled) {
  background: var(--modern-control-hover);
  color: var(--modern-text);
}
.modern-segmented-option[data-state='checked'] {
  background: var(--modern-surface);
  color: var(--modern-accent);
  font-weight: var(--modern-weight-medium);
  box-shadow: var(--modern-shadow-control);
}
.modern-segmented-option span {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
.modern-segmented-option[data-state='checked'] span {
  color: var(--modern-accent);
}
@media (max-width: 760px) {
  .modern-segmented {
    padding: 0;
  }
  .modern-segmented-option {
    min-height: var(--modern-touch-target);
  }
}
</style>
