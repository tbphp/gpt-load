<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { frontendOptions } from '@shared/frontend/catalog'
import { switchFrontend, type FrontendID } from '@shared/frontend/preference'

const { t } = useI18n()
const pending = ref(false)
const failed = ref(false)

function select(frontend: FrontendID): void {
  if (frontend === 'modern' || pending.value) return
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
  <section class="modern-page" aria-labelledby="modern-frontend-title">
    <h2 id="modern-frontend-title">{{ t('interfaceSettings') }}</h2>
    <p class="modern-description">{{ t('frontend.description') }}</p>
    <div class="modern-frontend-options">
      <button
        v-for="frontend in frontendOptions"
        :key="frontend.id"
        type="button"
        class="modern-frontend-option"
        :aria-pressed="frontend.id === 'modern'"
        :disabled="pending"
        @click="select(frontend.id)"
      >
        <img :src="frontend.preview" alt="" width="320" height="180" />
        <strong>{{ t(`frontend.${frontend.id}.title`) }}</strong>
        <span>{{ t(`frontend.${frontend.id}.description`) }}</span>
        <span v-if="frontend.id === 'modern'">{{ t('frontend.current') }}</span>
      </button>
    </div>
    <p class="modern-description">{{ t('frontend.previewNote') }}</p>
    <p v-if="failed" role="alert">{{ t('frontend.saveFailed') }}</p>
  </section>
</template>

<style scoped>
.modern-frontend-options {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 300px));
  gap: 16px;
}

.modern-frontend-option {
  display: flex;
  min-width: 0;
  flex-direction: column;
  align-items: flex-start;
  gap: 10px;
  border: 1px solid var(--modern-border);
  border-radius: 8px;
  background: var(--modern-surface);
  padding: 14px;
  text-align: left;
  cursor: pointer;
}

.modern-frontend-option[aria-pressed='true'] {
  border-color: var(--modern-accent);
  outline: 1px solid var(--modern-accent);
}

.modern-frontend-option img {
  width: 100%;
  height: auto;
  border-radius: 4px;
}

.modern-frontend-option span {
  color: var(--modern-muted);
  font-size: 14px;
}
</style>
