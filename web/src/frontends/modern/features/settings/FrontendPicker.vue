<script setup lang="ts">
import { Check } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import modernPreview from '@modern/assets/frontend-preview.jpg'
import AppIcon from '@modern/components/ui/AppIcon.vue'
import AppNotice from '@modern/components/ui/AppNotice.vue'
import AppPanel from '@modern/components/ui/AppPanel.vue'
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
  <AppPanel :title="t('interfaceSettings')" :description="t('frontend.description')">
    <div class="modern-frontend-body">
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
              ><AppIcon :icon="Check" size="sm" />{{ t('frontend.current') }}</span
            >
          </span>
        </button>
      </div>
      <AppNotice v-if="failed" tone="danger">{{ t('frontend.saveFailed') }}</AppNotice>
    </div>
  </AppPanel>
</template>

<style scoped>
.modern-frontend-body {
  display: grid;
  gap: var(--modern-space-4);
}

.modern-frontend-options {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 236px));
  gap: var(--modern-space-4);
}

.modern-frontend-option {
  display: flex;
  min-width: 0;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  padding: var(--modern-space-2);
  text-align: left;
  cursor: pointer;
}

.modern-frontend-option[aria-pressed='true'] {
  border-color: var(--modern-accent);
  outline: var(--modern-line-width) solid var(--modern-accent);
}

.modern-frontend-option img {
  width: 100%;
  height: auto;
  border-radius: var(--modern-radius-small);
}

.modern-frontend-option__label {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  padding: 0 var(--modern-space-0-5);
  font-size: var(--modern-font-size-secondary);
}

.modern-frontend-option__current {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
</style>
