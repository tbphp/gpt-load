<script setup lang="ts">
import { layoutAttrs } from './field-attrs'
import { computed, useId } from 'vue'
import type { FieldProps } from './types'

defineOptions({ inheritAttrs: false })
const props = defineProps<FieldProps>()
const fallbackId = useId()
const fieldId = computed(() => props.id ?? fallbackId)
const descriptionId = computed(() => `${fieldId.value}-description`)
const errorId = computed(() => `${fieldId.value}-error`)
const describedBy = computed(
  () =>
    [props.describedBy, props.description && descriptionId.value, props.error && errorId.value]
      .filter(Boolean)
      .join(' ') || undefined,
)
</script>

<template>
  <div v-bind="layoutAttrs($attrs)" class="modern-field" :class="{ 'is-disabled': disabled }">
    <label :id="`${fieldId}-label`" :for="fieldId" :class="{ 'modern-sr-only': labelHidden }">{{
      label
    }}</label>
    <slot v-bind="{ id: fieldId, describedBy, invalid: Boolean(error || invalid) }" />
    <p v-if="description" :id="descriptionId" class="modern-field-description">{{ description }}</p>
    <p v-if="error" :id="errorId" class="modern-field-error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.modern-field {
  display: grid;
  align-content: start;
  min-width: 0;
  gap: var(--modern-space-1-5);
}
.modern-field label {
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
  line-height: var(--modern-leading-compact);
}
.modern-field.is-disabled label,
.modern-field-description {
  color: var(--modern-muted);
}
.modern-field-description,
.modern-field-error {
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
  overflow-wrap: anywhere;
}
.modern-field-error {
  color: var(--modern-danger);
}
</style>
