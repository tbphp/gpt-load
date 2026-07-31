<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    tone?: 'info' | 'success' | 'warning' | 'danger'
  }>(),
  {
    tone: 'info',
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
    :class="`inline-feedback--${tone}`"
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
</style>
