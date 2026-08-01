<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
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
const typedName = ref('')
const nameInput = ref<HTMLInputElement>()
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
        :aria-label="t('accessKeys.delete.open')"
        @click="setOpen(true)"
      >
        <Trash2 :size="16" aria-hidden="true" />
        <span class="access-key-delete__trigger-label">{{ t('accessKeys.delete.open') }}</span>
      </button>
    </template>

    <div class="access-key-delete__body">
      <InlineFeedback v-if="total === 1" tone="warning">
        {{ t('accessKeys.delete.lastWarning') }}
      </InlineFeedback>
      <dl class="access-key-delete__details">
        <dt>{{ t('accessKeys.delete.name') }}</dt>
        <dd>{{ accessKey.name }}</dd>
        <dt>{{ t('accessKeys.delete.key') }}</dt>
        <dd>
          <code>{{ accessKey.masked_key }}</code>
        </dd>
      </dl>
      <InlineFeedback tone="warning">
        {{ t('accessKeys.delete.impact') }}
      </InlineFeedback>
      <label class="access-key-delete__label" for="access-key-delete-name">{{
        t('accessKeys.delete.typeName', { name: accessKey.name })
      }}</label>
      <input
        id="access-key-delete-name"
        ref="nameInput"
        v-model="typedName"
        type="text"
        autocomplete="off"
        spellcheck="false"
        :disabled="pending"
      />
      <InlineFeedback v-if="failed" tone="danger">{{
        t('accessKeys.delete.failed')
      }}</InlineFeedback>
      <div class="access-key-delete__actions">
        <AppButton variant="secondary" :disabled="pending" @click="setOpen(false)">
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton
          class="access-key-delete__confirm"
          variant="danger"
          :busy="pending"
          :disabled="!confirmed"
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
.access-key-delete__label {
  margin-bottom: calc(var(--space-3) * -1);
  font-weight: 650;
}
.access-key-delete__details {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-2) var(--space-3);
  margin: 0;
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
}
.access-key-delete__details dt {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.access-key-delete__details dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}
.access-key-delete__details code {
  color: var(--color-code);
}
input {
  width: 100%;
  min-height: var(--touch-target);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
.access-key-delete__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
}
</style>
