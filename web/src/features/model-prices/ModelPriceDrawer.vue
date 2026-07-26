<script setup lang="ts">
import { Save } from 'lucide-vue-next'
import { useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { putModelPrice, type ModelPriceRuleDto } from '@/api/control/model-prices'
import { RequestCancelledError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
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
const draft = ref<ModelPriceDraft>(createModelPriceDraft())
const initialDraft = ref<ModelPriceDraft>(createModelPriceDraft())
const globalConfirmed = ref(false)
const pending = ref(false)
const failed = ref(false)
let controller: AbortController | undefined

const errors = computed(() => validateModelPriceDraft(draft.value))
const requestBody = computed(() => buildModelPriceRequest(draft.value))
const isGlobal = computed(() => draft.value.pattern === '*')
const dirty = computed(
  () =>
    props.rule?.source !== 'user' ||
    JSON.stringify(draft.value) !== JSON.stringify(initialDraft.value),
)
const valid = computed(
  () =>
    requestBody.value !== null &&
    dirty.value &&
    (!isGlobal.value || globalConfirmed.value),
)
const title = computed(() => {
  if (props.rule?.source === 'builtin') return t('modelPrices.drawer.builtinTitle')
  if (props.rule?.source === 'user') return t('modelPrices.drawer.editTitle')
  return t('modelPrices.drawer.addTitle')
})

function fieldError(field: ModelPriceField): string | undefined {
  return errors.value.fields[field]
    ? t(`modelPrices.drawer.errors.${errors.value.fields[field]}`)
    : undefined
}

function patternError(): string | undefined {
  return errors.value.pattern
    ? t(`modelPrices.drawer.errors.${errors.value.pattern}`)
    : undefined
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
  globalConfirmed.value = false
  failed.value = false
  await nextTick()
  await nextTick()
  patternInput.value?.focus()
}

function setOpen(open: boolean): void {
  if (!open) {
    clearRequest()
    failed.value = false
    globalConfirmed.value = false
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
    if (pattern !== '*') globalConfirmed.value = false
  },
)

async function save(): Promise<void> {
  const body = requestBody.value
  if (pending.value || body === null || !valid.value) return
  pending.value = true
  failed.value = false
  controller?.abort()
  controller = new AbortController()
  const activeController = controller
  try {
    await putModelPrice(client, body.pattern, body.prices, activeController.signal)
    if (controller !== activeController || !props.open) return
    await queryClient.invalidateQueries({ queryKey: controlQueryKeys.modelPrices() })
    if (controller !== activeController || !props.open) return
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
    @update:open="setOpen"
  >
    <template #trigger><slot name="trigger" /></template>

    <form class="model-price-drawer" @submit.prevent="save">
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
          rule ? t('modelPrices.drawer.patternReadonly') : t('modelPrices.drawer.patternDescription')
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
      <label v-if="isGlobal" class="model-price-drawer__global-confirm">
        <input
          v-model="globalConfirmed"
          data-test="model-price-global-confirm"
          type="checkbox"
          :disabled="pending"
        />
        <span>{{ t('modelPrices.drawer.globalConfirm') }}</span>
      </label>

      <div class="model-price-drawer__actions">
        <AppButton variant="secondary" :disabled="pending" @click="setOpen(false)">
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton
          data-test="model-price-save"
          type="submit"
          :busy="pending"
          :disabled="!valid"
        >
          <Save :size="16" aria-hidden="true" />{{ t('modelPrices.drawer.save') }}
        </AppButton>
      </div>
    </form>
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
  border: 1px solid var(--color-border);
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
  display: flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  font-weight: 650;
  cursor: pointer;
}
.model-price-drawer__global-confirm input {
  width: 20px;
  height: 20px;
}
.model-price-drawer__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
}
</style>
