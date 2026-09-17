<script setup lang="ts">
import { Primitive } from 'reka-ui'
import type { ControlSize } from './types'

withDefaults(
  defineProps<{ asChild?: boolean; disabled?: boolean; invalid?: boolean; size?: ControlSize }>(),
  { size: 'md' },
)
</script>

<template>
  <Primitive
    as="div"
    :as-child="asChild"
    class="modern-field-control"
    :class="[{ 'is-invalid': invalid, 'is-disabled': disabled }, `modern-field-control--${size}`]"
  >
    <slot />
  </Primitive>
</template>

<style scoped>
.modern-field-control {
  display: flex;
  width: 100%;
  min-width: 0;
  min-height: var(--modern-control-md);
  align-items: center;
  gap: var(--modern-space-2);
  border: var(--modern-line-width) solid var(--modern-control-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
  padding: 0 var(--modern-space-3);
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
  line-height: var(--modern-leading-compact);
  box-shadow: var(--modern-shadow-control);
}
.modern-field-control:hover:not(.is-disabled, .is-invalid) {
  border-color: var(--modern-control-border-hover);
}
.modern-field-control:focus-within {
  border-color: var(--modern-accent);
  box-shadow: var(--modern-shadow-focus);
}
.modern-field-control.is-invalid {
  border-color: var(--modern-danger);
}
.modern-field-control.is-invalid:focus-within {
  box-shadow: var(--modern-shadow-focus-danger);
}
.modern-field-control.is-disabled {
  background: var(--modern-control-disabled);
  box-shadow: none;
  cursor: not-allowed;
}
.modern-field-control--sm {
  min-height: var(--modern-control-sm);
}
.modern-field-control--xs {
  min-height: var(--modern-control-xs);
  padding-inline: var(--modern-space-2);
  font-size: var(--modern-font-size-small);
}
@media (max-width: 760px) {
  .modern-field-control {
    min-height: var(--modern-touch-target);
  }
}
</style>
