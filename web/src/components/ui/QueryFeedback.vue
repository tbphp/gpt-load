<script setup lang="ts">
import { LoaderCircle, RefreshCw, TriangleAlert } from '@lucide/vue'

withDefaults(
  defineProps<{
    state: 'loading' | 'error' | 'stale' | 'indeterminate'
    message: string
    retryLabel?: string
  }>(),
  { retryLabel: 'Retry' },
)
defineEmits<{ retry: [] }>()
</script>

<template>
  <div
    class="query-feedback"
    :class="`query-feedback--${state}`"
    :role="state === 'error' ? 'alert' : 'status'"
  >
    <LoaderCircle v-if="state === 'loading'" class="query-feedback__spin" :size="18" />
    <TriangleAlert v-else :size="state === 'stale' ? 14 : 18" />
    <span>{{ message }}</span>
    <button v-if="state !== 'loading'" type="button" @click="$emit('retry')">
      <RefreshCw :size="15" aria-hidden="true" />{{ retryLabel }}
    </button>
  </div>
</template>

<style scoped>
.query-feedback {
  display: flex;
  min-height: 48px;
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: var(--space-3);
}
.query-feedback--error {
  border-color: var(--color-danger);
  background: var(--color-danger-bg);
  color: var(--color-danger);
}
.query-feedback--stale {
  min-height: 0;
  border-color: color-mix(in srgb, var(--color-warning) 32%, var(--color-border-subtle));
  background: var(--color-warning-bg);
  color: var(--color-warning);
  padding: 9px 11px;
  font-size: var(--text-sm);
}
.query-feedback--indeterminate {
  border-color: var(--color-border-control);
  background: var(--color-action-soft);
  color: var(--color-text);
}
.query-feedback button {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-1);
  margin-left: auto;
  border: 0;
  background: transparent;
  color: currentColor;
  font: inherit;
  font-weight: 650;
  cursor: pointer;
}
.query-feedback--stale button {
  min-height: var(--control-compact);
}
.query-feedback__spin {
  animation: query-spin 1s linear infinite;
}
@media (prefers-reduced-motion: reduce) {
  .query-feedback__spin {
    animation: none;
  }
}

@media (max-width: 860px) {
  .query-feedback--stale button {
    min-height: var(--touch-target);
  }
}

@keyframes query-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
