<script setup lang="ts">
import { computed } from 'vue'
import AppTooltip from './AppTooltip.vue'
import type { SemanticTone } from './types'

const props = defineProps<{
  label: string
  segments: readonly { key: string; value: number; tone: SemanticTone }[]
}>()
const visible = computed(() => props.segments.filter((segment) => segment.value > 0))
</script>

<template>
  <AppTooltip :label="label" side="bottom">
    <div class="modern-segmented-bar" role="img" :aria-label="label" tabindex="0">
      <span
        v-for="segment in visible"
        :key="segment.key"
        class="modern-segmented-bar-part"
        :class="`modern-segmented-bar--${segment.tone}`"
        :style="{ flexGrow: segment.value }"
        aria-hidden="true"
      />
    </div>
  </AppTooltip>
</template>

<style scoped>
.modern-segmented-bar {
  display: flex;
  width: 100%;
  height: var(--modern-status-bar-height);
  overflow: hidden;
  border-radius: var(--modern-radius-small);
  background: var(--modern-progress-track);
  box-shadow: inset 0 0 0 var(--modern-line-width) var(--modern-progress-track-border);
  gap: var(--modern-space-0-5);
}
.modern-segmented-bar-part {
  min-width: var(--modern-space-0-5);
  flex-basis: 0;
  background: var(--modern-status-neutral);
}
.modern-segmented-bar--success {
  background: var(--modern-status-success);
}
.modern-segmented-bar--warning {
  background: var(--modern-status-warning);
}
.modern-segmented-bar--danger {
  background: var(--modern-status-danger);
}
.modern-segmented-bar--info {
  background: var(--modern-info);
}
</style>
