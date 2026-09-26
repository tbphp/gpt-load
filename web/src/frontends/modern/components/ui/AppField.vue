<script setup lang="ts">
import { layoutAttrs } from './field-attrs'
import { computed, useId, useSlots } from 'vue'
import type { FieldProps } from './types'

defineOptions({ inheritAttrs: false })
const props = defineProps<FieldProps>()
const slots = useSlots()
const fallbackId = useId()
const fieldId = computed(() => props.id ?? fallbackId)
const labelExtraId = computed(() => `${fieldId.value}-label-extra`)
const descriptionId = computed(() => `${fieldId.value}-description`)
const errorId = computed(() => `${fieldId.value}-error`)
const describedBy = computed(
  () =>
    [
      props.describedBy,
      slots['label-extra'] && labelExtraId.value,
      (props.description || props.descriptionWarning) && descriptionId.value,
      props.error && errorId.value,
    ]
      .filter(Boolean)
      .join(' ') || undefined,
)
</script>

<template>
  <div v-bind="layoutAttrs($attrs)" class="modern-field" :class="{ 'is-disabled': disabled }">
    <div v-if="$slots['label-extra']" class="modern-field-heading">
      <label :id="`${fieldId}-label`" :for="fieldId" :class="{ 'modern-sr-only': labelHidden }">{{
        label
      }}</label>
      <div :id="labelExtraId" class="modern-field-label-extra"><slot name="label-extra" /></div>
    </div>
    <label
      v-else
      :id="`${fieldId}-label`"
      :for="fieldId"
      :class="{ 'modern-sr-only': labelHidden }"
      >{{ label }}</label
    >
    <slot v-bind="{ id: fieldId, describedBy, invalid: Boolean(error || invalid) }" />
    <p
      v-if="description || descriptionWarning"
      :id="descriptionId"
      class="modern-field-description"
    >
      {{ description }}
      <span v-if="descriptionWarning" class="modern-field-warning" aria-live="polite">
        {{ description ? ' · ' : '' }}{{ descriptionWarning }}
      </span>
    </p>
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
.modern-field-heading {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-3);
}
.modern-field-heading > label {
  flex-shrink: 0;
}
.modern-field-label-extra {
  display: flex;
  flex: 1;
  min-width: 0;
  align-items: center;
  justify-content: flex-end;
  gap: var(--modern-space-1-5);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
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
.modern-field-warning {
  color: var(--modern-warning);
}
</style>
