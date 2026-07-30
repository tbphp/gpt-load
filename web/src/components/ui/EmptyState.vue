<script setup lang="ts">
withDefaults(
  defineProps<{
    title: string
    description: string
    headingAs?: 'h1' | 'h2' | 'h3' | 'h4' | 'p'
  }>(),
  {
    headingAs: 'h2',
  },
)
</script>

<template>
  <div class="empty-state">
    <div class="empty-state__icon" aria-hidden="true"><slot name="icon" /></div>
    <component :is="headingAs" class="empty-state__title">{{ title }}</component>
    <p>{{ description }}</p>
    <div v-if="$slots.actions" class="empty-state__actions"><slot name="actions" /></div>
  </div>
</template>

<style scoped>
.empty-state {
  display: grid;
  min-height: 240px;
  place-items: center;
  align-content: center;
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--radius-card);
  background: var(--color-surface-sunken);
  padding: var(--space-8);
  text-align: center;
}
.empty-state__icon {
  color: var(--color-text-faint);
}
.empty-state__title {
  margin: var(--space-3) 0 0;
  font-size: 1.125rem;
  font-weight: 650;
}
.empty-state p {
  max-width: 54ch;
  margin: var(--space-2) 0 0;
  color: var(--color-text-muted);
}
.empty-state__actions {
  margin-top: var(--space-5);
}
</style>
