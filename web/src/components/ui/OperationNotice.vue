<script setup lang="ts">
import { CircleAlert, LoaderCircle } from 'lucide-vue-next'

import AppButton from './AppButton.vue'

withDefaults(
  defineProps<{
    state: 'indeterminate' | 'reconciling'
    message: string
    actionLabel: string
    busy?: boolean
  }>(),
  {
    busy: false,
  },
)

defineEmits<{ recover: [] }>()
</script>

<template>
  <div
    class="operation-notice"
    :class="`operation-notice--${state}`"
    role="status"
    aria-live="polite"
  >
    <LoaderCircle v-if="state === 'reconciling'" class="operation-notice__spin" :size="18" />
    <CircleAlert v-else :size="18" />
    <span class="operation-notice__message">{{ message }}</span>
    <AppButton
      variant="secondary"
      size="sm"
      :busy="busy"
      :disabled="busy"
      @click="$emit('recover')"
    >
      {{ actionLabel }}
    </AppButton>
  </div>
</template>

<style scoped>
.operation-notice {
  display: flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-action-soft);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
}

.operation-notice__message {
  flex: 1;
}

.operation-notice__spin {
  animation: operation-spin 1s linear infinite;
}

@keyframes operation-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 639px) {
  .operation-notice {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .operation-notice__message {
    min-width: calc(100% - 32px);
  }
}

@media (prefers-reduced-motion: reduce) {
  .operation-notice__spin {
    animation: none;
  }
}
</style>
