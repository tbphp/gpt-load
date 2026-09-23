<script setup lang="ts">
import { useApiClient } from '@shared/http/client-context'
import { DialogRoot } from 'reka-ui'
import { onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import { getCredentialBalance, type CredentialBalanceResult } from '@modern/api/credential-actions'
import { AppButton, AppDialogContent, AppDialogHeader, AppNotice } from '@modern/components/ui'

const props = defineProps<{ groupId: number; row: CredentialRow }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const client = useApiClient()

const pending = ref(false)
const error = ref('')
const result = ref<CredentialBalanceResult>()

let controller = new AbortController()
onScopeDispose(() => controller.abort())

async function load(): Promise<void> {
  if (pending.value) return
  controller.abort()
  controller = new AbortController()
  pending.value = true
  error.value = ''
  try {
    result.value = await getCredentialBalance(
      client,
      props.groupId,
      props.row.id,
      controller.signal,
    )
  } catch {
    if (!controller.signal.aborted) error.value = t('credentialCards.balanceFailed')
  } finally {
    pending.value = false
  }
}

watch(() => props.row.id, load, { immediate: true })

function close(): void {
  emit('close')
}
</script>

<template>
  <DialogRoot
    :open="true"
    @update:open="
      (value) => {
        if (!value) close()
      }
    "
  >
    <AppDialogContent :title="t('credentialCards.balanceTitle')" :description="row.mask">
      <AppDialogHeader
        :title="t('credentialCards.balanceTitle')"
        :description="row.mask"
        :close-label="t('shell.close')"
        @close="close"
      />
      <div class="modern-credential-balance">
        <AppNotice v-if="pending" tone="info">{{ t('credentialCards.balanceLoading') }}</AppNotice>
        <AppNotice v-else-if="error" tone="danger"
          >{{ error
          }}<template #actions
            ><AppButton @click="load()">{{ t('ui.retry') }}</AppButton></template
          ></AppNotice
        >
        <AppNotice v-else-if="result && !result.supported" tone="warning">{{
          t('credentialCards.balanceUnsupported')
        }}</AppNotice>
        <AppNotice v-else-if="result && !result.available" tone="warning">{{
          t('credentialCards.balanceRejected')
        }}</AppNotice>
        <AppNotice v-else-if="result && !result.balances.length" tone="warning">{{
          t('credentialCards.balanceEmpty')
        }}</AppNotice>
        <table v-else-if="result" class="modern-credential-balance-table">
          <thead>
            <tr>
              <th scope="col">{{ t('credentialCards.balanceCurrency') }}</th>
              <th scope="col">{{ t('credentialCards.balanceTotal') }}</th>
              <th scope="col">{{ t('credentialCards.balanceGranted') }}</th>
              <th scope="col">{{ t('credentialCards.balanceToppedUp') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="entry in result.balances" :key="entry.currency">
              <td>{{ entry.currency }}</td>
              <td class="modern-credential-balance-total">{{ entry.total }}</td>
              <td>{{ entry.granted ?? '—' }}</td>
              <td>{{ entry.toppedUp ?? '—' }}</td>
            </tr>
          </tbody>
        </table>
        <div v-if="result" class="modern-credential-balance-actions">
          <AppButton :disabled="pending" @click="load()">{{
            t('credentialCards.queryBalance')
          }}</AppButton>
        </div>
      </div>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-credential-balance {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-3);
}
.modern-credential-balance-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--modern-font-size-small);
}
.modern-credential-balance-table th,
.modern-credential-balance-table td {
  padding: var(--modern-space-2);
  text-align: left;
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-credential-balance-total {
  font-variant-numeric: tabular-nums;
  font-weight: var(--modern-weight-medium);
}
.modern-credential-balance-actions {
  display: flex;
  justify-content: flex-end;
}
</style>
