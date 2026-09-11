<script setup lang="ts">
import type { Component } from 'vue'

import AppIcon from './AppIcon.vue'

withDefaults(
  defineProps<{ icon?: Component; tone?: 'neutral' | 'info' | 'success' | 'warning' | 'danger' }>(),
  {
    icon: undefined,
    tone: 'neutral',
  },
)
</script>

<template>
  <span class="modern-badge" :class="`modern-badge--${tone}`">
    <AppIcon v-if="icon" :icon="icon" size="sm" />
    <span><slot /></span>
  </span>
</template>

<style scoped>
.modern-badge {
  display: inline-flex;
  min-height: var(--modern-control-xs);
  align-items: center;
  gap: var(--modern-space-1-5);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  padding: var(--modern-space-1) var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
  line-height: var(--modern-leading-compact);
}
.modern-badge--info {
  background: var(--modern-info-soft);
  color: var(--modern-info);
}
.modern-badge--success {
  background: var(--modern-success-soft);
  color: var(--modern-success);
}
.modern-badge--warning {
  background: var(--modern-warning-soft);
  color: var(--modern-warning);
}
.modern-badge--danger {
  background: var(--modern-danger-soft);
  color: var(--modern-danger);
}
</style>
