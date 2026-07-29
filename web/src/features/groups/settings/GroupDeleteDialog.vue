<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { Trash2 } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { deleteGroup, isGroupInUseData, type AccessKeyReferenceDto } from '@/app/resources/groups'
import { ApiError, RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import AppButton from '@/components/ui/AppButton.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

import GroupInUseFeedback from './GroupInUseFeedback.vue'

const props = defineProps<{ groupId: number; groupName: string }>()
const client = useApiClient()
const queryClient = useQueryClient()
const router = useRouter()
const { t } = useI18n()
const open = ref(false)
const typedName = ref('')
const nameInput = ref<HTMLInputElement>()
const pending = ref(false)
const genericError = ref(false)
const references = ref<AccessKeyReferenceDto[]>([])
let controller: AbortController | undefined

const confirmed = computed(() => typedName.value === props.groupName)

async function focusNameInput(): Promise<void> {
  await nextTick()
  await nextTick()
  nameInput.value?.focus()
}

function setOpen(value: boolean): void {
  if (pending.value && !value) return
  open.value = value
  if (value) {
    genericError.value = false
    references.value = []
    void focusNameInput()
  } else {
    typedName.value = ''
  }
}

async function confirmDelete(): Promise<void> {
  if (!confirmed.value || pending.value) return
  pending.value = true
  genericError.value = false
  references.value = []
  controller = new AbortController()
  const activeController = controller
  try {
    await deleteGroup(client, props.groupId, activeController.signal)
    queryClient.removeQueries({
      queryKey: controlQueryKeys.groups.detail(props.groupId),
      exact: true,
    })
    queryClient.removeQueries({
      queryKey: controlQueryKeys.groups.keys(props.groupId),
      exact: true,
    })
    await applyInvalidationPlan(queryClient, mutationInvalidationPlans.group.delete)
    await router.replace({ name: 'home' })
  } catch (error: unknown) {
    if (error instanceof RequestCancelledError) return
    if (
      error instanceof ApiError &&
      error.code === 'GROUP_IN_USE' &&
      isGroupInUseData(error.data)
    ) {
      references.value = error.data.access_keys
    } else {
      genericError.value = true
    }
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
    :title="t('group.settings.delete.title')"
    :description="t('group.settings.delete.description', { name: groupName })"
    :close-label="t('group.settings.delete.close')"
    :dismissible="!pending"
    @update:open="setOpen"
  >
    <template #trigger>
      <AppButton
        data-test="group-delete-open"
        class="group-delete__open"
        variant="secondary"
        @click="setOpen(true)"
      >
        <Trash2 :size="16" aria-hidden="true" />{{ t('group.settings.delete.open') }}
      </AppButton>
    </template>

    <div class="group-delete__body">
      <label for="group-delete-name">{{
        t('group.settings.delete.typeName', { name: groupName })
      }}</label>
      <input
        id="group-delete-name"
        ref="nameInput"
        v-model="typedName"
        data-test="group-delete-name"
        type="text"
        autocomplete="off"
        spellcheck="false"
        :disabled="pending"
      />
      <GroupInUseFeedback v-if="references.length" :references="references" />
      <InlineFeedback v-else-if="genericError" tone="danger">
        {{ t('group.settings.delete.failed') }}
      </InlineFeedback>
      <div class="group-delete__actions">
        <AppButton variant="secondary" :disabled="pending" @click="setOpen(false)">
          {{ t('group.settings.delete.cancel') }}
        </AppButton>
        <AppButton
          data-test="group-delete-confirm"
          class="group-delete__confirm"
          variant="secondary"
          :busy="pending"
          :disabled="!confirmed"
          @click="confirmDelete"
        >
          {{ t('group.settings.delete.confirm') }}
        </AppButton>
      </div>
    </div>
  </AppDialog>
</template>

<style scoped>
.group-delete__open,
.group-delete__confirm {
  border-color: var(--color-danger);
  color: var(--color-danger);
}
.group-delete__open :deep(button),
.group-delete__open {
  gap: var(--space-2);
}
.group-delete__body {
  display: grid;
  gap: var(--space-3);
}
label {
  font-weight: 650;
}
input {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
.group-delete__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
}
</style>
