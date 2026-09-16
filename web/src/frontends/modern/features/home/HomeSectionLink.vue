<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import { AppButton, AppIcon } from '@modern/components/ui'

withDefaults(defineProps<{ to: RouteLocationRaw; label: string; arrow?: boolean }>(), {
  arrow: true,
})
</script>

<template>
  <AppButton as-child variant="text" size="xs" class="modern-home-section-link">
    <RouterLink :to="to">
      {{ label }}<AppIcon v-if="arrow" :icon="ArrowRight" size="sm" />
    </RouterLink>
  </AppButton>
</template>

<style scoped>
.modern-home-section-link {
  color: var(--modern-accent);
  white-space: nowrap;
}
</style>
