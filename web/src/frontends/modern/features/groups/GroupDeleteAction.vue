<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import { onScopeDispose, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { deleteGroup, type GroupRow } from '@modern/api/groups'
import { AppIconButton, AppConfirmDialog, AppTextField } from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'

const props = defineProps<{ group: GroupRow; disabled?: boolean }>()
const emit = defineEmits<{ deleted: []; pending: [value: boolean] }>()
const { t } = useI18n()
const client = useApiClient()
const open = ref(false)
const typed = ref('')
const pending = ref(false)
const error = ref('')
const controller = new AbortController()
function show(): void {
  if (props.disabled) return
  typed.value = ''
  error.value = ''
  open.value = true
}
async function remove(): Promise<void> {
  if (props.disabled || pending.value || typed.value !== props.group.name) return
  pending.value = true
  emit('pending', true)
  error.value = ''
  try {
    await deleteGroup(client, props.group.id, controller.signal)
    if (controller.signal.aborted) return
    open.value = false
    emit('deleted')
  } catch (cause) {
    if (controller.signal.aborted) return
    if (cause instanceof ApiError && cause.code === 'GROUP_IN_USE') {
      const data = cause.data as { access_keys?: { name?: unknown }[] } | undefined
      const names = Array.isArray(data?.access_keys)
        ? data.access_keys
            .map((item) => (typeof item?.name === 'string' ? item.name : ''))
            .filter(Boolean)
        : []
      error.value = [t('groupWorkflows.deleteInUse'), names.join('、')].filter(Boolean).join(' ')
    } else error.value = t('groupWorkflows.deleteFailed')
  } finally {
    pending.value = false
    emit('pending', false)
  }
}
onScopeDispose(() => controller.abort())
</script>

<template>
  <AppIconButton
    :icon="Trash2"
    :label="t('groupWorkflows.deleteGroup')"
    variant="danger"
    size="sm"
    :disabled="disabled || pending"
    @click="show"
  />
  <AppConfirmDialog
    :open="open"
    :icon="Trash2"
    :title="t('groupWorkflows.deleteGroup')"
    :subject="group.name"
    :description="t('groupWorkflows.deleteDescription')"
    :confirm-label="t('groupWorkflows.deleteGroup')"
    :pending="pending"
    :disabled="disabled || typed !== group.name"
    :error="error"
    tone="danger"
    @cancel="open = false"
    @confirm="remove"
  >
    <AppTextField
      v-model="typed"
      :label="t('groupWorkflows.typeGroupName')"
      :disabled="pending"
      autocomplete="off"
    />
  </AppConfirmDialog>
</template>
