<script setup lang="ts">
import { PopoverAnchor, PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'

import AppTooltip from './AppTooltip.vue'

const open = defineModel<boolean>('open', { default: false })

withDefaults(
  defineProps<{
    align?: 'start' | 'center' | 'end'
    side?: 'top' | 'right' | 'bottom' | 'left'
    contentClass?: string
    tooltip?: string
  }>(),
  {
    align: 'end',
    side: 'bottom',
    contentClass: undefined,
    tooltip: undefined,
  },
)
</script>

<template>
  <span class="app-popover">
    <PopoverRoot v-model:open="open" :modal="false">
      <!-- Tooltip 有独立的定位上下文，弹层锚点必须放在它外面。 -->
      <PopoverAnchor v-if="tooltip" as-child>
        <span class="app-popover__anchor">
          <AppTooltip :content="tooltip" :disabled="open">
            <PopoverTrigger as-child>
              <slot name="trigger" />
            </PopoverTrigger>
          </AppTooltip>
        </span>
      </PopoverAnchor>
      <PopoverTrigger v-else as-child>
        <slot name="trigger" />
      </PopoverTrigger>
      <PopoverPortal>
        <PopoverContent
          class="app-popover__content"
          :class="contentClass"
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

.app-popover__anchor {
  display: inline-flex;
  min-width: 0;
  flex: 1;
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
    transform: translateY(-4px) scale(0.98);
  }
}

@media (prefers-reduced-motion: reduce) {
  .app-popover__content[data-state='open'] {
    animation: none;
  }
}
</style>
