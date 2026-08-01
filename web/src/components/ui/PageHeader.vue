<script setup lang="ts">
defineProps<{
  id?: string
  title: string
  description?: string
  eyebrow?: string
}>()
</script>

<template>
  <header class="page-header">
    <div>
      <p v-if="eyebrow" class="eyebrow">{{ eyebrow }}</p>
      <h1 :id="id" tabindex="-1">{{ title }}</h1>
      <p v-if="description" class="page-header__description">{{ description }}</p>
    </div>
    <div v-if="$slots.actions" class="page-header__actions"><slot name="actions" /></div>
  </header>
</template>

<style scoped>
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-6);
}

.page-header h1 {
  max-width: none;
  font-family: var(--font-serif);
  font-size: var(--title-lede);
  font-weight: 500;
  letter-spacing: -0.02em;
  line-height: var(--line-compact);
}

.page-header__description {
  max-width: 68ch;
  margin: var(--space-3) 0 0;
  color: var(--color-text-muted);
}

.page-header__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}

@media (max-width: 680px) {
  .page-header {
    flex-direction: column;
    gap: 15px;
  }
}

@media (max-width: 560px) {
  .page-header h1 {
    font-size: 24px;
  }
}
</style>
