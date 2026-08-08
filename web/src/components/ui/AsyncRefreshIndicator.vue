<script setup lang="ts">
import { toRef } from 'vue'

import { useStableLoading } from '@/app/loading-state'

const props = defineProps<{
  active: boolean
  label: string
}>()
const visible = useStableLoading(toRef(props, 'active'))
</script>

<template>
  <div
    class="async-refresh-indicator"
    :class="{ 'async-refresh-indicator--visible': visible }"
    :role="visible ? 'status' : undefined"
    :aria-label="visible ? label : undefined"
    :aria-hidden="visible ? undefined : 'true'"
  >
    <span v-if="visible" class="sr-only">{{ label }}</span>
    <span class="async-refresh-indicator__bar" aria-hidden="true" />
  </div>
</template>

<style scoped>
.async-refresh-indicator {
  position: absolute;
  z-index: 1;
  top: 0;
  right: 0;
  left: 0;
  height: 2px;
  overflow: hidden;
  border-radius: 999px;
  opacity: 0;
  pointer-events: none;
  transition: opacity var(--duration-fast) var(--easing-standard);
}

.async-refresh-indicator--visible {
  opacity: 1;
}

.async-refresh-indicator__bar {
  position: absolute;
  inset: 0;
  background: linear-gradient(
    90deg,
    transparent 0%,
    var(--color-skeleton-highlight) 35%,
    var(--color-action) 50%,
    var(--color-skeleton-highlight) 65%,
    transparent 100%
  );
  transform: translateX(-100%);
  animation: async-refresh-shift var(--duration-loading-indicator) linear infinite;
}

@keyframes async-refresh-shift {
  to {
    transform: translateX(100%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .async-refresh-indicator__bar {
    background: var(--color-action);
    transform: none;
    animation: none;
  }
}
</style>
