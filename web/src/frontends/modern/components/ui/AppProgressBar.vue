<script setup lang="ts">
import { computed } from 'vue'
import type { SemanticTone } from './types'
const props = withDefaults(
  defineProps<{ label: string; value?: number; tone?: SemanticTone; size?: 'sm' | 'md' }>(),
  { tone: 'success', value: undefined, size: 'md' },
)
const amount = computed(() =>
  props.value === undefined ? undefined : Math.max(0, Math.min(100, props.value)),
)
</script>
<template>
  <div
    class="modern-progress-bar"
    :class="[`modern-progress-bar--${tone}`, `modern-progress-bar--${size}`]"
    role="progressbar"
    :aria-label="label"
    :aria-valuemin="0"
    :aria-valuemax="100"
    :aria-valuenow="amount"
  >
    <span :style="{ width: (amount ?? 0) + '%' }" />
  </div>
</template>
<style scoped>
.modern-progress-bar {
  height: var(--modern-status-bar-height);
  width: 100%;
  overflow: hidden;
  background: var(--modern-progress-track);
  box-shadow: inset 0 0 0 var(--modern-line-width) var(--modern-progress-track-border);
  border-radius: var(--modern-radius-small);
}
.modern-progress-bar--sm {
  height: var(--modern-progress-bar-sm);
}
.modern-progress-bar > span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--modern-status-success);
  transition: width var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-progress-bar--warning > span {
  background: var(--modern-status-warning);
}
.modern-progress-bar--danger > span {
  background: var(--modern-status-danger);
}
.modern-progress-bar--neutral > span {
  background: var(--modern-status-neutral);
}
.modern-progress-bar--info > span {
  background: var(--modern-info);
}
</style>
