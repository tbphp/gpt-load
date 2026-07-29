<script setup lang="ts">
import { KeyRound } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import SecretValue from '@/components/ui/SecretValue.vue'

defineProps<{
  result: AccessKeyDto
  secret: string | null
  revealPending: boolean
}>()
const emit = defineEmits<{ reveal: []; clear: [] }>()
const { t } = useI18n()
</script>

<template>
  <section class="access-key-drawer__result" aria-live="polite">
    <div class="access-key-drawer__result-title">
      <KeyRound :size="18" aria-hidden="true" />
      <strong>{{ t('accessKeys.drawer.resultTitle') }}</strong>
    </div>
    <p>{{ t('accessKeys.drawer.resultDescription') }}</p>
    <div class="access-key-drawer__secret">
      <template v-if="secret">
        <SecretValue
          :value="secret"
          :reveal-label="t('common.reveal')"
          :conceal-label="t('common.conceal')"
          button-test="access-key-result-reveal"
          @conceal="emit('clear')"
        />
        <CopyButton
          :value="secret"
          :label="t('accessKeys.copy')"
          :success-label="t('common.copied')"
          :failure-label="t('common.copyFailed')"
        />
      </template>
      <AppButton
        v-else
        data-test="access-key-result-reveal"
        variant="secondary"
        :busy="revealPending"
        @click="emit('reveal')"
      >
        {{ t('common.reveal') }}
      </AppButton>
    </div>
  </section>
</template>

<style scoped>
.access-key-drawer__result {
  display: grid;
  gap: var(--space-2);
  border: 1px solid var(--color-success);
  border-radius: var(--radius-control);
  background: var(--color-success-bg);
  padding: var(--space-3);
}
.access-key-drawer__result p {
  margin: 0;
  color: var(--color-text-muted);
}
.access-key-drawer__result-title,
.access-key-drawer__secret {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}
.access-key-drawer__result-title {
  color: var(--color-success);
}
</style>
