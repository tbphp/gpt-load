<script setup lang="ts">
import { useForwardExpose } from 'reka-ui'
import type { Component } from 'vue'
import AppTooltip from './AppTooltip.vue'
import type { ControlSize } from './types'

defineOptions({ inheritAttrs: false })
withDefaults(
  defineProps<{ icon: Component; size?: ControlSize | 'lg' | 'inherit'; label?: string }>(),
  { size: 'md', label: undefined },
)
const { forwardRef } = useForwardExpose()
</script>

<template>
  <AppTooltip :label="label">
    <component
      :is="icon"
      :ref="forwardRef"
      v-bind="$attrs"
      class="modern-icon"
      :class="'modern-icon--' + size"
      aria-hidden="true"
      focusable="false"
    />
  </AppTooltip>
</template>

<style scoped>
.modern-icon {
  width: var(--modern-icon-md);
  height: var(--modern-icon-md);
  flex-shrink: 0;
  stroke-width: var(--modern-icon-stroke);
  vertical-align: middle;
}
.modern-icon--xs {
  width: var(--modern-icon-xs);
  height: var(--modern-icon-xs);
}
.modern-icon--sm {
  width: var(--modern-icon-sm);
  height: var(--modern-icon-sm);
}
.modern-icon--lg {
  width: var(--modern-icon-lg);
  height: var(--modern-icon-lg);
}
.modern-icon--inherit {
  width: 1em;
  height: 1em;
}
</style>
