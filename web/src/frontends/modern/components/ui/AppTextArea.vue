<script setup lang="ts">
import { controlAttrs, layoutAttrs } from './field-attrs'
import { ref } from 'vue'
import AppField from './AppField.vue'
import AppFieldControl from './AppFieldControl.vue'
import type { ControlSize, FieldProps } from './types'
import './textarea.css'

defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<FieldProps & { rows?: number; mono?: boolean; size?: ControlSize }>(),
  {
    rows: 5,
    size: 'md',
  },
)
const model = defineModel<string>({ required: true })
const input = ref<HTMLTextAreaElement>()
defineExpose({ focus: () => input.value?.focus(), select: () => input.value?.select() })
</script>

<template>
  <AppField v-slot="{ id, describedBy, invalid }" v-bind="{ ...props, ...layoutAttrs($attrs) }">
    <AppFieldControl as-child :invalid="invalid" :disabled="disabled" :size="size">
      <textarea
        :id="id"
        ref="input"
        v-model="model"
        v-bind="controlAttrs($attrs)"
        class="modern-textarea modern-resizable-textarea"
        :class="[{ 'is-mono': mono }, `modern-textarea--${size}`]"
        :rows="rows"
        :disabled="disabled"
        :aria-invalid="invalid || undefined"
        :aria-describedby="describedBy"
      />
    </AppFieldControl>
  </AppField>
</template>

<style scoped>
.modern-textarea {
  display: block;
  outline: none;
  resize: vertical;
  padding: var(--modern-space-2) var(--modern-space-3);
  line-height: var(--modern-leading-body);
}
.modern-textarea.is-mono {
  font-family: var(--modern-font-mono);
}
.modern-textarea--xs {
  padding: var(--modern-space-1) var(--modern-space-2);
  line-height: var(--modern-leading-compact);
}
.modern-textarea::placeholder {
  color: var(--modern-control-placeholder);
  opacity: 1;
}
@media (max-width: 760px) {
  .modern-textarea {
    font-size: var(--modern-font-size-input-mobile);
  }
}
</style>
