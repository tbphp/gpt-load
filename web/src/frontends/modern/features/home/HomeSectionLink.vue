<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import { AppButton, AppIcon } from '@modern/components/ui'

defineProps<{ to: RouteLocationRaw; label: string }>()
</script>

<template>
  <AppButton as-child variant="text" size="xs" class="modern-home-section-link">
    <RouterLink :to="to"> {{ label }}<AppIcon :icon="ArrowRight" size="sm" /> </RouterLink>
  </AppButton>
</template>

<style scoped>
.modern-home-section-link {
  color: var(--modern-action);
  white-space: nowrap;
}
.modern-home-section-link:hover:not(:disabled, [aria-disabled='true']) {
  color: var(--modern-action-hover);
}
</style>
