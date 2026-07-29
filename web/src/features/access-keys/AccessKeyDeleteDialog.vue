<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { deleteAccessKey } from '@/app/resources/access-keys'
import type { AccessKeyDto } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

const props = defineProps<{ accessKey: AccessKeyDto; total: number }>()
const emit = defineEmits<{ deleted: [name: string] }>()
const client = useApiClient()
const { t } = useI18n()
const open = ref(false)
const pending = ref(false)
const failed = ref(false)
let controller: AbortController | undefined

function setOpen(value: boolean): void {
  if (!value && pending.value) return
  if (!value) {
    controller?.abort()
    controller = undefined
    failed.value = false
  }
  open.value = value
}

async function confirmDelete(): Promise<void> {
  if (pending.value) return
  pending.value = true
  failed.value = false
  controller = new AbortController()
  const activeController = controller
  try {
    await deleteAccessKey(client, props.accessKey.id, activeController.signal)
    open.value = false
    emit('deleted', props.accessKey.name)
  } catch (error: unknown) {
    if (!(error instanceof RequestCancelledError)) failed.value = true
  } finally {
    if (controller === activeController) controller = undefined
    pending.value = false
  }
}

onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <AppDialog
    :open="open"
    :title="t('accessKeys.delete.title')"
    :description="t('accessKeys.delete.description', { name: accessKey.name })"
    :close-label="t('accessKeys.delete.close')"
    :dismissible="!pending"
    @update:open="setOpen"
  >
    <template #trigger>
      <button
        type="button"
        class="access-key-delete__trigger"
        data-test="access-key-delete-open"
        @click="setOpen(true)"
      >
        <Trash2 :size="16" aria-hidden="true" />{{ t('accessKeys.delete.open') }}
      </button>
    </template>

    <div class="access-key-delete__body">
      <InlineFeedback v-if="total === 1" tone="warning">
        {{ t('accessKeys.delete.lastWarning') }}
      </InlineFeedback>
      <InlineFeedback v-if="failed" tone="danger">{{
        t('accessKeys.delete.failed')
      }}</InlineFeedback>
      <div class="access-key-delete__actions">
        <AppButton variant="secondary" :disabled="pending" @click="setOpen(false)">
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton
          data-test="access-key-delete-confirm"
          class="access-key-delete__confirm"
          variant="danger"
          :busy="pending"
          @click="confirmDelete"
        >
          {{ t('accessKeys.delete.confirm') }}
        </AppButton>
      </div>
    </div>
  </AppDialog>
</template>

<style scoped>
.access-key-delete__trigger {
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
.access-key-delete__body {
  display: grid;
  gap: var(--space-4);
}
.access-key-delete__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
}
</style>
