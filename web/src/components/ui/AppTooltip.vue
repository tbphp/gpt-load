<script setup lang="ts">
import {
  TooltipContent,
  TooltipPortal,
  TooltipProvider,
  TooltipRoot,
  TooltipTrigger,
} from 'reka-ui'

withDefaults(
  defineProps<{
    content: string
    side?: 'top' | 'right' | 'bottom' | 'left'
    align?: 'start' | 'center' | 'end'
    disabled?: boolean
  }>(),
  {
    side: 'top',
    align: 'center',
    disabled: false,
  },
)
</script>

<template>
  <TooltipProvider :delay-duration="180" :skip-delay-duration="120">
    <TooltipRoot :disabled="disabled">
      <TooltipTrigger as-child>
        <slot />
      </TooltipTrigger>
      <TooltipPortal>
        <TooltipContent
          class="app-tooltip__content"
          :side="side"
          :align="align"
          :side-offset="7"
          :collision-padding="8"
        >
          {{ content }}
        </TooltipContent>
      </TooltipPortal>
    </TooltipRoot>
  </TooltipProvider>
</template>

<style>
.app-tooltip__content {
  z-index: var(--z-popover);
  max-width: min(320px, calc(100vw - 24px));
  border-radius: var(--radius-control);
  background: var(--color-text);
  color: var(--color-text-inverse);
  padding: 6px 8px;
  box-shadow: var(--shadow-overlay);
  font-size: var(--text-label-xs);
  line-height: 1.45;
  transform-origin: var(--reka-tooltip-content-transform-origin);
  white-space: pre-line;
}

.app-tooltip__content[data-state='delayed-open'],
.app-tooltip__content[data-state='instant-open'] {
  animation: app-tooltip-in var(--duration-fast) var(--easing-standard);
}

@keyframes app-tooltip-in {
  from {
    opacity: 0;
    transform: translateY(2px) scale(0.98);
  }
}

@media (prefers-reduced-motion: reduce) {
  .app-tooltip__content[data-state='delayed-open'],
  .app-tooltip__content[data-state='instant-open'] {
    animation: none;
  }
}
</style>
