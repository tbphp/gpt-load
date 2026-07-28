<script setup lang="ts">
import { ChevronLeft, TriangleAlert } from 'lucide-vue-next'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { UpstreamUrlConflictData } from '@/app/resources/groups'
import type { GroupProtocol } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import type { ImportDraft } from './model-draft'
import { toGroupModels } from './model-draft'

defineProps<{
  name: string
  upstreamUrl: string
  protocols: GroupProtocol[]
  keyCount: number
  models: ImportDraft['models']
  errorKey: string
  conflict: UpstreamUrlConflictData | null
  pending: boolean
  operationNoticeActive: boolean
}>()
const emit = defineEmits<{
  append: [groupID: number]
  separate: []
  edit: []
  back: []
  create: []
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
      <h2 ref="heading" data-test="import-step-3-heading" tabindex="-1">
        {{ t('import.reviewTitle') }}
      </h2>
      <p>{{ t('import.reviewDescription') }}</p>
    </header>
    <dl class="review-list">
      <div>
        <dt>{{ t('import.connection.name') }}</dt>
        <dd>{{ name || t('import.automaticName') }}</dd>
      </div>
      <div>
        <dt>{{ t('import.connection.url') }}</dt>
        <dd>
          <code>{{ upstreamUrl }}</code>
        </dd>
      </div>
      <div>
        <dt>{{ t('import.connection.protocols') }}</dt>
        <dd>{{ protocols.join(', ') }}</dd>
      </div>
      <div>
        <dt>{{ t('import.keys.label') }}</dt>
        <dd>{{ t('import.keys.count', { count: keyCount }) }}</dd>
      </div>
      <div>
        <dt>{{ t('import.models.title') }}</dt>
        <dd>
          {{
            toGroupModels(models)
              .map((model) => model.id)
              .join(', ')
          }}
        </dd>
      </div>
    </dl>
    <InlineFeedback v-if="errorKey" tone="danger">{{ t(errorKey) }}</InlineFeedback>
    <section v-if="conflict" class="conflict" aria-live="polite">
      <h3><TriangleAlert :size="18" aria-hidden="true" />{{ t('import.conflict.title') }}</h3>
      <p>{{ t('import.conflict.description') }}</p>
      <div v-for="group in conflict.groups" :key="group.id" class="conflict-group">
        <strong>{{ group.name }}</strong>
        <AppButton
          :data-test="`conflict-append-${group.id}`"
          variant="secondary"
          @click="emit('append', group.id)"
        >
          {{ t('import.conflict.append') }}
        </AppButton>
      </div>
      <div class="conflict-actions">
        <AppButton data-test="conflict-confirm-separate" @click="emit('separate')">
          {{ t('import.conflict.separate') }}
        </AppButton>
        <AppButton
          data-test="conflict-edit"
          variant="ghost"
          :disabled="pending"
          @click="emit('edit')"
        >
          {{ t('import.conflict.edit') }}
        </AppButton>
      </div>
    </section>
    <footer v-else class="card-actions split">
      <AppButton
        variant="secondary"
        :disabled="pending || operationNoticeActive"
        @click="emit('back')"
      >
        <ChevronLeft :size="16" aria-hidden="true" />{{ t('import.back') }}
      </AppButton>
      <AppButton
        data-test="create"
        :disabled="operationNoticeActive"
        :busy="pending"
        @click="emit('create')"
      >
        {{ t('import.create') }}
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
.review-list {
  display: grid;
  gap: 0;
  margin: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
}
.review-list div {
  display: grid;
  grid-template-columns: 180px 1fr;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
}
.review-list div:last-child {
  border-bottom: 0;
}
dt {
  color: var(--color-text-muted);
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.conflict {
  display: grid;
  gap: var(--space-3);
  border: 1px solid color-mix(in srgb, var(--color-warning) 38%, var(--color-border));
  border-radius: var(--radius-card);
  background: var(--color-warning-bg);
  padding: var(--space-4);
}
.conflict h3,
.conflict p {
  margin: 0;
}
.conflict h3 {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-warning);
}
.conflict-group,
.conflict-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}
.conflict-actions {
  justify-content: flex-start;
  flex-wrap: wrap;
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
@media (max-width: 640px) {
  .review-list div {
    grid-template-columns: 1fr;
    gap: var(--space-1);
  }
  .conflict-group {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
