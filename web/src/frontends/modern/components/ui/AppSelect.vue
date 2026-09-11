<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { useId } from 'vue'
import AppIcon from './AppIcon.vue'

defineOptions({ inheritAttrs: false })
defineProps<{
  label: string
  options: ReadonlyArray<{ value: string; label: string }>
  labelHidden?: boolean
  disabled?: boolean
}>()
const model = defineModel<string>({ required: true })
const id = useId()
</script>

<template>
  <div class="modern-select">
    <label :for="id" :class="{ 'modern-sr-only': labelHidden }">{{ label }}</label>
    <div class="modern-select-control">
      <select :id="id" v-model="model" v-bind="$attrs" :disabled="disabled">
        <option v-for="option in options" :key="option.value" :value="option.value">
          {{ option.label }}
        </option>
      </select>
      <AppIcon :icon="ChevronDown" size="sm" />
    </div>
  </div>
</template>

<style scoped>
.modern-select {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-2);
}
.modern-select label {
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-select-control {
  position: relative;
  min-width: 0;
}
.modern-select select {
  appearance: none;
  width: 100%;
  min-height: var(--modern-control-md);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
  padding: var(--modern-space-1-5) var(--modern-space-8) var(--modern-space-1-5)
    var(--modern-space-3);
  color: var(--modern-text);
  font-size: var(--modern-font-size-small);
  cursor: pointer;
}
.modern-select select:disabled {
  opacity: var(--modern-opacity-disabled);
  cursor: not-allowed;
}
.modern-select-control > svg {
  position: absolute;
  right: var(--modern-space-3);
  top: 50%;
  transform: translateY(-50%);
  pointer-events: none;
  color: var(--modern-muted);
}
@media (max-width: 760px) {
  .modern-select select {
    min-height: var(--modern-touch-target);
    font-size: var(--modern-font-size-input-mobile);
  }
}
</style>
