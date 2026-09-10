<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

defineProps<{
  messageKey: string
  resourceIdentity: string
  canRetry: boolean
  canAbandon: boolean
  pending: boolean
}>()
const emit = defineEmits<{ retry: []; abandon: [] }>()
const { t } = useI18n()
</script>

<template>
  <section v-if="messageKey" class="operation-notice" aria-live="polite">
    <InlineFeedback tone="warning">{{ t(messageKey) }}</InlineFeedback>
    <code v-if="resourceIdentity">{{ resourceIdentity }}</code>
    <div class="operation-notice__actions">
      <AppButton
        v-if="!resourceIdentity"
        variant="secondary"
        :disabled="!canRetry"
        :busy="pending"
        @click="emit('retry')"
      >
        {{ t('import.operation.checkResult') }}
      </AppButton>
      <AppButton variant="ghost" :disabled="!canAbandon || pending" @click="emit('abandon')">
        {{ t('import.operation.abandon') }}
      </AppButton>
    </div>
  </section>
</template>

<style scoped>
.operation-notice {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-card);
  background: var(--color-warning-bg);
  padding: var(--space-3) var(--space-4);
}
.operation-notice code {
  overflow-wrap: anywhere;
}

.operation-notice__actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--space-2);
}

@media (max-width: 640px) {
  .operation-notice {
    align-items: stretch;
    flex-direction: column;
  }

  .operation-notice__actions :deep(.app-button) {
    flex: 1;
  }
}
</style>
