<script setup lang="ts">
import type { Component } from 'vue'
import AppIcon from './AppIcon.vue'
import type { SemanticTone } from './types'

withDefaults(
  defineProps<{
    icon?: Component
    tone?: SemanticTone | 'brand'
    variant?: 'soft' | 'plain' | 'outline'
    size?: 'xs' | 'sm'
    dot?: boolean
    mono?: boolean
  }>(),
  { icon: undefined, tone: 'neutral', variant: 'soft', size: 'sm' },
)
</script>

<template>
  <span
    class="modern-badge"
    :class="[
      `modern-badge--${tone}`,
      `modern-badge--${variant}`,
      `modern-badge--${size}`,
      { 'is-mono': mono },
    ]"
  >
    <AppIcon v-if="icon" :icon="icon" size="sm" />
    <span v-else-if="dot" class="modern-badge-dot" aria-hidden="true" />
    <span class="modern-badge-label"><slot /></span>
  </span>
</template>

<style scoped>
.modern-badge {
  --modern-badge-color: var(--modern-muted);
  --modern-badge-background: var(--modern-subtle);
  display: inline-flex;
  width: fit-content;
  max-width: 100%;
  min-height: var(--modern-badge-sm);
  align-items: center;
  gap: var(--modern-space-1-5);
  border-radius: var(--modern-radius-small);
  background: var(--modern-badge-background);
  padding: var(--modern-space-0-5) var(--modern-space-2);
  color: var(--modern-badge-color);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
  line-height: var(--modern-leading-compact);
}
.modern-badge--info {
  --modern-badge-color: var(--modern-info);
  --modern-badge-background: var(--modern-info-soft);
}
.modern-badge--brand {
  --modern-badge-color: var(--modern-badge-brand-text);
  --modern-badge-background: var(--modern-badge-brand-surface);
}
.modern-badge--success {
  --modern-badge-color: var(--modern-success);
  --modern-badge-background: var(--modern-success-soft);
}
.modern-badge--warning {
  --modern-badge-color: var(--modern-warning);
  --modern-badge-background: var(--modern-warning-soft);
}
.modern-badge--danger {
  --modern-badge-color: var(--modern-danger);
  --modern-badge-background: var(--modern-danger-soft);
}
.modern-badge--plain {
  min-height: 0;
  background: transparent;
  padding: 0;
  font-weight: var(--modern-weight-regular);
}
.modern-badge--outline {
  border: var(--modern-line-width) solid var(--modern-border);
  background: transparent;
  font-weight: var(--modern-weight-regular);
}
.modern-badge--xs {
  min-height: var(--modern-badge-xs);
  font-size: var(--modern-font-size-caption);
}
.modern-badge.is-mono {
  font-family: var(--modern-font-mono);
  font-weight: var(--modern-weight-regular);
}
.modern-badge-dot {
  width: var(--modern-space-1-5);
  height: var(--modern-space-1-5);
  flex-shrink: 0;
  border-radius: var(--modern-radius-round);
  background: currentColor;
}
.modern-badge-label {
  min-width: 0;
  overflow-wrap: anywhere;
}
</style>
