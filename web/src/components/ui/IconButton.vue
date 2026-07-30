<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    disabled?: boolean
    busy?: boolean
    pressed?: boolean
    variant?: 'default' | 'danger'
  }>(),
  { disabled: false, busy: false, pressed: undefined, variant: 'default' },
)
</script>

<template>
  <button
    class="icon-button"
    :class="`icon-button--${variant}`"
    type="button"
    :aria-label="label"
    :aria-pressed="pressed"
    :aria-busy="busy ? 'true' : undefined"
    :disabled="disabled || busy"
  >
    <slot />
  </button>
</template>

<style scoped>
.icon-button {
  display: inline-flex;
  width: var(--control-md);
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

.icon-button:hover:not(:disabled) {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.icon-button:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.icon-button--danger {
  border-color: var(--color-danger);
  color: var(--color-danger);
}

.icon-button[aria-busy='true'] {
  cursor: wait;
}
</style>
