<script setup lang="ts">
import { ChevronLeft, ChevronRight } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import ModelDraftEditor from '@/components/config/ModelDraftEditor.vue'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import type { ImportDraft } from './model-draft'

defineProps<{
  discoveryFailed: boolean
  manualMode: boolean
  errorKey: string
  models: ImportDraft['models']
  canReview: boolean
}>()
const emit = defineEmits<{
  manual: []
  'update:models': [models: ImportDraft['models']]
  back: []
  review: []
}>()
const { t } = useI18n()
const heading = ref<HTMLHeadingElement>()

function focusHeading(): void {
  heading.value?.focus()
}

defineExpose({ focusHeading })
</script>

<template>
  <SurfaceCard class="import-card">
    <header>
      <h2 ref="heading" data-test="import-step-2-heading" tabindex="-1">
        {{ t('import.models.title') }}
      </h2>
      <p>{{ t('import.models.stepDescription') }}</p>
    </header>
    <InlineFeedback v-if="errorKey" tone="warning">{{ t(errorKey) }}</InlineFeedback>
    <button
      v-if="discoveryFailed && !manualMode"
      data-test="manual-path"
      class="manual-path"
      type="button"
      @click="emit('manual')"
    >
      {{ t('import.models.manualPath') }}
    </button>
    <ModelDraftEditor
      v-if="manualMode"
      :model-value="models"
      @update:model-value="emit('update:models', $event)"
    />
    <footer class="card-actions split">
      <AppButton variant="secondary" @click="emit('back')">
        <ChevronLeft :size="16" aria-hidden="true" />{{ t('import.back') }}
      </AppButton>
      <AppButton data-test="review" :disabled="!canReview" @click="emit('review')">
        {{ t('import.review') }}<ChevronRight :size="16" aria-hidden="true" />
      </AppButton>
    </footer>
  </SurfaceCard>
</template>

<style scoped>
.import-card {
  display: grid;
  gap: var(--space-5);
  padding: var(--space-6);
}
header h2,
header p {
  margin: 0;
}
header h2 {
  font-size: 1.2rem;
}
header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.card-actions {
  display: flex;
  justify-content: flex-end;
}
.card-actions.split {
  justify-content: space-between;
}
.card-actions :deep(.app-button) {
  gap: var(--space-2);
}
.manual-path {
  min-height: 44px;
  border: 1px solid var(--color-action);
  border-radius: var(--radius-control);
  background: var(--color-action-soft);
  color: var(--color-action);
  padding: var(--space-2) var(--space-4);
  font-weight: 650;
  cursor: pointer;
}
</style>
