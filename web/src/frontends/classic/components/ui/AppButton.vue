<script setup lang="ts">
withDefaults(
  defineProps<{
    type?: 'button' | 'submit' | 'reset'
    disabled?: boolean
    busy?: boolean
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger' | 'link'
    tone?: 'neutral' | 'action' | 'success' | 'warning' | 'danger'
    size?: 'inline' | 'compact' | 'sm' | 'md' | 'cta' | 'lg'
  }>(),
  {
    type: 'button',
    disabled: false,
    busy: false,
    variant: 'primary',
    tone: 'neutral',
    size: 'md',
  },
)
</script>

<template>
  <button
    class="app-button"
    :class="[`app-button--${variant}`, `app-button--${size}`, `app-button--tone-${tone}`]"
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
  gap: 7px;
  border: 1px solid transparent;
  border-radius: var(--radius-control);
  padding: 0 12px;
  font: inherit;
  font-size: var(--text-button);
  font-weight: 560;
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
  font-size: var(--text-sm);
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
  font-weight: 600;
}

.app-button--secondary {
  border-color: var(--color-border-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
}

.app-button--secondary.app-button--tone-action {
  border-color: color-mix(in srgb, var(--color-action) 34%, var(--color-border-control));
  background: var(--color-action-soft);
  color: var(--color-action);
}

.app-button--secondary.app-button--tone-success {
  border-color: color-mix(in srgb, var(--color-success) 34%, var(--color-border-control));
  background: var(--color-success-bg);
  color: var(--color-success);
}

.app-button--secondary.app-button--tone-warning {
  border-color: color-mix(in srgb, var(--color-warning) 34%, var(--color-border-control));
  background: var(--color-warning-bg);
  color: var(--color-warning);
}

.app-button--secondary.app-button--tone-danger {
  border-color: color-mix(in srgb, var(--color-danger) 34%, var(--color-border-control));
  background: var(--color-danger-bg);
  color: var(--color-danger);
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

.app-button--secondary.app-button--tone-action:hover:not(:disabled) {
  border-color: var(--color-action);
  color: var(--color-action);
}

.app-button--secondary.app-button--tone-success:hover:not(:disabled) {
  border-color: var(--color-success);
  color: var(--color-success);
}

.app-button--secondary.app-button--tone-warning:hover:not(:disabled) {
  border-color: var(--color-warning);
  color: var(--color-warning);
}

.app-button--secondary.app-button--tone-danger:hover:not(:disabled) {
  border-color: var(--color-danger);
  color: var(--color-danger);
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
  opacity: 0.46;
}

.app-button[aria-busy='true'] {
  cursor: wait;
}
</style>
