<script setup lang="ts">
import { controlAttrs, layoutAttrs } from './field-attrs'
import { Check, ChevronDown } from '@lucide/vue'
import {
  SelectContent,
  SelectItem,
  SelectItemIndicator,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectViewport,
} from 'reka-ui'
import { computed, type Component } from 'vue'
import AppField from './AppField.vue'
import AppFieldControl from './AppFieldControl.vue'
import AppIcon from './AppIcon.vue'
import AppMenuSurface from './AppMenuSurface.vue'
import AppOverflowText from './AppOverflowText.vue'
import { overlaySideOffset } from './overlay'
import type { ControlSize, FieldProps, SelectOption } from './types'

defineOptions({ inheritAttrs: false })
const props = defineProps<
  FieldProps & {
    options: readonly SelectOption[]
    icon?: Component
    size?: ControlSize
    name?: string
    required?: boolean
    tooltip?: boolean
  }
>()
const model = defineModel<string>({ required: true })
// 全部选项使用空字符串；Reka 的可选空值为 null，表单提交仍为原始空字符串。
const selected = computed({
  get: () => (model.value === '' ? null : model.value),
  set: (value: string | null) => {
    model.value = value ?? ''
  },
})
const selectedLabel = computed(
  () => props.options.find((option) => option.value === model.value)?.label ?? model.value,
)
</script>

<template>
  <AppField v-slot="{ id, describedBy, invalid }" v-bind="{ ...props, ...layoutAttrs($attrs) }">
    <SelectRoot v-model="selected" :disabled="disabled" :name="name" :required="required">
      <AppFieldControl as-child :invalid="invalid" :disabled="disabled" :size="size">
        <SelectTrigger
          v-bind="controlAttrs($attrs)"
          :id="id"
          class="modern-select-trigger"
          :class="{ 'is-invalid': invalid }"
          :aria-labelledby="`${id}-label`"
          :aria-invalid="invalid || undefined"
          :aria-describedby="describedBy"
        >
          <AppIcon
            v-if="icon"
            :icon="icon"
            size="sm"
            :label="tooltip === false ? undefined : label"
          />
          <SelectValue class="modern-select-value"
            ><slot name="value" :value="model" :label="selectedLabel"
              ><AppOverflowText :text="selectedLabel" /></slot
          ></SelectValue>
          <AppIcon :icon="ChevronDown" size="sm" :label="tooltip === false ? undefined : label" />
        </SelectTrigger>
      </AppFieldControl>
      <SelectPortal>
        <AppMenuSurface>
          <SelectContent position="popper" align="start" :side-offset="overlaySideOffset">
            <SelectViewport>
              <SelectItem
                v-for="option in options"
                :key="option.value"
                :value="option.value === '' ? null : option.value"
                :disabled="option.disabled"
                :text-value="option.label"
                class="modern-menu-option"
              >
                <SelectItemText
                  ><slot name="option" :option="option">{{ option.label }}</slot></SelectItemText
                >
                <SelectItemIndicator><AppIcon :icon="Check" size="sm" /></SelectItemIndicator>
              </SelectItem>
            </SelectViewport>
          </SelectContent>
        </AppMenuSurface>
      </SelectPortal>
    </SelectRoot>
  </AppField>
</template>

<style scoped>
.modern-select-trigger {
  justify-content: space-between;
  text-align: left;
}
.modern-select-value {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-select-trigger > svg {
  color: var(--modern-muted);
  transition: transform var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-select-trigger[data-state='open'] > svg:last-child {
  transform: rotate(180deg);
}
</style>
