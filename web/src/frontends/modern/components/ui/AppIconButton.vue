<script setup lang="ts">
import { useForwardExpose } from 'reka-ui'
import type { Component } from 'vue'

import AppButton from './AppButton.vue'

defineProps<{
  icon: Component
  label: string
  size?: 'xs' | 'sm' | 'md'
  loading?: boolean
  disabled?: boolean
}>()
const { forwardRef } = useForwardExpose()
</script>

<template>
  <AppButton
    :ref="forwardRef"
    variant="ghost"
    icon-only
    :icon="icon"
    :size="size"
    :loading="loading"
    :disabled="disabled"
    :aria-label="label"
    :title="label"
  />
</template>
