<script setup lang="ts">
import { RotateCcw } from '@lucide/vue'
import { useQueryClient } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import type { AccessKeyDto, AccessKeyRotateResultDto } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import { classifyMutationOutcome } from '@/app/mutation-outcome'
import { accessKeyResources, revealAccessKey, rotateAccessKey } from '@/app/resources/access-keys'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import { createUUID } from '@/lib/uuid'

import { findAccessKeyForReconciliation } from './access-key-edit-operation'
import type { PendingAccessKeyRotateOperation } from './access-key-rotate-operation'

const props = withDefaults(
  defineProps<{
    accessKey: AccessKeyDto
    disabled?: boolean
    operation?: PendingAccessKeyRotateOperation | null
  }>(),
  { disabled: false, operation: null },
)
const emit = defineEmits<{
  rotated: [accessKey: AccessKeyDto]
  'update:pending': [pending: boolean]
  'update:operation': [operation: PendingAccessKeyRotateOperation | null]
}>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const open = ref(false)
const pending = ref(false)
const failed = ref(false)
const refreshFailed = ref(false)
const operationState = ref<'idle' | 'indeterminate' | 'reconciling'>(
  props.operation?.state ?? 'idle',
)
const operationID = ref(props.operation?.idempotencyKey ?? createUUID())
const result = ref<AccessKeyRotateResultDto | null>(null)
const current = ref<AccessKeyDto | null>(null)
let controller: AbortController | undefined

const feedbackKey = computed(() => {
  if (operationState.value === 'reconciling') return 'accessKeys.rotate.reconciling'
  if (operationState.value === 'indeterminate') return 'accessKeys.rotate.indeterminate'
  return ''
})
const confirmLabel = computed(() => {
  if (result.value) return t('accessKeys.rotate.done')
  if (operationState.value !== 'idle') return t('accessKeys.rotate.checkResult')
  return t('accessKeys.rotate.confirm')
})
const triggerLabel = computed(() =>
  operationState.value === 'idle'
    ? t('accessKeys.rotate.open')
    : t('accessKeys.rotate.checkResult'),
)
const displayKey = computed(() =>
  result.value?.key ? result.value.key : (current.value?.masked_key ?? props.accessKey.masked_key),
)

watch(pending, (value) => emit('update:pending', value))
watch(
  () => props.operation,
  (operation) => {
    if (pending.value || result.value) return
    if (operation) {
      operationID.value = operation.idempotencyKey
      operationState.value = operation.state
    } else if (operationState.value !== 'idle') {
      operationID.value = createUUID()
      operationState.value = 'idle'
    }
  },
)

function resetOperation(): void {
  controller?.abort()
  controller = undefined
  pending.value = false
  failed.value = false
  refreshFailed.value = false
  operationState.value = 'idle'
  operationID.value = createUUID()
  result.value = null
  current.value = null
  emit('update:operation', null)
}

function setOpen(value: boolean): void {
  if (!value && pending.value) return
  const refreshAfterClose = !value && result.value !== null
  if (refreshAfterClose) resetOperation()
  open.value = value
  if (refreshAfterClose) void refreshQueries()
}

function openDialog(): void {
  if (props.disabled) return
  open.value = true
}

async function refreshCurrent(signal: AbortSignal): Promise<AccessKeyDto | null> {
  try {
    return (await findAccessKeyForReconciliation(client, props.accessKey.id, signal)) ?? null
  } catch {
    refreshFailed.value = true
    return null
  }
}

async function resolveCurrentKey(): Promise<string> {
  const value = await revealAccessKey(client, props.accessKey.id)
  return value.key
}

async function refreshQueries(): Promise<void> {
  try {
    await applyInvalidationPlan(queryClient, mutationInvalidationPlans.accessKey.rotate)
  } catch {
    void queryClient.invalidateQueries({ queryKey: accessKeyResources.collection.queryKey })
  }
}

async function completeRotation(
  rotation: AccessKeyRotateResultDto,
  signal: AbortSignal,
): Promise<void> {
  result.value = rotation
  current.value = rotation
  if (rotation.replayed) {
    current.value = (await refreshCurrent(signal)) ?? rotation
  }
  emit('rotated', current.value)
  emit('update:operation', null)
  operationState.value = 'idle'
}

