<script setup lang="ts">
import { RadioGroupItem, RadioGroupRoot } from 'reka-ui'
import AppOverflowText from './AppOverflowText.vue'
import type { ControlSize, SelectOption } from './types'

type SegmentedSize = 'xxs' | ControlSize

withDefaults(
  defineProps<{
    label: string
    modelValue: string
    options: readonly (SelectOption & { count?: string | number })[]
    disabled?: boolean
    appearance?: 'filter' | 'field'
    size?: SegmentedSize
  }>(),
  { appearance: 'filter', size: 'md' },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
function select(value: unknown): void {
  if (typeof value === 'string') emit('update:modelValue', value)
}
</script>

<template>
  <RadioGroupRoot
    class="modern-segmented"
    :class="[`modern-segmented--${appearance}`, `modern-segmented--${size}`]"
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
      <AppOverflowText v-if="appearance === 'field'" :text="option.label" />
      <template v-else>{{ option.label }}</template>
      <span v-if="option.count !== undefined" class="modern-segmented-count">{{
        option.count
      }}</span>
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
  background: var(--modern-surface);
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
.modern-segmented-option:hover:not(:disabled, [data-state='checked']) {
  background: var(--modern-control-hover);
  color: var(--modern-text);
}
.modern-segmented-option[data-state='checked'] {
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
  font-weight: var(--modern-weight-semibold);
  box-shadow: inset 0 0 0 var(--modern-line-width) var(--modern-segmented-active-border);
}
.modern-segmented-option[data-state='checked']:hover:not(:disabled) {
  background: var(--modern-segmented-active-hover);
  color: var(--modern-accent);
  box-shadow: inset 0 0 0 var(--modern-line-width) var(--modern-accent);
}
.modern-segmented-count {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
.modern-segmented-option[data-state='checked'] .modern-segmented-count {
  color: var(--modern-accent);
}
.modern-segmented-option:disabled {
  cursor: not-allowed;
  opacity: var(--modern-opacity-disabled);
}
.modern-segmented--xxs {
  padding: var(--modern-space-0-5);
}
.modern-segmented--xxs .modern-segmented-option {
  min-height: var(--modern-control-xxs);
  padding: 0 var(--modern-space-2);
  font-size: var(--modern-font-size-caption);
}
.modern-segmented--field {
  --modern-segmented-size: var(--modern-control-md);
  width: fit-content;
  min-width: 0;
  flex-wrap: nowrap;
  border: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-0-5);
}
.modern-segmented--field.modern-segmented--sm {
  --modern-segmented-size: var(--modern-control-sm);
}
.modern-segmented--field.modern-segmented--xs {
  --modern-segmented-size: var(--modern-control-xs);
}
.modern-segmented--field.modern-segmented--xxs {
  --modern-segmented-size: var(--modern-control-xxs);
}
.modern-segmented--field.modern-segmented--xs .modern-segmented-option {
  font-size: var(--modern-font-size-small);
  padding-inline: var(--modern-space-1-5);
}
.modern-segmented--field.modern-segmented--xxs .modern-segmented-option {
  font-size: var(--modern-font-size-caption);
  padding-inline: var(--modern-space-1);
}
.modern-segmented--field .modern-segmented-option {
  flex: 0 1 auto;
  min-width: 0;
  min-height: calc(
    var(--modern-segmented-size) - 2 * (var(--modern-space-0-5) + var(--modern-line-width))
  );
  padding: var(--modern-space-0-5) var(--modern-space-2);
}
.modern-segmented--field[aria-invalid='true'] {
  border-color: var(--modern-danger);
}
@media (max-width: 760px) {
  .modern-segmented {
    padding: 0;
  }
  .modern-segmented-option {
    min-height: var(--modern-touch-target);
  }
  .modern-segmented--field {
    --modern-segmented-size: var(--modern-touch-target);
    padding: var(--modern-space-0-5);
  }
  .modern-segmented--field.modern-segmented--sm,
  .modern-segmented--field.modern-segmented--xs,
  .modern-segmented--field.modern-segmented--xxs {
    --modern-segmented-size: var(--modern-touch-target);
  }
}
</style>
