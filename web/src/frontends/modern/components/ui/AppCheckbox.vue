<script setup lang="ts">
import { Check, Minus } from '@lucide/vue'
import { ref, useAttrs, type StyleValue } from 'vue'
import AppIcon from './AppIcon.vue'

defineOptions({ inheritAttrs: false })
defineProps<{ label: string; labelHidden?: boolean; disabled?: boolean; indeterminate?: boolean }>()
const model = defineModel<boolean>({ required: true })
const input = ref<HTMLInputElement>()
const attrs = useAttrs()
function inputAttrs() {
  return Object.fromEntries(
    Object.entries(attrs).filter(([key]) => key !== 'class' && key !== 'style'),
  )
}
defineExpose({ focus: () => input.value?.focus() })
</script>

<template>
  <label
    class="modern-checkbox"
    :class="[$attrs.class, { 'is-disabled': disabled }]"
    :style="$attrs.style as StyleValue"
  >
    <input
      ref="input"
      v-model="model"
      v-bind="inputAttrs()"
      type="checkbox"
      :disabled="disabled"
      :indeterminate="indeterminate"
      :aria-label="labelHidden || $slots.default ? label : undefined"
    />
    <span class="modern-checkbox-control" aria-hidden="true">
      <AppIcon v-if="indeterminate || model" :icon="indeterminate ? Minus : Check" size="xs" />
    </span>
    <span class="modern-checkbox-label" :class="{ 'modern-sr-only': labelHidden }"
      ><slot>{{ label }}</slot></span
    >
  </label>
</template>

<style scoped>
.modern-checkbox {
  position: relative;
  display: inline-flex;
  width: fit-content;
  min-height: var(--modern-control-sm);
  align-items: center;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
  cursor: pointer;
}
.modern-checkbox input {
  position: absolute;
  left: 0;
  width: var(--modern-checkbox-size);
  height: var(--modern-checkbox-size);
  margin: 0;
  opacity: 0;
  cursor: inherit;
}
.modern-checkbox-control {
  display: inline-flex;
  width: var(--modern-checkbox-size);
  height: var(--modern-checkbox-size);
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border: var(--modern-line-width) solid var(--modern-control-border);
  border-radius: var(--modern-radius-small);
  background: var(--modern-surface);
  color: var(--modern-on-action);
  box-shadow: var(--modern-shadow-control);
  transition:
    background-color var(--modern-motion-fast) var(--modern-motion-ease),
    border-color var(--modern-motion-fast) var(--modern-motion-ease),
    box-shadow var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-checkbox:hover:not(.is-disabled) .modern-checkbox-control {
  border-color: var(--modern-control-border-hover);
}
.modern-checkbox input:checked + .modern-checkbox-control {
  border-color: var(--modern-action);
  background: var(--modern-action);
}
.modern-checkbox input:indeterminate + .modern-checkbox-control {
  border-color: var(--modern-action);
  background: var(--modern-action);
}
.modern-checkbox-label {
  min-width: 0;
  flex: 1;
}
.modern-checkbox input:focus-visible + .modern-checkbox-control {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
  box-shadow: var(--modern-shadow-focus);
}
.modern-checkbox.is-disabled {
  opacity: var(--modern-opacity-disabled);
  cursor: not-allowed;
}
@media (max-width: 760px) {
  .modern-checkbox {
    min-height: var(--modern-touch-target);
  }
}
</style>
