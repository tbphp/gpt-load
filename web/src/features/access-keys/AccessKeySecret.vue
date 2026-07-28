<script setup lang="ts">
import { Eye, EyeOff } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import CopyButton from '@/components/ui/CopyButton.vue'

defineProps<{
  id: number
  maskedKey: string
  revealedValue?: string
  pending?: boolean
  failed?: boolean
}>()

defineEmits<{ toggle: [id: number] }>()
const { t } = useI18n()
</script>

<template>
  <div class="access-key-secret">
    <code :title="revealedValue || maskedKey">{{ revealedValue || maskedKey }}</code>
    <button
      type="button"
      class="access-key-secret__reveal"
      :aria-label="revealedValue ? t('common.conceal') : t('common.reveal')"
      :aria-pressed="Boolean(revealedValue)"
      :data-test="`access-key-reveal-${id}`"
      :disabled="pending"
      @click="$emit('toggle', id)"
    >
      <EyeOff v-if="revealedValue" :size="16" aria-hidden="true" />
      <Eye v-else :size="16" aria-hidden="true" />
    </button>
    <CopyButton
      v-if="revealedValue"
      :value="revealedValue"
      :label="t('accessKeys.copy')"
      :success-label="t('common.copied')"
      :failure-label="t('common.copyFailed')"
      :data-test="`access-key-copy-${id}`"
    />
    <small v-if="failed" role="alert">{{ t('accessKeys.revealFailed') }}</small>
  </div>
</template>

<style scoped>
.access-key-secret {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
}

.access-key-secret code {
  max-width: 100%;
  overflow-x: auto;
  color: var(--color-code);
  font-family: var(--font-mono);
  white-space: nowrap;
}

.access-key-secret__reveal {
  display: inline-flex;
  width: var(--touch-target);
  height: var(--touch-target);
  flex: 0 0 var(--touch-target);
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  cursor: pointer;
}

.access-key-secret small {
  flex-basis: 100%;
  color: var(--color-danger);
}
</style>
