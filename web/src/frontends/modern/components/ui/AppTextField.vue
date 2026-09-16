<script setup lang="ts">
import { controlAttrs, layoutAttrs } from './field-attrs'
import { useLoadingActivity } from './loading'
import { ref, type Component } from 'vue'
import AppField from './AppField.vue'
import AppFieldControl from './AppFieldControl.vue'
import AppIcon from './AppIcon.vue'
import type { ControlSize, FieldProps } from './types'

defineOptions({ inheritAttrs: false })
const props = defineProps<
  FieldProps & { icon?: Component; size?: ControlSize; loading?: boolean }
>()
const model = defineModel<string>({ required: true })
const input = ref<HTMLInputElement>()
useLoadingActivity(() => Boolean(props.loading))
defineExpose({
  focus: () => input.value?.focus({ preventScroll: true }),
  select: () => input.value?.select(),
})
</script>

<template>
  <AppField v-slot="{ id, describedBy, invalid }" v-bind="{ ...props, ...layoutAttrs($attrs) }">
    <AppFieldControl
      class="modern-text-field-control"
      :invalid="invalid"
      :disabled="disabled"
      :size="size"
    >
      <AppIcon v-if="icon" :icon="icon" size="sm" :label="label" />
      <input
        v-bind="controlAttrs($attrs)"
        :id="id"
        ref="input"
        v-model="model"
        :type="($attrs.type as string) ?? 'text'"
        :disabled="disabled"
        :aria-invalid="invalid || undefined"
        :aria-describedby="describedBy"
        :aria-busy="loading || undefined"
      />
      <slot name="suffix" />
    </AppFieldControl>
  </AppField>
</template>

<style scoped>
.modern-text-field-control {
  position: relative;
}
.modern-text-field-control > svg {
  color: var(--modern-muted);
}
.modern-text-field-control input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
  padding: var(--modern-space-1) 0;
  color: var(--modern-text);
  font-size: inherit;
  line-height: var(--modern-leading-compact);
}
.modern-text-field-control input:disabled {
  color: var(--modern-muted);
  cursor: not-allowed;
}
.modern-text-field-control input::placeholder {
  color: var(--modern-control-placeholder);
  opacity: 1;
}
@media (max-width: 760px) {
  .modern-text-field-control input {
    font-size: var(--modern-font-size-input-mobile);
  }
}
</style>
