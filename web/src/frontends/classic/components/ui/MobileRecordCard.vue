<script setup lang="ts">
defineProps<{
  label: string
}>()
</script>

<template>
  <article class="mobile-record-card" :aria-label="label">
    <header v-if="$slots.header" class="mobile-record-card__header">
      <slot name="header" />
    </header>
    <div class="mobile-record-card__body">
      <slot />
    </div>
    <footer v-if="$slots.actions" class="mobile-record-card__actions">
      <slot name="actions" />
    </footer>
  </article>
</template>

<style scoped>
.mobile-record-card {
  display: grid;
  gap: var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: var(--space-4);
  box-shadow: var(--shadow-card);
}

.mobile-record-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}

.mobile-record-card__body {
  min-width: 0;
}

.mobile-record-card__body :deep(dl) {
  display: grid;
  grid-template-columns: minmax(7rem, auto) 1fr;
  gap: var(--space-2) var(--space-3);
  margin: 0;
}

.mobile-record-card__body :deep(dt) {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.mobile-record-card__body :deep(dd) {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.mobile-record-card__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
}
</style>
