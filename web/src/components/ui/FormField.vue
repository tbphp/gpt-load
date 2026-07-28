<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  id: string
  label: string
  description?: string
  error?: string
  required?: boolean
  requiredText?: string
  disabledReason?: string
}>()

const descriptionId = computed(() => (props.description ? `${props.id}-description` : undefined))
const errorId = computed(() => (props.error ? `${props.id}-error` : undefined))
const disabledReasonId = computed(() =>
  props.disabledReason ? `${props.id}-disabled-reason` : undefined,
)
const describedBy = computed(
  () =>
    [descriptionId.value, disabledReasonId.value, errorId.value].filter(Boolean).join(' ') ||
    undefined,
)
</script>

<template>
  <div class="form-field">
    <label class="form-field__label" :for="id">
      {{ label }}
      <template v-if="required">
        <span aria-hidden="true">*</span>
        <span v-if="requiredText" class="sr-only">{{ requiredText }}</span>
      </template>
    </label>
    <slot
      :described-by="describedBy"
      :description-id="descriptionId"
      :disabled-reason-id="disabledReasonId"
      :error-id="errorId"
      :invalid="Boolean(error)"
      :required="Boolean(required)"
    />
    <p v-if="description" :id="descriptionId" class="form-field__description">
      {{ description }}
    </p>
    <p
      v-if="disabledReason"
      :id="disabledReasonId"
      class="form-field__description form-field__disabled-reason"
    >
      {{ disabledReason }}
    </p>
    <p v-if="error" :id="errorId" class="form-field__error" role="alert">
      <span aria-hidden="true">▲</span>
      <span>{{ error }}</span>
    </p>
  </div>
</template>
