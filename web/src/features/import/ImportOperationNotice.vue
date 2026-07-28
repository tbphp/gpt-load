<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

defineProps<{
  messageKey: string
  resourceIdentity: string
  canRetry: boolean
  pending: boolean
}>()
const emit = defineEmits<{ retry: [] }>()
const { t } = useI18n()
</script>

<template>
  <section
    v-if="messageKey"
    class="operation-notice"
    data-test="import-operation-notice"
    aria-live="polite"
  >
    <InlineFeedback tone="warning">{{ t(messageKey) }}</InlineFeedback>
    <code v-if="resourceIdentity" data-test="import-operation-resource">{{
      resourceIdentity
    }}</code>
    <AppButton
      v-else
      data-test="import-operation-retry"
      variant="secondary"
      :disabled="!canRetry"
      :busy="pending"
      @click="emit('retry')"
    >
      {{ t('import.operation.checkResult') }}
    </AppButton>
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
</style>
