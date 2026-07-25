<script setup lang="ts">
import { X } from 'lucide-vue-next'
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
  DialogTrigger,
} from 'reka-ui'

defineProps<{
  open: boolean
  title: string
  description: string
  closeLabel: string
}>()
defineEmits<{ 'update:open': [open: boolean] }>()
</script>

<template>
  <DialogRoot :open="open" @update:open="$emit('update:open', $event)">
    <DialogTrigger as-child><slot name="trigger" /></DialogTrigger>
    <DialogPortal>
      <DialogOverlay class="app-dialog__overlay" />
      <DialogContent class="app-dialog__content">
        <header class="app-dialog__header">
          <DialogTitle class="app-dialog__title">{{ title }}</DialogTitle>
          <DialogClose class="app-dialog__close" :aria-label="closeLabel">
            <X :size="20" aria-hidden="true" />
          </DialogClose>
        </header>
        <DialogDescription class="app-dialog__description">{{ description }}</DialogDescription>
        <div class="app-dialog__body"><slot /></div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style>
.app-dialog__overlay {
  position: fixed;
  z-index: var(--z-overlay);
  inset: 0;
  background: var(--color-overlay);
}
.app-dialog__content {
  position: fixed;
  z-index: var(--z-drawer);
  top: 50%;
  left: 50%;
  width: min(calc(100vw - 32px), 460px);
  transform: translate(-50%, -50%);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  box-shadow: var(--shadow-overlay);
  color: var(--color-text);
  padding: var(--space-5);
}
.app-dialog__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}
.app-dialog__title {
  font-size: 1.0625rem;
  font-weight: 700;
}
.app-dialog__close {
  display: inline-flex;
  width: 44px;
  height: 44px;
  flex: 0 0 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}
.app-dialog__description {
  margin: var(--space-2) 0 0;
  color: var(--color-text-muted);
}
.app-dialog__body {
  margin-top: var(--space-5);
}
</style>
