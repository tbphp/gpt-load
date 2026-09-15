<script setup lang="ts">
import { useForwardExpose } from 'reka-ui'
import type { Component } from 'vue'
import AppButton from './AppButton.vue'
import AppTooltip from './AppTooltip.vue'
import type { ButtonSize, ButtonVariant } from './types'

defineOptions({ inheritAttrs: false })
withDefaults(
  defineProps<{
    icon: Component
    label: string
    size?: ButtonSize
    variant?: ButtonVariant
    loading?: boolean
    disabled?: boolean
    // 显式开启时保留展开状态的提示，默认在菜单打开时隐藏。
    tooltip?: boolean
  }>(),
  // 保留“未指定”状态，避免 Vue 将省略的 Boolean prop 转成 false 而关闭提示。
  { size: 'md', variant: 'ghost', tooltip: undefined },
)
const { forwardRef } = useForwardExpose()
</script>

<template>
  <AppTooltip
    :label="label"
    :disabled="
      tooltip === false ||
      (tooltip !== true && ($attrs['aria-expanded'] === true || $attrs['aria-expanded'] === 'true'))
    "
  >
    <AppButton
      :ref="forwardRef"
      v-bind="$attrs"
      :variant="variant"
      icon-only
      :icon="icon"
      :size="size"
      :loading="loading"
      :disabled="disabled"
      :aria-label="label"
    />
  </AppTooltip>
</template>
