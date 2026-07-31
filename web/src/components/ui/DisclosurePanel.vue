<script setup lang="ts">
defineProps<{
  summary: string
}>()
</script>

<template>
  <details class="disclosure-panel">
    <summary>{{ summary }}</summary>
    <div class="disclosure-panel__body">
      <slot />
    </div>
  </details>
</template>

<style scoped>
.disclosure-panel {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 14px;
}

.disclosure-panel summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  color: var(--color-text-muted);
  cursor: pointer;
  font-size: var(--text-sm);
  font-weight: 600;
  list-style: none;
}

.disclosure-panel summary::-webkit-details-marker {
  display: none;
}

.disclosure-panel summary::after {
  color: var(--color-text-faint);
  content: '＋';
}

.disclosure-panel[open] summary::after {
  content: '−';
}

.disclosure-panel__body {
  margin-top: 11px;
}
</style>
