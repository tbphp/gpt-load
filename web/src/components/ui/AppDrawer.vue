<script setup lang="ts">
import { X } from 'lucide-vue-next'
import {
  DialogClose,
  DialogContent,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
  DialogTrigger,
} from 'reka-ui'

defineProps<{
  open: boolean
  title: string
  closeLabel: string
}>()
defineEmits<{ 'update:open': [open: boolean] }>()
</script>

<template>
  <DialogRoot :open="open" @update:open="$emit('update:open', $event)">
    <DialogTrigger as-child><slot name="trigger" /></DialogTrigger>
    <DialogPortal>
      <DialogOverlay class="app-drawer__overlay" />
      <DialogContent class="app-drawer__content">
        <header class="app-drawer__header">
          <DialogTitle class="app-drawer__title">{{ title }}</DialogTitle>
          <DialogClose class="app-drawer__close" :aria-label="closeLabel">
            <X :size="20" aria-hidden="true" />
          </DialogClose>
        </header>
        <div class="app-drawer__body"><slot /></div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style>
.app-drawer__overlay {
  position: fixed;
  z-index: var(--z-overlay);
  inset: 0;
  background: var(--color-overlay);
}
.app-drawer__content {
  position: fixed;
  z-index: var(--z-drawer);
  top: 0;
  right: 0;
  width: min(90vw, 380px);
  height: 100dvh;
  border-left: 1px solid var(--color-border);
  background: var(--color-surface);
  box-shadow: var(--shadow-overlay);
  color: var(--color-text);
}
.app-drawer__header {
  display: flex;
  min-height: 64px;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--color-border);
  padding: var(--space-2) var(--space-4);
}
.app-drawer__title {
  font-size: 1rem;
  font-weight: 700;
}
.app-drawer__close {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}
.app-drawer__body {
  overflow-y: auto;
  padding: var(--space-4);
}
@media (max-width: 480px) {
  .app-drawer__content {
    width: 100vw;
  }
}
</style>
