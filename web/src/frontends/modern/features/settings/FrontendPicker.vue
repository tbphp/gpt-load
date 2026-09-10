<script setup lang="ts">
import { Check } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import modernPreview from '@modern/assets/frontend-preview.jpg'
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
  <section class="modern-panel" aria-labelledby="modern-frontend-title">
    <header class="modern-panel-header">
      <h2 id="modern-frontend-title">{{ t('interfaceSettings') }}</h2>
      <p>{{ t('frontend.description') }}</p>
    </header>
    <div class="modern-panel-body modern-frontend-body">
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
          <img
            :src="frontend.id === 'modern' ? modernPreview : frontend.preview"
            alt=""
            width="320"
            height="180"
          />
          <span class="modern-frontend-option__label">
            <strong>{{ t(`frontend.${frontend.id}.title`) }}</strong>
            <span v-if="frontend.id === 'modern'" class="modern-frontend-option__current"
              ><Check :size="14" aria-hidden="true" />{{ t('frontend.current') }}</span
            >
          </span>
        </button>
      </div>
      <p v-if="failed" role="alert">{{ t('frontend.saveFailed') }}</p>
    </div>
  </section>
</template>

<style scoped>
.modern-frontend-body {
  display: grid;
  gap: 18px;
}

.modern-frontend-options {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 236px));
  gap: 16px;
}

.modern-frontend-option {
  display: flex;
  min-width: 0;
  flex-direction: column;
  align-items: flex-start;
  gap: 12px;
  border: 1px solid var(--modern-border);
  border-radius: 8px;
  background: var(--modern-surface);
  padding: 10px;
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

.modern-frontend-option__label {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 2px;
  font-size: 13px;
}

.modern-frontend-option__current {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--modern-muted);
  font-size: 11px;
}
</style>
