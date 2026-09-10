<script setup lang="ts">
import { TooltipContent, TooltipPortal, TooltipRoot, TooltipTrigger } from 'reka-ui'

defineProps<{ label: string; side?: 'top' | 'right' | 'bottom' | 'left' }>()
</script>

<template>
  <TooltipRoot>
    <TooltipTrigger as-child><slot /></TooltipTrigger>
    <TooltipPortal>
      <TooltipContent class="modern-tooltip" :side="side ?? 'bottom'" :side-offset="8">
        {{ label }}
      </TooltipContent>
    </TooltipPortal>
  </TooltipRoot>
</template>
