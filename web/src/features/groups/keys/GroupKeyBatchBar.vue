<script setup lang="ts">
import { Ban, Check, Trash2, X } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'

defineProps<{ selectedCount: number; pending?: boolean }>()
const emit = defineEmits<{ enable: []; disable: []; remove: []; clear: [] }>()
const { n, t } = useI18n()
</script>

<template>
  <div class="group-key-batch" role="status">
    <strong>{{ t('group.keys.batch.selected', { count: n(selectedCount) }) }}</strong>
    <div class="group-key-batch__actions">
      <AppButton variant="secondary" size="compact" :busy="pending" @click="emit('enable')"
        ><Check :size="15" aria-hidden="true" />{{ t('group.keys.batch.enable') }}</AppButton
      ><AppButton variant="secondary" size="compact" :busy="pending" @click="emit('disable')"
        ><Ban :size="15" aria-hidden="true" />{{ t('group.keys.batch.disable') }}</AppButton
      ><AppButton variant="danger" size="compact" :busy="pending" @click="emit('remove')"
        ><Trash2 :size="15" aria-hidden="true" />{{ t('group.keys.batch.delete') }}</AppButton
      ><AppButton variant="ghost" size="compact" :disabled="pending" @click="emit('clear')"
        ><X :size="15" aria-hidden="true" />{{ t('group.keys.batch.clear') }}</AppButton
      >
    </div>
  </div>
</template>

<style scoped>
.group-key-batch {
  position: sticky;
  z-index: 1;
  bottom: var(--space-3);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-card);
  background: var(--color-surface-raised);
  box-shadow: var(--shadow-card);
  padding: var(--space-3);
}
.group-key-batch__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
@media (max-width: 680px) {
  .group-key-batch {
    align-items: stretch;
    flex-direction: column;
  }
  .group-key-batch__actions > * {
    flex: 1 1 calc(50% - var(--space-2));
  }
}
</style>
