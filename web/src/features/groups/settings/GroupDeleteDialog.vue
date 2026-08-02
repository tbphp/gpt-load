<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { isNavigationFailure, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import {
  clearGroupResourceCaches,
  deleteGroup,
  isGroupInUseData,
  type AccessKeyReferenceDto,
} from '@/app/resources/groups'
import { ApiError, RequestCancelledError } from '@/api/errors'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { groupsLocation } from '@/app/route-locations'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppTypedConfirmation from '@/components/ui/AppTypedConfirmation.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

import GroupInUseFeedback from './GroupInUseFeedback.vue'

const props = withDefaults(
  defineProps<{ groupId: number; groupName: string; disabled?: boolean }>(),
  {
    disabled: false,
  },
)
const emit = defineEmits<{
  deleted: []
  'update:pending': [value: boolean]
}>()
const client = useApiClient()
const queryClient = useQueryClient()
const router = useRouter()
const { t } = useI18n()
const open = ref(false)
const typedName = ref('')
const nameInput = ref<InstanceType<typeof AppTypedConfirmation>>()
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
  if ((pending.value || props.disabled) && !value) return
  if (props.disabled && value) return
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
  if (!confirmed.value || pending.value || props.disabled) return
  pending.value = true
  emit('update:pending', true)
  genericError.value = false
  references.value = []
  controller = new AbortController()
  const activeController = controller
  try {
    await deleteGroup(client, props.groupId, activeController.signal)
    emit('deleted')
    clearGroupResourceCaches(queryClient, props.groupId)
    await applyInvalidationPlan(queryClient, mutationInvalidationPlans.group.delete)
    try {
      const failure = await router.replace(groupsLocation())
      if (isNavigationFailure(failure))
        window.location.assign(router.resolve(groupsLocation()).href)
    } catch {
      window.location.assign(router.resolve(groupsLocation()).href)
    }
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
    emit('update:pending', false)
  }
}

onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <AppConfirmDialog
    appearance="ledger"
    :open="open"
    :title="t('group.settings.delete.title')"
    :description="t('group.settings.delete.description', { name: groupName })"
    :close-label="t('group.settings.delete.close')"
    :cancel-label="t('group.settings.delete.cancel')"
    :confirm-label="t('group.settings.delete.confirm')"
    tone="danger"
    :pending="pending"
    :confirm-disabled="!confirmed || disabled"
    @update:open="setOpen"
    @confirm="confirmDelete"
  >
    <template #trigger>
      <AppButton
        class="group-delete__open"
        variant="secondary"
        size="compact"
        :disabled="disabled"
        @click="setOpen(true)"
      >
        {{ t('group.settings.delete.open') }}
      </AppButton>
    </template>

    <div class="group-delete__body">
      <AppTypedConfirmation
        id="group-delete-name"
        ref="nameInput"
        v-model="typedName"
        :label="t('group.settings.delete.typeName', { name: groupName })"
        :disabled="pending"
      />
      <GroupInUseFeedback v-if="references.length" :references="references" />
      <InlineFeedback v-else-if="genericError" tone="danger">
        {{ t('group.settings.delete.failed') }}
      </InlineFeedback>
    </div>
  </AppConfirmDialog>
</template>

<style scoped>
.group-delete__open {
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
</style>
