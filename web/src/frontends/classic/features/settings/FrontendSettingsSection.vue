<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { frontendOptions } from '@shared/frontend/catalog'
import { switchFrontend, type FrontendID } from '@shared/frontend/preference'

const props = defineProps<{ disabled: boolean }>()
const { t } = useI18n()
const pending = ref(false)
const failed = ref(false)

function select(frontend: FrontendID): void {
  if (frontend === 'classic' || props.disabled || pending.value) return
  failed.value = false
  pending.value = true
  try {
    switchFrontend(frontend)
  } catch {
    pending.value = false
    failed.value = true
  }
}
</script>

<template>
  <section id="settings-interface" class="settings-section" aria-labelledby="frontend-title">
    <h2 id="frontend-title">{{ t('settings.frontend.title') }}</h2>
    <p>{{ t('settings.frontend.description') }}</p>
    <div class="frontend-options">
      <button
        v-for="frontend in frontendOptions"
        :key="frontend.id"
        type="button"
        class="frontend-option"
        :aria-pressed="frontend.id === 'classic'"
        :disabled="disabled || pending"
        @click="select(frontend.id)"
      >
        <img :src="frontend.preview" alt="" width="320" height="180" />
        <strong>{{ t(`settings.frontend.${frontend.id}.title`) }}</strong>
        <span>{{ t(`settings.frontend.${frontend.id}.description`) }}</span>
        <span v-if="frontend.id === 'classic'">{{ t('settings.frontend.current') }}</span>
      </button>
    </div>
    <p>{{ t('settings.frontend.previewNote') }}</p>
    <p v-if="disabled" role="status">{{ t('settings.frontend.blocked') }}</p>
    <p v-if="failed" role="alert">{{ t('settings.frontend.saveFailed') }}</p>
  </section>
</template>

<style scoped>
.settings-section {
  display: grid;
  gap: var(--space-3);
}

h2,
p {
  margin: 0;
}

h2 {
  font-size: var(--title-section);
  font-weight: 650;
}

p,
.frontend-option span {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.frontend-options {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 260px));
  gap: var(--space-3);
}

.frontend-option {
  display: flex;
  min-width: 0;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-2);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: var(--space-3);
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.frontend-option[aria-pressed='true'] {
  border-color: var(--color-action);
  outline: 1px solid var(--color-action);
}

.frontend-option:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.frontend-option img {
  width: 100%;
  height: auto;
  border-radius: var(--radius-control);
}
</style>
