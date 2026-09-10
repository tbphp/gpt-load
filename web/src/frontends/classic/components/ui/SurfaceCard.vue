<script setup lang="ts">
import Surface from './Surface.vue'
</script>

<template>
  <Surface as="section" variant="raised" class="surface-card">
    <slot />
  </Surface>
</template>
