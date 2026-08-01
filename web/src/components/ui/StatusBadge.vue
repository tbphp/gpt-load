<script setup lang="ts">
import { CircleAlert, CircleCheck, CircleHelp, CircleOff, LoaderCircle } from '@lucide/vue'
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
    size?: 'default' | 'compact'
  }>(),
  {
    tone: 'neutral',
    status: undefined,
    size: 'default',
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
const resolvedIcon = computed<StatusIcon>(() => {
  const statusIcon = presentation.value.icon as StatusIcon | undefined
  if (statusIcon) return statusIcon
  if (resolvedTone.value === 'success') return 'check'
  if (resolvedTone.value === 'warning') return 'alert'
  if (resolvedTone.value === 'danger') return 'off'
  return 'help'
})
const icon = computed(() => {
  if (resolvedIcon.value === 'progress') return LoaderCircle
  if (resolvedIcon.value === 'check') return CircleCheck
  if (resolvedIcon.value === 'alert') return CircleAlert
  if (resolvedIcon.value === 'off') return CircleOff
  return CircleHelp
})
</script>

<template>
  <span class="status-badge" :class="[`status-badge--${resolvedTone}`, `status-badge--${size}`]">
    <component :is="icon" :size="size === 'compact' ? 12 : 14" aria-hidden="true" />
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
.status-badge--neutral {
  background: var(--color-neutral-bg);
}
.status-badge--warning {
  background: var(--color-warning-bg);
}
.status-badge--danger {
  background: var(--color-danger-bg);
}
.status-badge--compact {
  min-height: 24px;
  padding: 3px 8px;
  font-size: var(--text-sm);
  font-weight: 600;
}
.status-badge--success,
.status-badge--neutral,
.status-badge--warning,
.status-badge--danger {
  color: var(--color-text);
}
.status-badge--neutral svg {
  color: var(--color-neutral);
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
