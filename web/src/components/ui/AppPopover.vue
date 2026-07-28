<script setup lang="ts">
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'

const open = defineModel<boolean>('open', { default: false })

withDefaults(
  defineProps<{
    align?: 'start' | 'center' | 'end'
    side?: 'top' | 'right' | 'bottom' | 'left'
  }>(),
  {
    align: 'end',
    side: 'bottom',
  },
)
</script>

<template>
  <span class="app-popover">
    <PopoverRoot v-model:open="open" :modal="false">
      <PopoverTrigger as-child>
        <slot name="trigger" />
      </PopoverTrigger>
      <PopoverPortal>
        <PopoverContent
          class="app-popover__content"
          :align="align"
          :side="side"
          :side-offset="8"
          :collision-padding="8"
        >
          <slot />
        </PopoverContent>
      </PopoverPortal>
    </PopoverRoot>
  </span>
</template>

<style>
.app-popover {
  display: inline-flex;
}

.app-popover__content {
  z-index: var(--z-popover);
  width: min(360px, var(--reka-popover-content-available-width));
  max-height: min(560px, var(--reka-popover-content-available-height));
  overflow: auto;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface-raised);
  color: var(--color-text);
  padding: var(--space-4);
  box-shadow: var(--shadow-overlay);
  transform-origin: var(--reka-popover-content-transform-origin);
}

.app-popover__content[data-state='open'] {
  animation: app-popover-in var(--duration-fast) var(--easing-standard);
}

@keyframes app-popover-in {
  from {
    opacity: 0;
    transform: translateY(-4px);
  }
}

@media (prefers-reduced-motion: reduce) {
  .app-popover__content[data-state='open'] {
    animation: none;
  }
}
</style>
