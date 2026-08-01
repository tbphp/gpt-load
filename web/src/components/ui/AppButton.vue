<script setup lang="ts">
withDefaults(
  defineProps<{
    type?: 'button' | 'submit' | 'reset'
    disabled?: boolean
    busy?: boolean
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger' | 'link'
    size?: 'inline' | 'compact' | 'sm' | 'md' | 'cta' | 'lg'
  }>(),
  {
    type: 'button',
    disabled: false,
    busy: false,
    variant: 'primary',
    size: 'md',
  },
)
</script>

<template>
  <button
    class="app-button"
    :class="[`app-button--${variant}`, `app-button--${size}`]"
    :type="type"
    :disabled="disabled || busy"
    :aria-busy="busy ? 'true' : undefined"
  >
    <slot />
  </button>
</template>

<style scoped>
.app-button {
  display: inline-flex;
  min-height: var(--control-md);
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: 1px solid transparent;
  border-radius: var(--radius-control);
  padding: 0 14px;
  font: inherit;
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
  transition:
    background-color var(--duration-fast) var(--easing-standard),
    border-color var(--duration-fast) var(--easing-standard),
    color var(--duration-fast) var(--easing-standard),
    opacity var(--duration-fast) var(--easing-standard);
}

.app-button--sm {
  min-height: var(--control-sm);
  padding-inline: 12px;
  font-size: var(--text-sm);
}

.app-button--compact {
  min-height: var(--control-compact);
  padding-inline: 12px;
  font-size: var(--text-meta);
  font-weight: 560;
}

.app-button--cta {
  min-height: var(--control-md);
  gap: var(--space-2);
  padding-inline: 15px;
}

.app-button--inline {
  min-height: 0;
  padding: 0;
  font-size: inherit;
}

.app-button--lg {
  min-height: var(--control-lg);
  padding-inline: 18px;
}

.app-button--primary {
  border-color: var(--color-action);
  background: var(--color-action);
  color: var(--color-action-ink);
}

.app-button--secondary {
  border-color: var(--color-border-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
}

.app-button--ghost {
  background: transparent;
  color: var(--color-text-muted);
}

.app-button--danger {
  border-color: var(--color-danger);
  background: var(--color-danger);
  color: var(--color-action-ink);
}

.app-button--link {
  border: 0;
  background: transparent;
  color: var(--color-action);
  font-weight: inherit;
  line-height: inherit;
  vertical-align: baseline;
}

.app-button--primary:hover:not(:disabled) {
  border-color: var(--color-action-hover);
  background: var(--color-action-hover);
}

.app-button--secondary:hover:not(:disabled) {
  border-color: var(--color-text-faint);
  color: var(--color-text);
}

.app-button--ghost:hover:not(:disabled) {
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.app-button--danger:hover:not(:disabled) {
  opacity: 0.88;
}

.app-button--link:hover:not(:disabled) {
  color: var(--color-action-hover);
  text-decoration: underline;
}
.app-button:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.app-button[aria-busy='true'] {
  cursor: wait;
}
</style>
