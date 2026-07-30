<script setup lang="ts">
import { Cable } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <section
    class="connection-placeholder"
    data-test="home-connection-placeholder"
    aria-labelledby="connection-placeholder-title"
  >
    <div class="connection-placeholder__title">
      <Cable :size="22" aria-hidden="true" />
      <h2 id="connection-placeholder-title">{{ t('home.connectionPlaceholder.title') }}</h2>
    </div>
    <span class="connection-placeholder__badge">
      {{ t('home.connectionPlaceholder.badge') }}
    </span>
    <p>{{ t('home.connectionPlaceholder.description') }}</p>
  </section>
</template>

<style scoped>
.connection-placeholder {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-3) var(--space-5);
  border-top: 1px solid var(--color-border-strong);
  border-bottom: 1px solid var(--color-border-strong);
  padding: var(--space-5) 0;
}
.connection-placeholder__title {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}
.connection-placeholder__title svg {
  color: var(--color-action);
}
.connection-placeholder h2,
.connection-placeholder p {
  margin: 0;
}
.connection-placeholder h2 {
  font-family: var(--font-serif);
  font-size: 1.35rem;
  font-weight: 500;
}
.connection-placeholder p {
  grid-column: 1 / -1;
  color: var(--color-text-muted);
}
.connection-placeholder__badge {
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--radius-tag);
  color: var(--color-text-faint);
  padding: var(--space-1) var(--space-2);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}
@media (max-width: 759px) {
  .connection-placeholder {
    grid-template-columns: minmax(0, 1fr);
  }
  .connection-placeholder__badge {
    justify-self: start;
  }
}
</style>
