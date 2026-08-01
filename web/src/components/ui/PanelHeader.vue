<script setup lang="ts">
defineProps<{
  headingId: string
  title: string
  description?: string
}>()
</script>

<template>
  <header class="panel-header">
    <div class="panel-header__copy">
      <h2 :id="headingId">{{ title }}</h2>
      <p v-if="description">{{ description }}</p>
    </div>
    <div v-if="$slots.actions" class="panel-header__actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
.panel-header {
  display: flex;
  min-height: var(--surface-header-min-height);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-5);
  margin-bottom: var(--detail-panel-header-spacing);
  border-bottom: 1px solid var(--color-border-subtle);
  padding-bottom: var(--space-4);
}

.panel-header__copy {
  min-width: 0;
}

.panel-header h2 {
  margin: 0;
  font-size: var(--title-section);
  font-weight: 650;
  letter-spacing: -0.01em;
}

.panel-header p {
  max-width: 680px;
  margin: var(--space-1) 0 0;
  color: var(--color-text-faint);
  font-size: var(--text-meta);
}

.panel-header__actions {
  display: flex;
  flex: 0 0 auto;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
}

@media (max-width: 800px) {
  .panel-header {
    align-items: stretch;
    flex-direction: column;
    margin-bottom: var(--space-4);
  }

  .panel-header__actions {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .panel-header__actions :deep(.app-button),
  .panel-header__actions :deep(.button-link) {
    min-height: var(--touch-target);
  }
}

@media (max-width: 520px) {
  .panel-header__actions {
    grid-template-columns: 1fr;
  }
}
</style>
