<script setup lang="ts">
import { AppButton } from '@modern/components/ui'

defineProps<{ label: string }>()
defineEmits<{ click: [] }>()
</script>

<template>
  <AppButton
    variant="text"
    class="modern-log-filter-link"
    :aria-label="label"
    @click="$emit('click')"
  >
    <slot />
  </AppButton>
</template>

<style scoped>
.modern-log-filter-link {
  min-width: 0;
  min-height: 0;
  max-width: 100%;
  color: inherit;
  text-decoration: none;
}
.modern-log-filter-link :deep(*) {
  text-decoration: none;
}
.modern-log-filter-link:hover,
.modern-log-filter-link:focus-visible,
.modern-log-filter-link:hover :deep(*),
.modern-log-filter-link:focus-visible :deep(*) {
  text-decoration: underline;
  text-decoration-color: currentColor;
  text-decoration-thickness: var(--modern-line-width);
  text-underline-offset: var(--modern-space-1);
}
</style>
