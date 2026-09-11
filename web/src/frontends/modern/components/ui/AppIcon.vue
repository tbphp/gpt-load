<script setup lang="ts">
import type { Component } from 'vue'

withDefaults(defineProps<{ icon: Component; size?: 'xs' | 'sm' | 'md' | 'lg' }>(), { size: 'md' })
</script>

<template>
  <component
    :is="icon"
    class="modern-icon"
    :class="`modern-icon--${size}`"
    aria-hidden="true"
    focusable="false"
  />
</template>

<style scoped>
.modern-icon {
  width: var(--modern-icon-md);
  height: var(--modern-icon-md);
  flex-shrink: 0;
  stroke-width: var(--modern-icon-stroke);
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
</style>
