<script setup lang="ts">
import { Save } from '@lucide/vue'
import { useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { putModelPrice, type ModelPriceRuleDto } from '@/app/resources/model-prices'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import { RequestCancelledError } from '@/api/errors'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import FormField from '@/components/ui/FormField.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'

import {
  buildModelPriceRequest,
  createModelPriceDraft,
  modelPriceFields,
  validateModelPriceDraft,
  type ModelPriceDraft,
  type ModelPriceField,
} from './model-price-form'

const props = defineProps<{
  open: boolean
  rule: ModelPriceRuleDto | null
}>()
const emit = defineEmits<{ 'update:open': [open: boolean] }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const patternInput = ref<HTMLInputElement>()
const globalConfirmHeading = ref<HTMLHeadingElement>()
const draft = ref<ModelPriceDraft>(createModelPriceDraft())
const initialDraft = ref<ModelPriceDraft>(createModelPriceDraft())
const globalConfirmOpen = ref(false)
const pending = ref(false)
const failed = ref(false)
let controller: AbortController | undefined

const errors = computed(() => validateModelPriceDraft(draft.value))
const requestBody = computed(() => buildModelPriceRequest(draft.value))
const isGlobal = computed(() => draft.value.pattern === '*')
const formChanged = computed(
  () => JSON.stringify(draft.value) !== JSON.stringify(initialDraft.value),
)
const canSave = computed(
  () => requestBody.value !== null && (props.rule?.source === 'builtin' || formChanged.value),
)
const title = computed(() => {
  if (props.rule?.source === 'builtin') return t('modelPrices.drawer.builtinTitle')
  if (props.rule?.source === 'user') return t('modelPrices.drawer.editTitle')
  return t('modelPrices.drawer.addTitle')
})
const unsavedChanges = useUnsavedChanges(formChanged, { blocked: pending })

function fieldError(field: ModelPriceField): string | undefined {
  return errors.value.fields[field]
    ? t(`modelPrices.drawer.errors.${errors.value.fields[field]}`)
    : undefined
}

function patternError(): string | undefined {
  return errors.value.pattern ? t(`modelPrices.drawer.errors.${errors.value.pattern}`) : undefined
}

function setPrice(field: ModelPriceField, event: Event): void {
  draft.value[field] = (event.target as HTMLInputElement).value
}

function clearRequest(): void {
  controller?.abort()
  controller = undefined
  pending.value = false
}

async function resetForOpen(): Promise<void> {
  clearRequest()
  const nextDraft = createModelPriceDraft(props.rule)
  draft.value = nextDraft
  initialDraft.value = { ...nextDraft }
  globalConfirmOpen.value = false
  failed.value = false
  await nextTick()
  await nextTick()
  patternInput.value?.focus()
}

function setOpen(open: boolean): void {
  if (!open && !unsavedChanges.confirmDiscard()) return
  if (!open) {
    clearRequest()
    failed.value = false
    globalConfirmOpen.value = false
  }
  emit('update:open', open)
}

watch(
  () => [props.open, props.rule] as const,
  ([open]) => {
    if (open) void resetForOpen()
    else clearRequest()
  },
  { immediate: true },
)
watch(
  () => draft.value.pattern,
  (pattern) => {
    if (pattern !== '*') globalConfirmOpen.value = false
  },
)

function requestSave(): void {
  if (!canSave.value || pending.value) return
  if (isGlobal.value) {
    void openGlobalConfirmation()
    return
  }
  void save()
}

async function openGlobalConfirmation(): Promise<void> {
  globalConfirmOpen.value = true
  await nextTick()
  await nextTick()
  globalConfirmHeading.value?.focus()
}

function setGlobalConfirmOpen(open: boolean): void {
  if (!open && pending.value) return
  globalConfirmOpen.value = open
}

async function save(): Promise<void> {
  const body = requestBody.value
  if (pending.value || body === null || !canSave.value) return
  pending.value = true
  failed.value = false
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  try {
    await putModelPrice(client, body.pattern, body.prices, activeController.signal)
    if (controller !== activeController || !props.open) return
    await applyInvalidationPlan(queryClient, mutationInvalidationPlans.modelPrice.upsert)
    if (controller !== activeController || !props.open) return
    globalConfirmOpen.value = false
    initialDraft.value = { ...draft.value }
    controller = undefined
    pending.value = false
    setOpen(false)
  } catch (error: unknown) {
    if (
      controller === activeController &&
      props.open &&
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
  <AppDrawer
    :open="open"
    :title="title"
    :description="t('modelPrices.drawer.description')"
    :close-label="t('modelPrices.drawer.close')"
    :dismissible="!pending"
    @update:open="setOpen"
  >
    <template #trigger><slot name="trigger" /></template>

    <form v-if="!globalConfirmOpen" class="model-price-drawer" @submit.prevent="requestSave">
      <InlineFeedback v-if="failed" tone="danger">
        {{ t('modelPrices.drawer.saveFailed') }}
      </InlineFeedback>
      <InlineFeedback tone="info">
        {{ t('modelPrices.drawer.wholeReplacement') }}
      </InlineFeedback>

      <FormField
        id="model-price-pattern"
        :label="t('modelPrices.drawer.pattern')"
        :description="
          rule
            ? t('modelPrices.drawer.patternReadonly')
            : t('modelPrices.drawer.patternDescription')
        "
        :error="patternError()"
      >
        <template #default="{ describedBy }">
          <input
            id="model-price-pattern"
            ref="patternInput"
            v-model="draft.pattern"
            data-test="model-price-pattern"
            class="model-price-drawer__input mono"
            type="text"
            autocomplete="off"
            :readonly="rule !== null"
            :disabled="pending"
            :aria-describedby="describedBy"
            :aria-invalid="errors.pattern ? 'true' : undefined"
          />
        </template>
      </FormField>

      <fieldset class="model-price-drawer__prices">
        <legend>{{ t('modelPrices.drawer.prices') }}</legend>
        <p>{{ t('modelPrices.drawer.priceDescription') }}</p>
        <FormField
          v-for="field in modelPriceFields"
          :id="`model-price-${field}`"
          :key="field"
          :label="t(`modelPrices.fields.${field}`)"
          :error="fieldError(field)"
        >
          <template #default="{ describedBy }">
            <input
              :id="`model-price-${field}`"
              :data-test="`model-price-${field}`"
              class="model-price-drawer__input mono"
              type="number"
              min="0"
              step="any"
              inputmode="decimal"
              :disabled="pending"
              :value="draft[field]"
              :aria-describedby="describedBy"
              :aria-invalid="errors.fields[field] ? 'true' : undefined"
              @input="setPrice(field, $event)"
            />
          </template>
        </FormField>
        <p v-if="errors.prices" class="model-price-drawer__group-error" role="alert">
          {{ t('modelPrices.drawer.errors.all_empty') }}
        </p>
      </fieldset>

      <InlineFeedback v-if="isGlobal" tone="warning">
        {{ t('modelPrices.drawer.globalWarning') }}
      </InlineFeedback>

      <div class="model-price-drawer__actions">
        <AppButton variant="secondary" :disabled="pending" @click="setOpen(false)">
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton data-test="model-price-save" type="submit" :busy="pending" :disabled="!canSave">
          <Save :size="16" aria-hidden="true" />{{ t('modelPrices.drawer.save') }}
        </AppButton>
      </div>
    </form>

    <section
      v-else
      class="model-price-drawer__global-confirm"
      data-test="model-price-global-confirm"
    >
      <h2 ref="globalConfirmHeading" data-test="model-price-global-confirm-heading" tabindex="-1">
        {{ t('modelPrices.drawer.globalDialog.title') }}
      </h2>
      <p>{{ t('modelPrices.drawer.globalDialog.description') }}</p>
      <ul>
        <li>{{ t('modelPrices.drawer.globalDialog.precedence') }}</li>
        <li>{{ t('modelPrices.drawer.globalDialog.noFallback') }}</li>
        <li>{{ t('modelPrices.drawer.globalDialog.futureOnly') }}</li>
        <li>{{ t('modelPrices.drawer.globalDialog.reset') }}</li>
      </ul>
      <InlineFeedback v-if="failed" tone="danger">
        {{ t('modelPrices.drawer.saveFailed') }}
      </InlineFeedback>
      <div class="model-price-drawer__actions">
        <AppButton variant="secondary" :disabled="pending" @click="setGlobalConfirmOpen(false)">
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton
          data-test="model-price-global-save-confirm"
          :busy="pending"
          :disabled="pending"
          @click="save"
        >
          <Save :size="16" aria-hidden="true" />{{ t('modelPrices.drawer.globalDialog.confirm') }}
        </AppButton>
      </div>
    </section>
  </AppDrawer>
</template>

<style scoped>
.model-price-drawer {
  display: grid;
  gap: var(--space-5);
  font-size: 1rem;
}
.model-price-drawer__input {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
.model-price-drawer__input[readonly] {
  color: var(--color-text-muted);
  cursor: not-allowed;
}
.model-price-drawer__prices {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
  margin: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  padding: var(--space-4);
}
.model-price-drawer__prices legend {
  padding: 0 var(--space-1);
  font-weight: 700;
}
.model-price-drawer__prices > p {
  margin: 0;
  color: var(--color-text-muted);
}
.model-price-drawer__group-error {
  color: var(--color-danger) !important;
}
.model-price-drawer__global-confirm {
  display: grid;
  gap: var(--space-4);
}
.model-price-drawer__global-confirm h2,
.model-price-drawer__global-confirm p {
  margin: 0;
}
.model-price-drawer__global-confirm p {
  color: var(--color-text-muted);
}
.model-price-drawer__global-confirm ul {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding-left: var(--space-5);
}
.model-price-drawer__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
}
</style>
