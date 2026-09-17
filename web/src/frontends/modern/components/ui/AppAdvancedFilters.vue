<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{ open: boolean }>()
const mounted = ref(props.open)
watch(
  () => props.open,
  (open) => {
    if (open) mounted.value = true
  },
)
</script>

<template>
  <div v-if="mounted" v-show="open" class="modern-advanced-filters">
    <slot />
  </div>
</template>

<style scoped>
.modern-advanced-filters {
  display: grid;
  flex: none;
  margin-top: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  padding-inline: var(--modern-space-3);
  /* 仅作小屏兜底，正常内容不产生内层滚动。 */
  max-height: 60dvh;
  overflow-y: auto;
  overscroll-behavior: contain;
}
</style>
