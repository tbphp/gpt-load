<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CredentialTestResultDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import { formatLocalInstant } from '@/lib/format'

type RestoreError = 'failed' | 'conflict' | 'conflict_refresh_failed'
type CredentialTestDialogResult = Omit<CredentialTestResultDto, 'restore_proof'>

const props = defineProps<{
  open: boolean
  mask: string
  pending: boolean
  requestFailed: boolean
  result?: CredentialTestDialogResult
  restorePending: boolean
  restoreBlocked: boolean
  restoreError?: RestoreError
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  restore: []
}>()
const { locale, n, t } = useI18n()

const busy = computed(() => props.pending || props.restorePending)
const canRestore = computed(
  () => props.result?.outcome === 'passed' && props.result.can_restore && !props.restoreBlocked,
)
const resultTone = computed(() => {
  if (props.result?.outcome === 'passed') return 'success' as const
  if (props.result?.outcome === 'failed') return 'danger' as const
  return 'warning' as const
})
const reasonLabel = computed(() =>
  props.result ? t(`group.credentials.test.reason.${props.result.reason ?? 'passed'}`) : '',
)

function setOpen(open: boolean): void {
  if (!open && busy.value) return
  emit('update:open', open)
}
</script>

<template>
  <AppDialog
    appearance="ledger"
    :open="open"
    :title="t('group.credentials.test.title')"
    :description="t('group.credentials.test.description')"
    :close-label="t('group.credentials.test.close')"
    :dismissible="!busy"
    @update:open="setOpen"
  >
    <template #body>
      <div class="credential-test-dialog">
        <p class="credential-test-dialog__credential">
          <span>{{ t('group.credentials.test.fields.credential') }}</span>
          <strong>{{ mask }}</strong>
        </p>
        <QueryFeedback
          v-if="pending"
          state="loading"
          :message="t('group.credentials.test.loading', { mask })"
        />
        <InlineFeedback v-else-if="requestFailed" tone="danger" appearance="ledger">
          {{ t('group.credentials.test.requestFailed') }}
        </InlineFeedback>
        <template v-else-if="result">
          <InlineFeedback :tone="resultTone" appearance="ledger">
            {{ t(`group.credentials.test.outcome.${result.outcome}`) }}
          </InlineFeedback>
          <dl class="credential-test-dialog__details">
            <dt>{{ t('group.credentials.test.fields.model') }}</dt>
            <dd>{{ result.model }}</dd>
            <dt>{{ t('group.credentials.test.fields.protocol') }}</dt>
            <dd>{{ result.protocol }}</dd>
            <dt>{{ t('group.credentials.test.fields.latency') }}</dt>
            <dd>{{ t('group.credentials.test.latency', { value: n(result.latency_ms) }) }}</dd>
            <dt>{{ t('group.credentials.test.fields.reason') }}</dt>
            <dd>{{ reasonLabel }}</dd>
            <dt>{{ t('group.credentials.test.fields.testedAt') }}</dt>
            <dd>{{ formatLocalInstant(result.tested_at_ms, locale) }}</dd>
          </dl>
          <InlineFeedback
            v-if="result.outcome === 'passed' && result.can_restore && !restoreBlocked"
            tone="warning"
            appearance="ledger"
          >
            {{ t('group.credentials.test.restorePrompt') }}
          </InlineFeedback>
          <InlineFeedback v-if="restoreError" tone="danger" appearance="ledger">
            {{ t(`group.credentials.test.restoreError.${restoreError}`) }}
          </InlineFeedback>
        </template>
      </div>
    </template>

    <template #footer>
      <template v-if="canRestore">
        <AppButton
          variant="secondary"
          size="compact"
          :disabled="restorePending"
          @click="setOpen(false)"
        >
          {{ t('group.credentials.test.keepBlocked') }}
        </AppButton>
        <AppButton size="compact" :busy="restorePending" @click="emit('restore')">
          {{
            t(
              restorePending
                ? 'group.credentials.test.restoring'
                : 'group.credentials.test.restore',
            )
          }}
        </AppButton>
      </template>
      <AppButton v-else variant="secondary" size="compact" :disabled="busy" @click="setOpen(false)">
        {{ t('group.credentials.test.close') }}
      </AppButton>
    </template>
  </AppDialog>
</template>

<style scoped>
.credential-test-dialog {
  display: grid;
  gap: var(--space-3);
}

.credential-test-dialog__credential {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: var(--space-2);
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.credential-test-dialog__credential strong {
  min-width: 0;
  color: var(--color-text);
  font-weight: 600;
  overflow-wrap: anywhere;
}

.credential-test-dialog__details {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 8px var(--space-3);
  margin: 0;
  font-size: var(--text-sm);
  line-height: var(--line-normal);
}

.credential-test-dialog__details dt {
  color: var(--color-text-muted);
}

.credential-test-dialog__details dd {
  min-width: 0;
  margin: 0;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}

@media (max-width: 480px) {
  .credential-test-dialog__credential {
    display: grid;
    gap: var(--space-1);
  }

  .credential-test-dialog__details {
    grid-template-columns: 1fr;
    gap: var(--space-1);
  }

  .credential-test-dialog__details dd + dt {
    margin-top: var(--space-2);
  }
}
</style>
