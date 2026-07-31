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

<style scoped>
.form-field {
  display: grid;
  gap: 6px;
}

.form-field__label {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 560;
}

.form-field__label > span[aria-hidden='true'] {
  color: var(--color-danger);
}

.form-field :deep(input),
.form-field :deep(textarea) {
  width: 100%;
  min-height: var(--control-lg);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  outline: 0;
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 11px;
  font: inherit;
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    box-shadow var(--duration-fast) var(--easing-standard),
    opacity var(--duration-fast) var(--easing-standard);
}

.form-field :deep(textarea) {
  min-height: 96px;
  padding-block: 9px;
  resize: vertical;
}

.form-field :deep(input[type='password']) {
  font-family: var(--font-mono);
}

.form-field :deep(input:focus-visible),
.form-field :deep(textarea:focus-visible) {
  border-color: var(--color-action);
  border-radius: var(--radius-control);
  outline: 0;
  box-shadow: var(--focus-ring);
}

.form-field :deep(input:disabled),
.form-field :deep(textarea:disabled) {
  cursor: not-allowed;
  opacity: 0.55;
}

.form-field__description,
.form-field__error {
  margin: 0;
  font-size: var(--text-sm);
  line-height: var(--line-normal);
}

.form-field__description {
  color: var(--color-text-faint);
}

.form-field__error {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  color: var(--color-danger);
}
</style>
