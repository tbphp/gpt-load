<script setup lang="ts">
import { RotateCcw, Trash2 } from '@lucide/vue'
import { useQueryClient } from '@tanstack/vue-query'
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import {
  deleteModelPrice,
  projectModelPriceMutationIssue,
  resetModelPrice,
  type ModelPriceDto,
} from '@/app/resources/model-prices'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

const props = withDefaults(
  defineProps<{
    row: ModelPriceDto
    action: 'reset' | 'delete'
    disabled?: boolean
  }>(),
  { disabled: false },
)
const emit = defineEmits<{ completed: []; pending: [pending: boolean] }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const open = ref(false)
const pending = ref(false)
const failure = ref('')
let controller: AbortController | undefined

watch(pending, (value) => emit('pending', value))

function clearRequest(): void {
  controller?.abort()
  controller = undefined
  pending.value = false
}

function setOpen(value: boolean): void {
  if (!value && pending.value) return
  if (!value) {
    clearRequest()
    failure.value = ''
  }
  open.value = value
}

function failureMessage(error: unknown): string {
  try {
    const issue = projectModelPriceMutationIssue(error)
    if (issue?.code === 'MODEL_PRICE_REFERENCED') {
      return t('modelPrices.errors.referenced', {
        entries: issue.reference_count,
        groups: issue.reference_group_count,
      })
    }
    if (issue?.code === 'MODEL_PRICE_AUTOMATIC_DELETE_FORBIDDEN') {
      return t('modelPrices.errors.automaticDeleteForbidden')
    }
  } catch {
    return t(`modelPrices.${props.action}.failed`)
  }
  return t(`modelPrices.${props.action}.failed`)
}

async function confirm(): Promise<void> {
  if (pending.value) return
  pending.value = true
  failure.value = ''
  controller = new AbortController()
  const activeController = controller
  try {
    if (props.action === 'reset') {
      await resetModelPrice(client, props.row.id, activeController.signal)
    } else {
      await deleteModelPrice(client, props.row.id, activeController.signal)
    }
    if (controller !== activeController || !open.value) return
    await applyInvalidationPlan(
      queryClient,
      props.action === 'reset'
        ? mutationInvalidationPlans.modelPrice.reset
        : mutationInvalidationPlans.modelPrice.delete,
    )
    if (controller !== activeController || !open.value) return
    open.value = false
    emit('completed')
  } catch (error: unknown) {
    if (
      controller === activeController &&
      open.value &&
      !activeController.signal.aborted &&
      !(error instanceof RequestCancelledError)
    ) {
      failure.value = failureMessage(error)
    }
  } finally {
    if (controller === activeController) {
      controller = undefined
      pending.value = false
    }
  }
}

onBeforeUnmount(clearRequest)
</script>

<template>
  <AppConfirmDialog
    appearance="ledger"
    :open="open"
    :title="t(`modelPrices.${action}.title`)"
    :description="t(`modelPrices.${action}.description`, { model: row.model_id })"
    :close-label="t(`modelPrices.${action}.close`)"
    :cancel-label="t('common.cancel')"
    :confirm-label="t(`modelPrices.${action}.confirm`)"
    :tone="action === 'delete' ? 'danger' : 'default'"
    :pending="pending"
    @update:open="setOpen"
    @confirm="confirm"
  >
    <template #trigger>
      <AppButton
        variant="ghost"
        size="compact"
        :disabled="disabled"
        :aria-label="t(`modelPrices.${action}.open`, { model: row.model_id })"
        @click="setOpen(true)"
      >
        <RotateCcw v-if="action === 'reset'" :size="14" aria-hidden="true" />
        <Trash2 v-else :size="14" aria-hidden="true" />
        {{ t(`modelPrices.${action}.confirm`) }}
      </AppButton>
    </template>

    <InlineFeedback tone="warning" appearance="ledger">
      {{ t(`modelPrices.${action}.warning`) }}
    </InlineFeedback>
    <InlineFeedback v-if="failure" tone="danger" appearance="ledger">
      {{ failure }}
    </InlineFeedback>
  </AppConfirmDialog>
</template>
