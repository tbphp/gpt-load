<script setup lang="ts">
import { controlAttrs, layoutAttrs } from './field-attrs'
import AppField from './AppField.vue'
import AppSegmentedControl from './AppSegmentedControl.vue'
import type { ControlSize, FieldProps, SelectOption } from './types'

defineOptions({ inheritAttrs: false })
const props = defineProps<
  FieldProps & {
    options: readonly SelectOption[]
    size?: ControlSize
    name?: string
    required?: boolean
  }
>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <AppField v-slot="{ id, describedBy, invalid }" v-bind="{ ...props, ...layoutAttrs($attrs) }">
    <AppSegmentedControl
      v-bind="controlAttrs($attrs)"
      :id="id"
      v-model="model"
      appearance="field"
      :label="label"
      :options="options"
      :size="size"
      :disabled="disabled"
      :name="name"
      :required="required"
      :aria-labelledby="`${id}-label`"
      :aria-describedby="describedBy"
      :aria-invalid="invalid || undefined"
    />
  </AppField>
</template>
