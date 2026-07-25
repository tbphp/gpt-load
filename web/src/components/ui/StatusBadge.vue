<script setup lang="ts">
import { CircleAlert, CircleCheck, CircleHelp, CircleOff } from 'lucide-vue-next'
import { computed } from 'vue'

const props = withDefaults(defineProps<{ tone?: 'success' | 'warning' | 'danger' | 'neutral' }>(), {
  tone: 'neutral',
})
const icon = computed(() => {
  if (props.tone === 'success') return CircleCheck
  if (props.tone === 'warning') return CircleAlert
  if (props.tone === 'danger') return CircleOff
  return CircleHelp
})
</script>

<template>
  <span class="status-badge" :class="`status-badge--${tone}`">
    <component :is="icon" :size="14" aria-hidden="true" />
    <slot />
  </span>
</template>

<style scoped>
.status-badge {
  display: inline-flex;
  min-height: 28px;
  align-items: center;
  gap: var(--space-1);
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  color: var(--color-text-muted);
  padding: 4px 8px;
  font-size: 0.8125rem;
  font-weight: 650;
}
.status-badge--success {
  background: var(--color-success-bg);
}
.status-badge--warning {
  background: var(--color-warning-bg);
}
.status-badge--danger {
  background: var(--color-danger-bg);
}
.status-badge--success,
.status-badge--warning,
.status-badge--danger {
  color: var(--color-text);
}
.status-badge--success svg {
  color: var(--color-success);
}
.status-badge--warning svg {
  color: var(--color-warning);
}
.status-badge--danger svg {
  color: var(--color-danger);
}
</style>
