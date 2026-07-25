<script setup lang="ts">
defineProps<{ caption: string; dense?: boolean }>()
</script>

<template>
  <div class="data-table__container" :class="{ 'data-table__container--dense': dense }">
    <table class="data-table">
      <caption class="sr-only">
        {{
          caption
        }}
      </caption>
      <slot />
    </table>
  </div>
</template>

<style scoped>
.data-table__container {
  max-width: 100%;
  overflow-x: auto;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  box-shadow: var(--shadow-card);
  overscroll-behavior-inline: contain;
}
.data-table {
  width: 100%;
  min-width: 920px;
  border-collapse: collapse;
  font-size: 0.8125rem;
}
.data-table__container--dense .data-table {
  min-width: 780px;
}
.data-table :deep(th) {
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface-secondary);
  color: var(--color-text-muted);
  padding: 9px 12px;
  font-size: 0.6875rem;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-align: left;
  text-transform: uppercase;
  white-space: nowrap;
}
.data-table :deep(td) {
  border-bottom: 1px solid var(--color-border);
  padding: 8px 12px;
  vertical-align: middle;
}
.data-table :deep(tbody tr:last-child td) {
  border-bottom: 0;
}
.data-table :deep(th:first-child),
.data-table :deep(td:first-child) {
  padding-left: var(--space-4);
}
.data-table :deep(th:last-child),
.data-table :deep(td:last-child) {
  padding-right: var(--space-4);
}
</style>
