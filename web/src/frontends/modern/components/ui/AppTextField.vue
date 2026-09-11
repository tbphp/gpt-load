<script setup lang="ts">
import { computed, ref, useId } from 'vue'

defineOptions({ inheritAttrs: false })
const props = defineProps<{
  label: string
  id?: string
  error?: string
  invalid?: boolean
  describedBy?: string
}>()
const model = defineModel<string>({ required: true })
const fallbackId = useId()
const inputId = computed(() => props.id ?? fallbackId)
const input = ref<HTMLInputElement>()
const description = computed(
  () =>
    [props.describedBy, props.error ? `${inputId.value}-error` : undefined]
      .filter(Boolean)
      .join(' ') || undefined,
)

defineExpose({ focus: () => input.value?.focus({ preventScroll: true }) })
</script>

<template>
  <div class="modern-text-field">
    <label :for="inputId">{{ label }}</label>
    <div class="modern-text-field-control" :class="{ 'is-invalid': error || invalid }">
      <input
        :id="inputId"
        ref="input"
        v-model="model"
        type="text"
        v-bind="$attrs"
        :aria-invalid="error || invalid ? true : undefined"
        :aria-describedby="description"
      />
      <slot name="suffix" />
    </div>
    <p v-if="error" :id="`${inputId}-error`" class="modern-text-field-error" role="alert">
      {{ error }}
    </p>
  </div>
</template>

<style scoped>
.modern-text-field {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-2);
}
.modern-text-field label {
  font-size: var(--modern-text-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-text-field-control {
  display: flex;
  min-width: 0;
  min-height: var(--modern-control-md);
  align-items: center;
  gap: var(--modern-space-2);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
  padding: 0 var(--modern-space-2);
}
.modern-text-field-control:focus-within {
  border-color: var(--modern-accent);
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
}
.modern-text-field-control.is-invalid {
  border-color: var(--modern-danger);
}
.modern-text-field-control input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
  padding: var(--modern-space-1-5) 0;
  color: var(--modern-text);
  font-size: var(--modern-text-body);
  line-height: var(--modern-leading-compact);
}
.modern-text-field-control input::placeholder {
  color: var(--modern-muted);
  opacity: 1;
}
.modern-text-field-error {
  color: var(--modern-danger);
  font-size: var(--modern-text-small);
}
@media (max-width: 760px) {
  .modern-text-field-control {
    min-height: var(--modern-touch-target);
  }
  .modern-text-field-control input {
    font-size: var(--modern-text-input-mobile);
  }
}
</style>
