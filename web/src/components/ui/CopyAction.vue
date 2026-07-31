<script setup lang="ts">
import { Check, Copy } from '@lucide/vue'

withDefaults(
  defineProps<{
    label: string
    disabled?: boolean
    busy?: boolean
    state?: 'idle' | 'success'
    variant?: 'default' | 'embedded'
  }>(),
  {
    disabled: false,
    busy: false,
    state: 'idle',
    variant: 'default',
  },
)

defineEmits<{ copy: [] }>()
</script>

<template>
  <button
    class="copy-action"
    :class="[`copy-action--${variant}`, { 'copy-action--success': state === 'success' }]"
    type="button"
    :aria-label="label"
    :aria-busy="busy ? 'true' : undefined"
    :disabled="disabled || busy"
    @click="$emit('copy')"
  >
    <Check v-if="state === 'success'" :size="16" aria-hidden="true" />
    <Copy v-else :size="16" aria-hidden="true" />
  </button>
</template>

<style scoped>
.copy-action {
  display: inline-flex;
  width: var(--control-md);
  min-width: var(--control-md);
  height: var(--control-md);
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  cursor: pointer;
  transition:
    color var(--duration-fast) var(--easing-standard),
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard),
    opacity var(--duration-fast) var(--easing-standard);
}

.copy-action:hover:not(:disabled) {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
  color: var(--color-action);
}

.copy-action--embedded {
  width: 34px;
  min-width: 34px;
  height: auto;
  align-self: stretch;
  border-width: 0 0 0 1px;
  border-radius: 0;
}

.copy-action--embedded :deep(svg) {
  width: 14px;
  height: 14px;
}

.copy-action:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.copy-action[aria-busy='true'] {
  cursor: wait;
}

.copy-action--success {
  color: var(--color-success);
}

</style>
