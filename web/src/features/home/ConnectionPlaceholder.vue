<script setup lang="ts">
import { Cable } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <section class="connection-placeholder" aria-labelledby="connection-placeholder-title">
    <header class="connection-placeholder__header">
      <div class="connection-placeholder__title">
        <Cable :size="22" aria-hidden="true" />
        <h2 id="connection-placeholder-title">{{ t('home.connectionPlaceholder.title') }}</h2>
      </div>
      <span class="connection-placeholder__badge">
        {{ t('home.connectionPlaceholder.badge') }}
      </span>
    </header>
    <div class="connection-placeholder__surface">
      <p>{{ t('home.connectionPlaceholder.description') }}</p>
    </div>
  </section>
</template>

<style scoped>
.connection-placeholder {
  display: grid;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-strong);
  padding-top: var(--space-5);
}
.connection-placeholder__header {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-5);
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
  color: var(--color-text-muted);
  text-align: center;
}
.connection-placeholder__badge {
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--radius-tag);
  color: var(--color-text-faint);
  padding: var(--space-1) var(--space-2);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}
.connection-placeholder__surface {
  display: grid;
  min-height: 116px;
  place-items: center;
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--radius-card);
  background: var(--color-surface-sunken);
  padding: var(--space-6);
}
@media (max-width: 759px) {
  .connection-placeholder__header {
    align-items: flex-start;
    flex-direction: column;
    gap: var(--space-2);
  }
  .connection-placeholder__badge {
    justify-self: start;
  }
  .connection-placeholder__surface {
    min-height: 96px;
    padding: var(--space-5);
  }
}
</style>