async function confirmRotate(): Promise<void> {
  if (pending.value) return
  if (result.value) {
    setOpen(false)
    return
  }
  pending.value = true
  failed.value = false
  refreshFailed.value = false
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  try {
    const rotation = await rotateAccessKey(
      client,
      props.accessKey.id,
      operationID.value,
      activeController.signal,
    )
    if (controller !== activeController || !open.value) return
    await completeRotation(rotation, activeController.signal)
  } catch (error: unknown) {
    if (controller !== activeController || !open.value || error instanceof RequestCancelledError) {
      return
    }
    const outcome = classifyMutationOutcome({ kind: 'error', error, requestSent: true })
    if (outcome.kind === 'indeterminate' || outcome.kind === 'reconciling') {
      operationState.value = outcome.kind
      emit('update:operation', {
        base: props.accessKey,
        idempotencyKey: operationID.value,
        state: outcome.kind,
      })
    } else if (outcome.kind === 'failed' && outcome.reason === 'expired-known') {
      const latest = await refreshCurrent(activeController.signal)
      if (latest) {
        current.value = latest
        result.value = { ...latest, replayed: true }
        emit('rotated', latest)
        emit('update:operation', null)
        operationState.value = 'idle'
      } else {
        operationState.value = 'indeterminate'
        emit('update:operation', {
          base: props.accessKey,
          idempotencyKey: operationID.value,
          state: 'indeterminate',
        })
      }
    } else if (outcome.kind === 'failed' && outcome.reason === 'retryable-precondition') {
      operationState.value = 'reconciling'
      emit('update:operation', {
        base: props.accessKey,
        idempotencyKey: operationID.value,
        state: 'reconciling',
      })
    } else {
      failed.value = true
      if (outcome.kind === 'failed' && outcome.reason === 'rejected') {
        operationID.value = createUUID()
        operationState.value = 'idle'
        emit('update:operation', null)
      }
    }
  } finally {
    if (controller === activeController) {
      controller = undefined
      pending.value = false
    }
  }
}

onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <AppConfirmDialog
    :open="open"
    :title="t('accessKeys.rotate.title')"
    :description="t('accessKeys.rotate.description', { name: accessKey.name })"
    :close-label="t('accessKeys.rotate.close')"
    :cancel-label="result ? t('common.close') : t('common.cancel')"
    :confirm-label="confirmLabel"
    tone="danger"
    description-tone="warning"
    :pending="pending"
    @update:open="setOpen"
    @confirm="confirmRotate"
  >
    <template #trigger>
      <AppButton variant="secondary" size="compact" :disabled="disabled" @click="openDialog">
        <RotateCcw :size="16" aria-hidden="true" />
        {{ triggerLabel }}
      </AppButton>
    </template>

    <div class="access-key-rotate__body">
      <InlineFeedback tone="warning" appearance="hint">
        {{ t('accessKeys.rotate.impact') }}
      </InlineFeedback>
      <InlineFeedback v-if="feedbackKey" tone="warning">
        {{ t(feedbackKey) }}
      </InlineFeedback>
      <InlineFeedback v-if="failed" tone="danger">{{
        t('accessKeys.rotate.failed')
      }}</InlineFeedback>
      <InlineFeedback v-if="refreshFailed" tone="warning">
        {{ t('accessKeys.rotate.refreshFailed') }}
      </InlineFeedback>
      <div v-if="result" class="access-key-rotate__result" aria-live="polite">
        <strong>{{
          t(result.key ? 'accessKeys.rotate.newKey' : 'accessKeys.rotate.currentKey')
        }}</strong>
        <CopyChip
          :value="displayKey"
          :label="t('accessKeys.copy')"
          :success-label="t('common.copied')"
          :failure-label="t('common.copyFailed')"
          :resolve-value="result.key ? undefined : resolveCurrentKey"
        />
        <small>{{
          t(result.key ? 'accessKeys.rotate.newKeyHint' : 'accessKeys.rotate.replayedHint')
        }}</small>
      </div>
    </div>
  </AppConfirmDialog>
</template>

<style scoped>
.access-key-rotate__body,
.access-key-rotate__result {
  display: grid;
  gap: 10px;
}

.access-key-rotate__result {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 10px 12px;
}

.access-key-rotate__result strong {
  font-size: var(--text-sm);
}

.access-key-rotate__result small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
</style>
