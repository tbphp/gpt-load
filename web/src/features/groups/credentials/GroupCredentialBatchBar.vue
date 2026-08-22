<script setup lang="ts">
import { CircleCheck, CircleOff, Download, ListChecks, RefreshCw, Trash2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'

defineProps<{
  selectedCount: number
  allVisibleSelected: boolean
  canSelectAll: boolean
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
    <div class="group-credential-batch__actions">
      <AppButton
        variant="secondary"
        tone="action"
        size="compact"
        :busy="pending"
        :disabled="!canSelectAll"
        @click="emit('toggle-select')"
      >
        <ListChecks :size="15" aria-hidden="true" />
        {{
          allVisibleSelected
            ? t('group.credentials.batch.clearAll')
            : t('group.credentials.batch.selectAll')
        }}
        <span v-if="selectedCount > 0" class="group-credential-batch__count">{{
          n(selectedCount)
        }}</span>
      </AppButton>
      <AppButton
        variant="secondary"
        tone="success"
        size="compact"
        :busy="pending"
        :disabled="selectedCount === 0"
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
        :disabled="selectedCount === 0"
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
        :disabled="selectedCount === 0"
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
        :disabled="selectedCount === 0"
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
        :disabled="selectedCount === 0"
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
  justify-self: end;
  width: max-content;
  max-width: 100%;
  min-width: 0;
  border: 0;
  background: transparent;
  padding: 0;
}
.group-credential-batch__count {
  display: inline-flex;
  min-width: 1.25em;
  height: 1.25em;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: color-mix(in srgb, currentColor 16%, transparent);
  padding: 0 4px;
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-weight: 650;
}
.group-credential-batch__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
}
@media (max-width: 800px) {
  .group-credential-batch {
    justify-self: stretch;
    width: auto;
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
