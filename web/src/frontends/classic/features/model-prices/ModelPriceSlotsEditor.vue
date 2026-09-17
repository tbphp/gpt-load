<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppTextInput from '@/components/ui/AppTextInput.vue'

import {
  modelPriceFields,
  type ModelPriceField,
  type ModelPriceSlotDraft,
  type ModelPriceSlotErrors,
} from './model-price-form'

const props = withDefaults(
  defineProps<{
    errors: ModelPriceSlotErrors
    pending: boolean
    idPrefix?: string
  }>(),
  { idPrefix: 'model-price-slots' },
)
const draft = defineModel<ModelPriceSlotDraft>('draft', { required: true })
const { t } = useI18n()

function fieldError(field: ModelPriceField): string | undefined {
  const code = props.errors[field]
  return code ? t(`modelPrices.matrix.errors.${code}`) : undefined
}

const errorMessages = computed(() => [
  ...new Set(
    modelPriceFields
      .map((field) => fieldError(field))
      .filter((message): message is string => Boolean(message)),
  ),
])
const errorID = computed(() =>
  errorMessages.value.length > 0 ? `${props.idPrefix}-errors` : undefined,
)
</script>

<template>
  <div class="model-price-slots">
    <div v-for="field in modelPriceFields" :key="field" class="model-price-slots__field">
      <label :for="`${idPrefix}-${field}`">{{ t(`modelPrices.fields.${field}`) }}</label>
      <AppTextInput
        :id="`${idPrefix}-${field}`"
        v-model="draft[field]"
        :label="t(`modelPrices.fields.${field}`)"
        appearance="surface"
        size="compact"
        monospace
        inputmode="decimal"
        :disabled="pending"
        :invalid="Boolean(fieldError(field))"
        :described-by="fieldError(field) ? errorID : undefined"
      />
    </div>
    <p v-if="errorMessages.length > 0" :id="errorID" class="model-price-slots__error" role="alert">
      {{ errorMessages.join(' · ') }}
    </p>
  </div>
</template>

<style scoped>
.model-price-slots {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-1-75) var(--space-2);
}

.model-price-slots__field {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-price-slots__error {
  grid-column: 1 / -1;
  margin: 0;
  color: var(--color-danger);
  font-size: var(--text-label-xs);
}

@media (max-width: 560px) {
  .model-price-slots {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
