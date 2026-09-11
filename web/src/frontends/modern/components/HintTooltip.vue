<script setup lang="ts">
import { TooltipContent, TooltipPortal, TooltipRoot, TooltipTrigger } from 'reka-ui'

import { overlaySideOffset } from './ui/overlay'

defineProps<{
  label: string
  side?: 'top' | 'right' | 'bottom' | 'left'
  disabled?: boolean
}>()
</script>

<template>
  <slot v-if="disabled" />
  <TooltipRoot v-else>
    <TooltipTrigger as-child><slot /></TooltipTrigger>
    <TooltipPortal>
      <TooltipContent
        class="modern-tooltip"
        :side="side ?? 'bottom'"
        :side-offset="overlaySideOffset"
      >
        {{ label }}
      </TooltipContent>
    </TooltipPortal>
  </TooltipRoot>
</template>

<style scoped>
.modern-tooltip {
  z-index: var(--modern-layer-tooltip);
  max-width: min(var(--modern-tooltip-max-width), calc(100vw - var(--modern-space-8)));
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-tooltip-surface);
  padding: var(--modern-space-1) var(--modern-space-2);
  color: var(--modern-tooltip-text);
  font-size: var(--modern-text-caption);
  box-shadow: var(--modern-shadow-tooltip);
}
</style>
