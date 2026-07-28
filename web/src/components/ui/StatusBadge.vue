<script setup lang="ts">
import { CircleAlert, CircleCheck, CircleHelp, CircleOff, LoaderCircle } from 'lucide-vue-next'
import { computed } from 'vue'

import {
  presentMutationStatus,
  presentOperationalStatus,
  type MutationStatus,
  type OperationalStatus,
  type StatusIcon,
  type StatusTone,
} from './status-presenter'

const props = withDefaults(
  defineProps<{
    tone?: StatusTone
    status?: OperationalStatus | MutationStatus
  }>(),
  {
    tone: 'neutral',
    status: undefined,
  },
)
const presentation = computed(() => {
  if (!props.status) return { tone: props.tone, icon: undefined }
  if (
    props.status === 'confirmed' ||
    props.status === 'failed' ||
    props.status === 'indeterminate' ||
    props.status === 'reconciling'
  ) {
    return presentMutationStatus(props.status)
  }
  return presentOperationalStatus(props.status)
})
const resolvedTone = computed(() => presentation.value.tone)
const icon = computed(() => {
  const statusIcon = presentation.value.icon as StatusIcon | undefined
  if (statusIcon === 'progress') return LoaderCircle
  if (statusIcon === 'check' || (!statusIcon && resolvedTone.value === 'success'))
    return CircleCheck
  if (statusIcon === 'alert' || (!statusIcon && resolvedTone.value === 'warning'))
    return CircleAlert
  if (statusIcon === 'off' || (!statusIcon && resolvedTone.value === 'danger')) return CircleOff
  return CircleHelp
})
</script>

<template>
  <span
    class="status-badge"
    :class="`status-badge--${resolvedTone}`"
    :data-status="status || undefined"
  >
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
