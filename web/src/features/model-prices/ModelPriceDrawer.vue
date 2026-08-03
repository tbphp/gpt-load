<script setup lang="ts">
import { Save } from '@lucide/vue'
import { useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { RequestCancelledError } from '@/api/errors'
import {
  projectModelPriceMutationIssue,
  updateModelPrice,
  type ModelPriceDto,
} from '@/app/resources/model-prices'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import {
  buildModelPriceRequest,
  createModelPriceDraft,
  modelPriceDraftIsAllNull,
  modelPriceFields,
  validateModelPriceDraft,
  type ModelPriceDraft,
  type ModelPriceField,
} from './model-price-form'

const props = defineProps<{
  open: boolean
  row: ModelPriceDto
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  saved: []
}>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const firstInput = ref<InstanceType<typeof AppTextInput>>()
const draft = ref<ModelPriceDraft>(createModelPriceDraft(props.row))
const initialDraft = ref<ModelPriceDraft>(createModelPriceDraft(props.row))
const pending = ref(false)
const failure = ref('')
const unpricedConfirmOpen = ref(false)
let controller: AbortController | undefined

const errors = computed(() => validateModelPriceDraft(draft.value))
const changed = computed(() =>
  modelPriceFields.some((field) => draft.value[field] !== initialDraft.value[field]),
)
const allNull = computed(() => modelPriceDraftIsAllNull(draft.value))
const ownershipIntent = computed(() => allNull.value && props.row.method !== 'user_marked_unpriced')
const canSave = computed(
  () => Object.keys(errors.value).length === 0 && (changed.value || ownershipIntent.value),
)
const unsavedChanges = useUnsavedChanges(changed, { blocked: pending })

function setFirstInput(component: unknown): void {
  firstInput.value = component as InstanceType<typeof AppTextInput> | undefined
}

function fieldError(field: ModelPriceField): string | undefined {
  return errors.value[field] ? t(`modelPrices.drawer.errors.${errors.value[field]}`) : undefined
}

function clearRequest(): void {
  controller?.abort()
  controller = undefined
  pending.value = false
}

async function resetForOpen(): Promise<void> {
  clearRequest()
  const nextDraft = createModelPriceDraft(props.row)
  draft.value = nextDraft
  initialDraft.value = { ...nextDraft }
  failure.value = ''
  unpricedConfirmOpen.value = false
  await nextTick()
  firstInput.value?.focus()
}

async function setOpen(open: boolean): Promise<void> {
  if (!open && !(await unsavedChanges.confirmDiscard())) return
  if (!open) {
    clearRequest()
    failure.value = ''
    unpricedConfirmOpen.value = false
  }
  emit('update:open', open)
}

function failureMessage(error: unknown): string {
  try {
    const issue = projectModelPriceMutationIssue(error)
    if (issue?.code === 'MODEL_PRICE_UNPRICED_CONFIRMATION_REQUIRED') {
      unpricedConfirmOpen.value = true
      return ''
    }
  } catch {
    return t('modelPrices.drawer.saveFailed')
  }
  return t('modelPrices.drawer.saveFailed')
}

function requestSave(): void {
  if (!canSave.value || pending.value) return
  if (allNull.value) {
    unpricedConfirmOpen.value = true
    return
  }
  void save(false)
}

function canSubmit(confirmUnpriced: boolean): boolean {
  if (Object.keys(errors.value).length > 0) return false
  if (allNull.value) {
    return confirmUnpriced && (changed.value || ownershipIntent.value)
  }
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
    await updateModelPrice(client, props.row.id, request, activeController.signal)
    if (controller !== activeController || !props.open) return
    await applyInvalidationPlan(queryClient, mutationInvalidationPlans.modelPrice.update)
    if (controller !== activeController || !props.open) return
    initialDraft.value = { ...draft.value }
    unpricedConfirmOpen.value = false
    emit('saved')
    emit('update:open', false)
  } catch (error: unknown) {
    if (
      controller === activeController &&
      props.open &&
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

watch(
  () => [props.open, props.row.id] as const,
  ([open]) => {
    if (open) void resetForOpen()
    else clearRequest()
  },
  { immediate: true },
)

onBeforeUnmount(clearRequest)
</script>

<template>
  <AppDrawer
    appearance="ledger"
    show-description
    :open="open"
    :title="t('modelPrices.drawer.title')"
    :description="t('modelPrices.drawer.description')"
    :close-label="t('modelPrices.drawer.close')"
    :dismissible="!pending"
    @update:open="setOpen"
  >
    <form class="model-price-drawer" @submit.prevent="requestSave">
      <InlineFeedback v-if="failure" appearance="ledger" tone="danger">
        {{ failure }}
      </InlineFeedback>

      <section class="model-price-drawer__identity" aria-labelledby="model-price-identity-title">
        <div>
          <h2 id="model-price-identity-title">{{ t('modelPrices.drawer.identity') }}</h2>
          <p>{{ t('modelPrices.drawer.identityDescription') }}</p>
        </div>
        <dl>
          <div>
            <dt>{{ t('modelPrices.drawer.model') }}</dt>
            <dd>{{ row.model_id }}</dd>
          </div>
          <div>
            <dt>{{ t('modelPrices.drawer.scope') }}</dt>
            <dd>{{ t(`modelPrices.scope.${row.scope.kind}`) }} · {{ row.scope.label }}</dd>
          </div>
          <div>
            <dt>{{ t('modelPrices.drawer.currentStatus') }}</dt>
            <dd>
              <StatusBadge
                size="compact"
                :tone="row.pricing_status === 'configured' ? 'success' : 'warning'"
              >
                {{ t(`modelPrices.status.${row.pricing_status}`) }}
              </StatusBadge>
            </dd>
          </div>
        </dl>
      </section>

      <fieldset class="model-price-drawer__prices">
        <legend>{{ t('modelPrices.drawer.prices') }}</legend>
        <p>{{ t('modelPrices.drawer.priceDescription') }}</p>
        <div class="model-price-drawer__price-grid">
          <FormField
            v-for="(field, index) in modelPriceFields"
            :id="`model-price-${field}`"
            :key="field"
            size="compact"
            :label="t(`modelPrices.fields.${field}`)"
            :label-suffix="t('modelPrices.drawer.unit')"
            :error="fieldError(field)"
          >
            <template #default="{ describedBy, invalid }">
              <AppTextInput
                :id="`model-price-${field}`"
                :ref="index === 0 ? setFirstInput : undefined"
                v-model="draft[field]"
                :label="t(`modelPrices.fields.${field}`)"
                appearance="sunken"
                size="sm"
                monospace
                inputmode="decimal"
                :disabled="pending"
                :invalid="invalid"
                :described-by="describedBy"
              />
            </template>
          </FormField>
        </div>
      </fieldset>

      <InlineFeedback v-if="row.has_context_tiers" appearance="ledger" tone="warning">
        {{ t('modelPrices.drawer.tierNote') }}
      </InlineFeedback>
      <InlineFeedback v-if="row.partial" appearance="ledger" tone="neutral">
        {{ t('modelPrices.drawer.partialNote') }}
      </InlineFeedback>
    </form>

    <template #footer>
      <span class="model-price-drawer__footer-note">
        {{
          allNull ? t('modelPrices.drawer.unpricedState') : t('modelPrices.drawer.availableState')
        }}
      </span>
      <div class="model-price-drawer__actions">
        <AppButton variant="secondary" size="compact" :disabled="pending" @click="setOpen(false)">
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton size="compact" :busy="pending" :disabled="!canSave" @click="requestSave">
          <Save :size="15" aria-hidden="true" />{{ t('modelPrices.drawer.save') }}
        </AppButton>
      </div>
    </template>
  </AppDrawer>

  <AppConfirmDialog
    appearance="ledger"
    tone="danger"
    :open="unpricedConfirmOpen"
    :title="t('modelPrices.drawer.unpricedConfirm.title')"
    :description="t('modelPrices.drawer.unpricedConfirm.description', { model: row.model_id })"
    :close-label="t('modelPrices.drawer.unpricedConfirm.close')"
    :cancel-label="t('common.cancel')"
    :confirm-label="t('modelPrices.drawer.unpricedConfirm.confirm')"
    :pending="pending"
    @update:open="unpricedConfirmOpen = $event"
    @confirm="save(true)"
  >
    <InlineFeedback appearance="ledger" tone="warning">
      {{ t('modelPrices.drawer.unpricedConfirm.warning') }}
    </InlineFeedback>
  </AppConfirmDialog>
</template>

<style scoped>
.model-price-drawer,
.model-price-drawer__identity,
.model-price-drawer__prices {
  display: grid;
}

.model-price-drawer {
  gap: var(--space-5-5);
  padding: var(--space-5) 0;
}

.model-price-drawer__identity,
.model-price-drawer__prices {
  gap: var(--space-3-25);
}

.model-price-drawer__identity h2,
.model-price-drawer__identity p,
.model-price-drawer__prices p,
.model-price-drawer__prices legend,
.model-price-drawer__identity dl,
.model-price-drawer__identity dd {
  margin: 0;
}

.model-price-drawer__identity h2,
.model-price-drawer__prices legend {
  font-size: var(--text-sm);
  font-weight: 650;
}

.model-price-drawer__identity p,
.model-price-drawer__prices > p,
.model-price-drawer__footer-note {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-price-drawer__identity p {
  margin-top: var(--space-0-75);
}

.model-price-drawer__identity dl {
  display: grid;
  border-top: 1px solid var(--color-border-subtle);
}

.model-price-drawer__identity dl > div {
  display: grid;
  min-height: 42px;
  grid-template-columns: minmax(112px, 0.42fr) minmax(0, 1fr);
  align-items: center;
  gap: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-2) var(--space-0-5);
}

.model-price-drawer__identity dt {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-price-drawer__identity dd {
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-price-drawer__prices {
  min-width: 0;
  border: 0;
  border-top: 1px solid var(--color-border-subtle);
  padding: var(--space-4-5) 0 0;
}

.model-price-drawer__prices legend {
  padding: 0;
}

.model-price-drawer__price-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3-25) var(--space-3);
}

.model-price-drawer__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
}

@media (max-width: 520px) {
  .model-price-drawer__price-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .model-price-drawer__footer-note {
    display: none;
  }

  .model-price-drawer__actions {
    width: 100%;
  }

  .model-price-drawer__actions :deep(.app-button) {
    flex: 1;
  }
}
</style>
