<script setup lang="ts">
import { RotateCcw } from 'lucide-vue-next'
import { useQueryClient } from '@tanstack/vue-query'
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { resetModelPrice, type ModelPriceRuleDto } from '@/app/resources/model-prices'
import { RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

const props = defineProps<{ rule: ModelPriceRuleDto }>()
const emit = defineEmits<{ reset: [] }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const open = ref(false)
const pending = ref(false)
const failed = ref(false)
let controller: AbortController | undefined

function clearRequest(): void {
  controller?.abort()
  controller = undefined
  pending.value = false
}

function setOpen(value: boolean): void {
  if (!value && pending.value) return
  if (!value) {
    clearRequest()
    failed.value = false
  }
  open.value = value
}

async function confirmReset(): Promise<void> {
  if (pending.value) return
  pending.value = true
  failed.value = false
  controller = new AbortController()
  const activeController = controller
  try {
    await resetModelPrice(client, props.rule.pattern, activeController.signal)
    if (controller !== activeController || !open.value) return
    await queryClient.invalidateQueries({ queryKey: controlQueryKeys.modelPrices() })
    if (controller !== activeController || !open.value) return
    open.value = false
    emit('reset')
  } catch (error: unknown) {
    if (
      controller === activeController &&
      open.value &&
      !activeController.signal.aborted &&
      !(error instanceof RequestCancelledError)
    ) {
      failed.value = true
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
  <AppDialog
    :open="open"
    :title="t('modelPrices.reset.title')"
    :description="t('modelPrices.reset.description', { pattern: rule.pattern })"
    :close-label="t('modelPrices.reset.close')"
    :dismissible="!pending"
    @update:open="setOpen"
  >
    <template #trigger>
      <button
        type="button"
        class="model-price-reset__trigger"
        data-test="model-price-reset-open"
        @click="setOpen(true)"
      >
        <RotateCcw :size="16" aria-hidden="true" />{{ t('modelPrices.reset.open') }}
      </button>
    </template>

    <div class="model-price-reset__body">
      <InlineFeedback tone="warning">{{ t('modelPrices.reset.warning') }}</InlineFeedback>
      <InlineFeedback v-if="failed" tone="danger">
        {{ t('modelPrices.reset.failed') }}
      </InlineFeedback>
      <div class="model-price-reset__actions">
        <AppButton variant="secondary" :disabled="pending" @click="setOpen(false)">
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton
          data-test="model-price-reset-confirm"
          class="model-price-reset__confirm"
          variant="secondary"
          :busy="pending"
          @click="confirmReset"
        >
          {{ t('modelPrices.reset.confirm') }}
        </AppButton>
      </div>
    </div>
  </AppDialog>
</template>

<style scoped>
.model-price-reset__trigger {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-1);
  border: 0;
  background: transparent;
  color: var(--color-danger);
  font: inherit;
  font-weight: 650;
  cursor: pointer;
}
.model-price-reset__body {
  display: grid;
  gap: var(--space-4);
}
.model-price-reset__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
}
.model-price-reset__confirm {
  border-color: var(--color-danger);
  color: var(--color-danger);
}
</style>
