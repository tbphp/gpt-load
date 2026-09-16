<script setup lang="ts">
import { CircleCheck, CircleX, Info, TriangleAlert } from '@lucide/vue'
import AppIcon from './AppIcon.vue'
import type { SemanticTone } from './types'

withDefaults(
  defineProps<{
    tone?: Exclude<SemanticTone, 'neutral'>
    bordered?: boolean
  }>(),
  { tone: 'info' },
)
const icons = { info: Info, success: CircleCheck, warning: TriangleAlert, danger: CircleX }
</script>

<template>
  <div
    class="modern-notice"
    :class="[`modern-notice--${tone}`, { 'modern-notice--bordered': bordered }]"
    :role="tone === 'danger' ? 'alert' : 'status'"
  >
    <AppIcon :icon="icons[tone]" class="modern-notice-icon" />
    <div class="modern-notice-message"><slot /></div>
    <div v-if="$slots.actions" class="modern-notice-actions"><slot name="actions" /></div>
  </div>
</template>

<style scoped>
.modern-notice {
  --modern-notice-color: var(--modern-info);
  --modern-notice-background: var(--modern-info-soft);
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3);
  border: var(--modern-line-width) solid transparent;
  border-radius: var(--modern-radius-control);
  background: var(--modern-notice-background);
  padding: var(--modern-space-3) var(--modern-space-4);
  color: var(--modern-notice-color);
  font-size: var(--modern-font-size-secondary);
  line-height: var(--modern-leading-body);
}
.modern-notice--success {
  --modern-notice-color: var(--modern-success);
  --modern-notice-background: var(--modern-success-soft);
}
.modern-notice--warning {
  --modern-notice-color: var(--modern-warning);
  --modern-notice-background: var(--modern-warning-soft);
}
.modern-notice--danger {
  --modern-notice-color: var(--modern-danger);
  --modern-notice-background: var(--modern-danger-soft);
}
.modern-notice--bordered {
  border-color: var(--modern-notice-color);
}
.modern-notice-icon {
  align-self: flex-start;
  margin-top: var(--modern-space-0-5);
}
.modern-notice-message {
  min-width: 0;
  flex: 1;
  overflow-wrap: anywhere;
}
.modern-notice-actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--modern-space-2);
}
</style>
