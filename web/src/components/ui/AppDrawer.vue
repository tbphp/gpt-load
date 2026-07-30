<script setup lang="ts">
import { X } from '@lucide/vue'
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

const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    description: string
    closeLabel: string
    dismissible?: boolean
  }>(),
  { dismissible: true },
)
const emit = defineEmits<{ 'update:open': [open: boolean] }>()

function setOpen(open: boolean): void {
  if (!open && !props.dismissible) return
  emit('update:open', open)
}

function guardDismiss(event: Event): void {
  if (!props.dismissible) event.preventDefault()
}
</script>

<template>
  <DialogRoot :open="open" @update:open="setOpen">
    <DialogTrigger as-child><slot name="trigger" /></DialogTrigger>
    <DialogPortal>
      <DialogOverlay class="app-drawer__overlay" />
      <DialogContent
        class="app-drawer__content"
        @escape-key-down="guardDismiss"
        @interact-outside="guardDismiss"
      >
        <header class="app-drawer__header">
          <DialogTitle class="app-drawer__title">{{ title }}</DialogTitle>
          <DialogDescription class="sr-only">{{ description }}</DialogDescription>
          <DialogClose class="app-drawer__close" :aria-label="closeLabel" :disabled="!dismissible">
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
  width: min(92vw, 520px);
  height: 100dvh;
  border-left: 1px solid var(--color-border-subtle);
  background: var(--color-surface);
  box-shadow: var(--shadow-overlay);
  color: var(--color-text);
  display: flex;
  flex-direction: column;
}
.app-drawer__header {
  display: flex;
  min-height: 64px;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--color-border-subtle);
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
.app-drawer__close:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
.app-drawer__body {
  overflow-y: auto;
  padding: var(--space-4);
  flex: 1;
  min-height: 0;
}
@media (max-width: 480px) {
  .app-drawer__content {
    width: 100vw;
  }
}
</style>
