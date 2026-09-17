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
    <h3>{{ t('group.credentials.modelCooldown.label') }}</h3>
    <dl class="model-cooldowns__list">
      <div v-for="cooldown in cooldowns" :key="cooldown.model" class="model-cooldowns__tag">
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
.model-cooldowns h3 {
  margin: 0;
  color: var(--color-text-muted);
  font: inherit;
  font-weight: 680;
  letter-spacing: 0.06em;
}
.model-cooldowns__list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 8px;
  min-width: 0;
  margin: 0;
}
.model-cooldowns__tag {
  display: inline-flex;
  max-width: 100%;
  align-items: baseline;
  gap: 6px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  background: var(--color-surface);
  padding: 5px 9px;
}
.model-cooldowns dt {
  min-width: 0;
  color: var(--color-text);
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}
.model-cooldowns dt::after {
  content: ':';
}
.model-cooldowns dd {
  flex: none;
  margin: 0;
  color: var(--color-text-muted);
  white-space: nowrap;
}
</style>
