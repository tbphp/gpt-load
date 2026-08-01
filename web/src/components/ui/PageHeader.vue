<script setup lang="ts">
defineProps<{
  id?: string
  title: string
}>()
</script>

<template>
  <header class="page-header">
    <h1 :id="id" tabindex="-1">{{ title }}</h1>
    <div v-if="$slots.actions" class="page-header__actions"><slot name="actions" /></div>
  </header>
</template>

<style scoped>
.page-header {
  display: flex;
  min-height: 64px;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-6);
  border-bottom: 1px solid var(--color-border-control);
  padding-bottom: var(--space-5);
}

.page-header h1 {
  max-width: none;
  font-family: var(--font-serif);
  font-size: var(--title-lede);
  font-weight: 500;
  letter-spacing: -0.02em;
  line-height: var(--line-compact);
}

.page-header__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

@media (max-width: 560px) {
  .page-header h1 {
    font-size: 24px;
  }
}
</style>
