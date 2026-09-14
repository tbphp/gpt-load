<script setup lang="ts">
import { useForwardExpose } from 'reka-ui'
import type { Component } from 'vue'
import AppButton from './AppButton.vue'
import AppTooltip from './AppTooltip.vue'
import type { ButtonSize, ButtonVariant } from './types'

defineOptions({ inheritAttrs: false })
defineProps<{
  icon: Component
  label: string
  size?: ButtonSize
  variant?: ButtonVariant
  loading?: boolean
  disabled?: boolean
  tooltip?: boolean
}>()
const { forwardRef } = useForwardExpose()
</script>

<template>
  <AppTooltip
    :label="label"
    :disabled="
      tooltip === false || $attrs['aria-expanded'] === true || $attrs['aria-expanded'] === 'true'
    "
  >
    <AppButton
      :ref="forwardRef"
      v-bind="$attrs"
      :variant="variant ?? 'ghost'"
      icon-only
      :icon="icon"
      :size="size"
      :loading="loading"
      :disabled="disabled"
      :aria-label="label"
    />
  </AppTooltip>
</template>
