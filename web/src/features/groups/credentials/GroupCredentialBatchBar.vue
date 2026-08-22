<script setup lang="ts">
import { CircleCheck, CircleOff, Download, ListChecks, RefreshCw, Trash2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'

defineProps<{
  selectedCount: number
  allVisibleSelected: boolean
  pending?: boolean
  canSync?: boolean
  canDownload?: boolean
}>()
const emit = defineEmits<{
  'toggle-select': []
  enable: []
  disable: []
  sync: []
  download: []
  remove: []
}>()
const { n, t } = useI18n()
</script>

<template>
  <div class="group-credential-batch" role="status">
    <div class="group-credential-batch__copy">
      <i18n-t keypath="group.credentials.batch.selected">
        <template #count
          ><strong>{{ n(selectedCount) }}</strong></template
        >
      </i18n-t>
    </div>
    <div class="group-credential-batch__actions">
      <AppButton
        variant="secondary"
        tone="action"
        size="compact"
        :busy="pending"
        @click="emit('toggle-select')"
      >
        <ListChecks :size="15" aria-hidden="true" />
        {{
          allVisibleSelected
            ? t('group.credentials.batch.clearAll')
            : t('group.credentials.batch.selectAll')
        }}
      </AppButton>
      <AppButton
        variant="secondary"
        tone="success"
        size="compact"
        :busy="pending"
        @click="emit('enable')"
      >
        <CircleCheck :size="15" aria-hidden="true" />
        {{ t('group.credentials.batch.enable') }}
      </AppButton>
      <AppButton
        variant="secondary"
        tone="warning"
        size="compact"
        :busy="pending"
        @click="emit('disable')"
      >
        <CircleOff :size="15" aria-hidden="true" />
        {{ t('group.credentials.batch.disable') }}
      </AppButton>
      <AppButton
        variant="secondary"
        tone="danger"
        size="compact"
        :busy="pending"
        @click="emit('remove')"
      >
        <Trash2 :size="15" aria-hidden="true" />
        {{ t('group.credentials.batch.delete') }}
      </AppButton>
      <AppButton
        v-if="canSync"
        variant="secondary"
        tone="action"
        size="compact"
        :busy="pending"
        @click="emit('sync')"
      >
        <RefreshCw :size="15" aria-hidden="true" />
        {{ t('group.credentials.batch.sync') }}
      </AppButton>
      <AppButton
        v-if="canDownload"
        variant="secondary"
        tone="action"
        size="compact"
        :busy="pending"
        @click="emit('download')"
      >
        <Download :size="15" aria-hidden="true" />
        {{ t('group.credentials.batch.download') }}
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.group-credential-batch {
  display: flex;
  min-height: var(--touch-target);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: 1px solid var(--color-border-control);
  margin-bottom: 10px;
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 7px 10px 7px 13px;
}
.group-credential-batch__copy {
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}
.group-credential-batch__copy strong {
  color: var(--color-text);
  font-family: var(--font-mono);
}
.group-credential-batch__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
@media (max-width: 800px) {
  .group-credential-batch {
    align-items: stretch;
    flex-direction: column;
  }
  .group-credential-batch__actions {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .group-credential-batch__actions :deep(.app-button) {
    min-height: var(--touch-target);
  }
}
</style>
