import { useQueryClient } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { RequestCancelledError } from '@/api/errors'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import {
  projectModelPriceMutationIssue,
  updateModelPrice,
  type ModelPriceDto,
} from '@/app/resources/model-prices'
import { useUnsavedChanges } from '@/app/unsaved-changes'

import {
  buildModelPriceRequest,
  createEmptyTierDraft,
  createModelPriceDraft,
  modelPriceDraftChanged,
  modelPriceDraftIsAllNull,
  modelPriceFormHasErrors,
  validateModelPriceDraft,
  type ModelPriceDraft,
} from './model-price-form'

export function useModelPriceEditor(row: Ref<ModelPriceDto>) {
  const client = useApiClient()
  const queryClient = useQueryClient()
  const { t } = useI18n()

  const baseline = ref<ModelPriceDto>(row.value)
  const draft = ref<ModelPriceDraft>(createModelPriceDraft(row.value))
  const pending = ref(false)
  const failure = ref('')
  const unpricedConfirmOpen = ref(false)
  let controller: AbortController | undefined

  const errors = computed(() => validateModelPriceDraft(draft.value))
  const hasErrors = computed(() => modelPriceFormHasErrors(errors.value))
  const changed = computed(() => modelPriceDraftChanged(baseline.value, draft.value))
  const allNull = computed(() => modelPriceDraftIsAllNull(draft.value))
  const ownershipIntent = computed(
    () => allNull.value && baseline.value.method !== 'user_marked_unpriced',
  )
  const canSave = computed(() => !hasErrors.value && (changed.value || ownershipIntent.value))
  const unsavedChanges = useUnsavedChanges(changed, { blocked: pending })

  function clearRequest(): void {
    controller?.abort()
    controller = undefined
    pending.value = false
  }

  function resetDraft(): void {
    clearRequest()
    baseline.value = row.value
    draft.value = createModelPriceDraft(baseline.value)
    failure.value = ''
    unpricedConfirmOpen.value = false
  }

  watch(
    () => [row.value.id, row.value.updated_at_ms] as const,
    ([id, updatedAtMS]) => {
      if (id !== baseline.value.id) {
        resetDraft()
        return
      }
      if (updatedAtMS === baseline.value.updated_at_ms) return
      if (!changed.value) resetDraft()
    },
    { immediate: true },
  )

  function scheduleDraft(mode?: string) {
    return mode ? draft.value.modeSchedules[mode] : draft.value
  }

  function addTier(mode?: string): void {
    scheduleDraft(mode)?.tiers.push(createEmptyTierDraft())
  }

  function removeTier(key: string, mode?: string): void {
    const schedule = scheduleDraft(mode)
    if (schedule) schedule.tiers = schedule.tiers.filter((tier) => tier.key !== key)
  }

  function failureMessage(error: unknown): string {
    try {
      const issue = projectModelPriceMutationIssue(error)
      if (issue?.code === 'MODEL_PRICE_UNPRICED_CONFIRMATION_REQUIRED') {
        unpricedConfirmOpen.value = true
        return ''
      }
    } catch {
      return t('modelPrices.matrix.saveFailed')
    }
    return t('modelPrices.matrix.saveFailed')
  }

  function canSubmit(confirmUnpriced: boolean): boolean {
    if (hasErrors.value) return false
    if (allNull.value) return confirmUnpriced && (changed.value || ownershipIntent.value)
    return !confirmUnpriced && changed.value
  }

  async function save(confirmUnpriced: boolean): Promise<void> {
    const request = buildModelPriceRequest(draft.value, confirmUnpriced)
    if (request === null || !canSubmit(confirmUnpriced) || pending.value) return
    pending.value = true
    failure.value = ''
    controller?.abort()
    controller = new AbortController()
    const activeController = controller
    try {
      const updated = await updateModelPrice(
        client,
        baseline.value.id,
        request,
        activeController.signal,
      )
      if (controller !== activeController) return
      baseline.value = updated
      draft.value = createModelPriceDraft(updated)
      await applyInvalidationPlan(queryClient, mutationInvalidationPlans.modelPrice.update)
      if (controller !== activeController) return
      unpricedConfirmOpen.value = false
    } catch (error: unknown) {
      if (
        controller === activeController &&
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

  function requestSave(): void {
    if (pending.value) return
    if (allNull.value) {
      unpricedConfirmOpen.value = true
      return
    }
    if (canSave.value) void save(false)
  }

  function confirmUnpricedSave(): void {
    void save(true)
  }

  function cancel(): void {
    resetDraft()
  }

  async function confirmDiscardSwitch(): Promise<boolean> {
    return unsavedChanges.confirmDiscard()
  }

  onBeforeUnmount(clearRequest)

  return {
    draft,
    errors,
    pending,
    failure,
    changed,
    canSave,
    allNull,
    unpricedConfirmOpen,
    addTier,
    removeTier,
    requestSave,
    confirmUnpricedSave,
    cancel,
    confirmDiscardSwitch,
  }
}

export type ModelPriceEditor = ReturnType<typeof useModelPriceEditor>
