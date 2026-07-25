<script setup lang="ts">
import { LoaderCircle, RefreshCw, TriangleAlert } from 'lucide-vue-next'

withDefaults(
  defineProps<{
    state: 'loading' | 'error' | 'stale'
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
    :role="state === 'loading' ? 'status' : 'alert'"
  >
    <LoaderCircle v-if="state === 'loading'" class="query-feedback__spin" :size="18" />
    <TriangleAlert v-else :size="18" />
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
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text-muted);
  padding: var(--space-3);
}
.query-feedback--error {
  border-color: var(--color-danger);
  background: var(--color-danger-bg);
  color: var(--color-danger);
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
.query-feedback__spin {
  animation: query-spin 1s linear infinite;
}
@media (prefers-reduced-motion: reduce) {
  .query-feedback__spin {
    animation: none;
  }
}

@keyframes query-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
