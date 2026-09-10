<script setup lang="ts">
withDefaults(
  defineProps<{
    title: string
    description: string
    headingAs?: 'h1' | 'h2' | 'h3' | 'h4' | 'p'
    variant?: 'panel' | 'ledger'
  }>(),
  {
    headingAs: 'h2',
    variant: 'panel',
  },
)
</script>

<template>
  <div class="empty-state" :class="`empty-state--${variant}`">
    <div class="empty-state__inner">
      <div class="empty-state__icon" aria-hidden="true"><slot name="icon" /></div>
      <component :is="headingAs" class="empty-state__title">{{ title }}</component>
      <p>{{ description }}</p>
      <div v-if="$slots.actions" class="empty-state__actions"><slot name="actions" /></div>
    </div>
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
.empty-state__inner {
  display: grid;
  max-width: 100%;
  justify-items: center;
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
.empty-state--ledger {
  min-height: 260px;
  border: 0;
  border-bottom: 1px solid var(--color-border-control);
  border-radius: 0;
  background: transparent;
  padding: 36px var(--space-5);
}
.empty-state--ledger .empty-state__inner {
  width: 100%;
  max-width: 420px;
  gap: var(--space-2);
}
.empty-state--ledger .empty-state__icon {
  display: grid;
  width: var(--control-lg);
  height: var(--control-lg);
  place-items: center;
  border: 1px solid var(--color-border-subtle);
  border-radius: calc(var(--radius-card) - 1px);
  background: var(--color-surface-sunken);
}
.empty-state--ledger .empty-state__title {
  margin: 5px 0 0;
  font-size: var(--title-empty);
}
.empty-state--ledger p {
  margin: 0 0 7px;
  font-size: var(--text-meta);
}
.empty-state--ledger .empty-state__actions {
  margin-top: 0;
}
</style>
