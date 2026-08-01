<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    tone?: 'info' | 'success' | 'warning' | 'danger'
    appearance?: 'default' | 'hint' | 'auth' | 'toast'
  }>(),
  {
    tone: 'info',
    appearance: 'default',
  },
)

const role = computed(() =>
  props.tone === 'info' || props.tone === 'success' ? 'status' : 'alert',
)
const live = computed(() => (role.value === 'status' ? 'polite' : 'assertive'))
const glyph = computed(() => {
  if (props.tone === 'success') return '✓'
  if (props.tone === 'danger' || props.tone === 'warning') return '▲'
  return 'i'
})
</script>

<template>
  <div
    class="inline-feedback"
    :class="[`inline-feedback--${tone}`, `inline-feedback--${appearance}`]"
    :role="role"
    :aria-live="live"
    aria-atomic="true"
  >
    <span class="inline-feedback__glyph" aria-hidden="true">{{ glyph }}</span>
    <span><slot /></span>
  </div>
</template>

<style scoped>
.inline-feedback {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  border: 1px solid transparent;
  border-radius: var(--radius-control);
  padding: 9px 10px;
  font-size: var(--text-meta);
  line-height: var(--line-normal);
}

.inline-feedback__glyph {
  display: grid;
  width: 18px;
  height: 18px;
  flex: none;
  place-items: center;
  font-weight: 600;
  line-height: 1;
}

.inline-feedback--info {
  border-color: var(--color-border-subtle);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
}

.inline-feedback--success {
  border-color: var(--color-success);
  background: var(--color-success-bg);
  color: var(--color-success);
}

.inline-feedback--warning {
  border-color: var(--color-warning);
  background: var(--color-warning-bg);
  color: var(--color-warning);
}

.inline-feedback--danger {
  border-color: var(--color-danger);
  background: var(--color-danger-bg);
  color: var(--color-danger);
}

.inline-feedback--hint {
  gap: 6px;
  border: 0;
  background: transparent;
  padding: 0;
  font-size: var(--text-sm);
}

.inline-feedback--hint .inline-feedback__glyph {
  width: 14px;
  height: 14px;
}

.inline-feedback--hint.inline-feedback--info {
  color: var(--color-action);
}

.inline-feedback--auth {
  gap: 0;
  border-color: color-mix(in srgb, var(--color-danger) 34%, var(--color-border-subtle));
  background: var(--color-danger-bg);
  color: var(--color-danger);
  padding: 8px 10px;
  font-size: var(--text-label-xs);
  line-height: 1.55;
}

.inline-feedback--auth .inline-feedback__glyph {
  display: none;
}

.inline-feedback--toast {
  position: fixed;
  z-index: var(--z-drawer);
  bottom: 26px;
  left: 50%;
  width: max-content;
  max-width: calc(100vw - 32px);
  transform: translateX(-50%);
  border: 0;
  border-radius: 8px;
  background: var(--color-text);
  color: var(--color-surface);
  padding: 8px 14px;
  box-shadow: var(--shadow-overlay);
  font-size: 12.5px;
}

.inline-feedback--toast .inline-feedback__glyph {
  display: none;
}
</style>
