<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { ModelCooldownDto } from '@/api/control/types'
import AppRelativeTime from './AppRelativeTime.vue'

defineProps<{ cooldowns: ModelCooldownDto[] }>()
const { locale, t } = useI18n()
</script>

<template>
  <dl
    v-if="cooldowns.length > 0"
    class="model-cooldowns"
    :aria-label="t('group.credentials.modelCooldown.label')"
  >
    <div v-for="cooldown in cooldowns" :key="cooldown.model">
      <dt>{{ cooldown.model }}</dt>
      <dd>
        <span>{{ t('group.credentials.modelCooldown.until') }}</span>
        <AppRelativeTime
          :instant="cooldown.cooldown_until_ms"
          :locale="locale"
          :empty-label="t('group.credentials.none')"
          hint
        />
      </dd>
    </div>
  </dl>
</template>

<style scoped>
.model-cooldowns {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  min-width: 0;
  font-size: var(--text-label-xs);
}
.model-cooldowns > div,
.model-cooldowns dd {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-2);
}
.model-cooldowns > div {
  justify-content: space-between;
}
.model-cooldowns dt {
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}
.model-cooldowns dd {
  margin: 0;
  color: var(--color-text-muted);
}
</style>
