<script setup lang="ts">
import { Ellipsis } from '@lucide/vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import type { Component } from 'vue'
import AppIcon from './AppIcon.vue'
import AppIconButton from './AppIconButton.vue'
import AppMenuSurface from './AppMenuSurface.vue'
import { overlaySideOffset } from './overlay'
import type { ButtonSize } from './types'

defineProps<{
  label: string
  disabled?: boolean
  size?: ButtonSize
  items: readonly {
    id: string
    label: string
    icon?: Component
    disabled?: boolean
    danger?: boolean
  }[]
}>()
defineEmits<{ select: [id: string] }>()
</script>

<template>
  <DropdownMenuRoot>
    <DropdownMenuTrigger as-child
      ><slot name="trigger"
        ><AppIconButton
          :icon="Ellipsis"
          :label="label"
          :size="size ?? 'sm'"
          :disabled="disabled" /></slot
    ></DropdownMenuTrigger>
    <DropdownMenuPortal
      ><AppMenuSurface
        ><DropdownMenuContent align="end" :side-offset="overlaySideOffset">
          <DropdownMenuItem
            v-for="item in items"
            :key="item.id"
            class="modern-menu-option modern-action-menu-item"
            :class="{ 'is-danger': item.danger }"
            :disabled="item.disabled || disabled"
            @select="$emit('select', item.id)"
            ><AppIcon v-if="item.icon" :icon="item.icon" size="sm" /><span>{{
              item.label
            }}</span></DropdownMenuItem
          >
        </DropdownMenuContent></AppMenuSurface
      ></DropdownMenuPortal
    >
  </DropdownMenuRoot>
</template>

<style>
.modern-menu-surface .modern-action-menu-item {
  justify-content: flex-start;
  gap: var(--modern-space-2);
}
.modern-menu-surface .modern-action-menu-item.is-danger {
  color: var(--modern-danger);
}
</style>
