<script setup lang="ts">
import PageHeader from '@modern/components/PageHeader.vue'

defineProps<{ title: string; description?: string }>()
</script>

<template>
  <section class="modern-auth-card">
    <PageHeader :title="title" :description="description" />
    <slot />
  </section>
</template>

<style scoped>
.modern-auth-card {
  display: grid;
  width: min(100%, 440px);
  gap: var(--modern-space-6);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  padding: var(--modern-space-8);
}
@media (max-width: 760px) {
  .modern-auth-card {
    padding: var(--modern-space-6);
  }
}
</style>
