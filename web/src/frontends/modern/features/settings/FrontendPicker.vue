<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import modernPreview from '@modern/assets/frontend-preview.jpg'
import { AppChoiceCard, AppNotice, AppPanel } from '@modern/components/ui'
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
        <AppChoiceCard
          v-for="frontend in frontendOptions"
          :key="frontend.id"
          :label="t(`frontend.${frontend.id}.title`)"
          :image="frontend.id === 'modern' ? modernPreview : frontend.preview"
          :selected="frontend.id === 'modern'"
          :selected-label="t('frontend.current')"
          :disabled="pending"
          @click="select(frontend.id)"
        />
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
</style>
