<script setup lang="ts">
import { SwitchRoot, SwitchThumb } from 'reka-ui'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    disabled?: boolean
    label: string
  }>(),
  { disabled: false },
)
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()
</script>

<template>
  <SwitchRoot
    class="app-switch"
    :model-value="props.modelValue"
    :disabled="props.disabled"
    :aria-label="props.label"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <SwitchThumb class="app-switch__thumb" />
  </SwitchRoot>
</template>

<style scoped>
.app-switch {
  display: inline-flex;
  width: 34px;
  height: 20px;
  flex: 0 0 auto;
  align-items: center;
  border: 1px solid var(--color-border-strong);
  border-radius: 999px;
  background: var(--color-surface-sunken);
  padding: 2px;
  cursor: pointer;
  transition:
    background-color var(--duration-fast) var(--easing-standard),
    border-color var(--duration-fast) var(--easing-standard);
}

.app-switch[data-state='checked'] {
  border-color: var(--color-action);
  background: var(--color-action);
}

.app-switch:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.app-switch:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.app-switch__thumb {
  display: block;
  width: 14px;
  height: 14px;
  transform: translateX(0);
  border-radius: 50%;
  background: var(--color-surface);
  box-shadow: var(--shadow-card);
  transition: transform var(--duration-fast) var(--easing-standard);
}

.app-switch[data-state='checked'] .app-switch__thumb {
  transform: translateX(14px);
}

@media (prefers-reduced-motion: reduce) {
  .app-switch,
  .app-switch__thumb {
    transition: none;
  }
}
</style>
