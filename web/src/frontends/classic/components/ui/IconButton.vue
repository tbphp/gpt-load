<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    disabled?: boolean
    busy?: boolean
    pressed?: boolean
    variant?: 'default' | 'surface' | 'ghost' | 'danger'
    tone?: 'neutral' | 'action' | 'success' | 'warning' | 'danger'
    size?: 'md' | 'compact' | 'xs'
  }>(),
  {
    disabled: false,
    busy: false,
    pressed: undefined,
    variant: 'default',
    tone: 'neutral',
    size: 'md',
  },
)
</script>

<template>
  <button
    class="icon-button"
    :class="[`icon-button--${variant}`, `icon-button--${size}`, `icon-button--tone-${tone}`]"
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

.icon-button[aria-pressed='true'] {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.icon-button--compact {
  width: var(--control-compact);
  height: var(--control-compact);
}

.icon-button--xs {
  width: 28px;
  height: 28px;
}

.icon-button--surface:hover:not(:disabled):not([aria-pressed='true']) {
  background: var(--color-surface);
}

.icon-button--ghost {
  border-color: transparent;
  background: transparent;
  color: var(--color-text-faint);
}

.icon-button--ghost.icon-button--tone-action {
  color: var(--color-action);
}

.icon-button--ghost.icon-button--tone-success {
  color: var(--color-success);
}

.icon-button--ghost.icon-button--tone-warning {
  color: var(--color-warning);
}

.icon-button--ghost.icon-button--tone-danger {
  color: var(--color-danger);
}

.icon-button--ghost:hover:not(:disabled) {
  border-color: transparent;
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.icon-button--ghost.icon-button--tone-action:hover:not(:disabled) {
  background: var(--color-action-soft);
  color: var(--color-action);
}

.icon-button--ghost.icon-button--tone-success:hover:not(:disabled) {
  background: var(--color-success-bg);
  color: var(--color-success);
}

.icon-button--ghost.icon-button--tone-warning:hover:not(:disabled) {
  background: var(--color-warning-bg);
  color: var(--color-warning);
}

.icon-button--ghost.icon-button--tone-danger:hover:not(:disabled) {
  background: var(--color-danger-bg);
  color: var(--color-danger);
}

.icon-button:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.icon-button--danger {
  border-color: var(--color-danger);
  background: var(--color-danger-bg);
  color: var(--color-danger);
}

.icon-button[aria-busy='true'] {
  cursor: wait;
}
</style>
