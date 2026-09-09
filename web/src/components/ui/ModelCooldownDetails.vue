<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { ModelCooldownDto } from '@/api/control/types'
import AppRelativeTime from './AppRelativeTime.vue'

defineProps<{ cooldowns: ModelCooldownDto[] }>()
const { locale, t } = useI18n()
</script>

<template>
  <section
    v-if="cooldowns.length > 0"
    class="model-cooldowns"
    :aria-label="t('group.credentials.modelCooldown.label')"
  >
    <header class="model-cooldowns__header">
      <h3>{{ t('group.credentials.modelCooldown.label') }}</h3>
      <span>{{ t('group.credentials.modelCooldown.until') }}</span>
    </header>
    <dl class="model-cooldowns__list">
      <div v-for="cooldown in cooldowns" :key="cooldown.model" class="model-cooldowns__row">
        <dt>{{ cooldown.model }}</dt>
        <dd>
          <AppRelativeTime
            :instant="cooldown.cooldown_until_ms"
            :locale="locale"
            :empty-label="t('group.credentials.none')"
            hint
          />
        </dd>
      </div>
    </dl>
  </section>
</template>

<style scoped>
.model-cooldowns {
  display: grid;
  gap: 6px;
  min-width: 0;
  font-size: var(--text-label-xs);
}
.model-cooldowns__header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-3);
  color: var(--color-text-muted);
}
.model-cooldowns__header h3 {
  margin: 0;
  font: inherit;
  font-weight: 680;
  letter-spacing: 0.06em;
}
.model-cooldowns__header > span {
  flex: none;
  padding-right: 12px;
  font-weight: 400;
}
.model-cooldowns__list {
  min-width: 0;
  margin: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
.model-cooldowns__row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) max-content;
  align-items: baseline;
  gap: var(--space-4);
  padding: 9px 12px;
}
.model-cooldowns__row + .model-cooldowns__row {
  border-top: 1px solid var(--color-border-subtle);
}
.model-cooldowns dt {
  min-width: 0;
  color: var(--color-text);
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}
.model-cooldowns dd {
  margin: 0;
  color: var(--color-text-muted);
  text-align: right;
  white-space: nowrap;
}
</style>
