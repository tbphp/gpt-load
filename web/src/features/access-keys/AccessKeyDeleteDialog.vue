<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { deleteAccessKey } from '@/app/resources/access-keys'
import type { AccessKeyDto } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppTypedConfirmation from '@/components/ui/AppTypedConfirmation.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

const props = withDefaults(
  defineProps<{ accessKey: AccessKeyDto; total: number; disabled?: boolean }>(),
  { disabled: false },
)
const emit = defineEmits<{ deleted: [name: string] }>()
const client = useApiClient()
const { t } = useI18n()
const open = ref(false)
const typedName = ref('')
const nameInput = ref<InstanceType<typeof AppTypedConfirmation>>()
const pending = ref(false)
const failed = ref(false)
let controller: AbortController | undefined

const confirmed = computed(() => typedName.value === props.accessKey.name)

async function focusNameInput(): Promise<void> {
  await nextTick()
  await nextTick()
  nameInput.value?.focus()
}

function setOpen(value: boolean): void {
  if (!value && pending.value) return
  if (!value) {
    controller?.abort()
    controller = undefined
    failed.value = false
    typedName.value = ''
  }
  open.value = value
  if (value) void focusNameInput()
}

function openDialog(): void {
  if (props.disabled) return
  setOpen(true)
}

async function confirmDelete(): Promise<void> {
  if (!confirmed.value || pending.value) return
  pending.value = true
  failed.value = false
  controller = new AbortController()
  const activeController = controller
  try {
    await deleteAccessKey(client, props.accessKey.id, activeController.signal)
    open.value = false
    typedName.value = ''
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
  <AppConfirmDialog
    :open="open"
    :title="t('accessKeys.delete.title')"
    :description="t('accessKeys.delete.description', { name: accessKey.name })"
    :close-label="t('accessKeys.delete.close')"
    :cancel-label="t('common.cancel')"
    :confirm-label="t('accessKeys.delete.confirm')"
    tone="danger"
    :pending="pending"
    :confirm-disabled="!confirmed"
    @update:open="setOpen"
    @confirm="confirmDelete"
  >
    <template #trigger>
      <slot name="trigger" :open="openDialog">
        <AppButton variant="danger" size="compact" :disabled="disabled" @click="openDialog">
          <Trash2 :size="16" aria-hidden="true" />
          {{ t('accessKeys.delete.open') }}
        </AppButton>
      </slot>
    </template>

    <div class="access-key-delete__body">
      <div class="access-key-delete__summary">
        <strong>{{ accessKey.name }}</strong>
        <code>{{ accessKey.masked_key }}</code>
      </div>
      <InlineFeedback v-if="total === 1" tone="warning" appearance="hint">
        {{ t('accessKeys.delete.lastWarning') }}
      </InlineFeedback>
      <InlineFeedback tone="warning" appearance="hint">
        {{ t('accessKeys.delete.impact') }}
      </InlineFeedback>
      <AppTypedConfirmation
        id="access-key-delete-name"
        ref="nameInput"
        v-model="typedName"
        :label="t('accessKeys.delete.typeName', { name: accessKey.name })"
        :disabled="pending"
      />
      <InlineFeedback v-if="failed" tone="danger">{{
        t('accessKeys.delete.failed')
      }}</InlineFeedback>
    </div>
  </AppConfirmDialog>
</template>

<style scoped>
.access-key-delete__body {
  display: grid;
  gap: 12px;
}
.access-key-delete__summary {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 9px 10px;
}
.access-key-delete__summary strong {
  font-size: var(--text-sm);
}
.access-key-delete__summary code {
  min-width: 0;
  color: var(--color-code);
  font-size: var(--text-label-xs);
  overflow-wrap: anywhere;
}
</style>
