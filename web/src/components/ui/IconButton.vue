<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    disabled?: boolean
    pressed?: boolean
  }>(),
  { disabled: false, pressed: undefined },
)
</script>

<template>
  <button
    class="icon-button"
    type="button"
    :aria-label="label"
    :aria-pressed="pressed"
    :disabled="disabled"
  >
    <slot />
  </button>
</template>

<style scoped>
.icon-button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  cursor: pointer;
  transition:
    color var(--duration-fast) ease,
    background-color var(--duration-fast) ease;
}

.icon-button:hover:not(:disabled) {
  background: var(--color-surface-secondary);
  color: var(--color-text);
}

.icon-button:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}
</style>
