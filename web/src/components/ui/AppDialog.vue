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
    preventCloseAutoFocus?: boolean
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
    <DialogTrigger v-if="$slots.trigger" as-child><slot name="trigger" /></DialogTrigger>
    <DialogPortal>
      <DialogOverlay class="app-dialog__overlay" />
      <DialogContent
        class="app-dialog__content"
        @close-auto-focus="preventCloseAutoFocus && $event.preventDefault()"
        @escape-key-down="guardDismiss"
        @interact-outside="guardDismiss"
      >
        <header class="app-dialog__header">
          <DialogTitle class="app-dialog__title">{{ title }}</DialogTitle>
          <DialogClose class="app-dialog__close" :aria-label="closeLabel" :disabled="!dismissible">
            <X :size="20" aria-hidden="true" />
          </DialogClose>
        </header>
        <DialogDescription class="app-dialog__description">{{ description }}</DialogDescription>
        <div v-if="$slots.body" class="app-dialog__body"><slot name="body" /></div>
        <footer v-if="$slots.footer" class="app-dialog__footer"><slot name="footer" /></footer>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style>
.app-dialog__overlay {
  position: fixed;
  z-index: var(--z-dialog-overlay);
  inset: 0;
  background: var(--color-overlay);
}
.app-dialog__content {
  position: fixed;
  z-index: var(--z-dialog);
  top: 50%;
  left: 50%;
  display: grid;
  width: min(calc(100vw - 32px), 420px);
  max-height: calc(100vh - 32px);
  max-height: calc(100dvh - 32px);
  transform: translate(-50%, -50%);
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  box-shadow: var(--shadow-overlay);
  color: var(--color-text);
  padding: 0;
}
.app-dialog__header {
  display: flex;
  min-height: 58px;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 10px 16px 10px 18px;
}
.app-dialog__title {
  font-size: var(--text-meta);
  font-weight: 700;
}
.app-dialog__close {
  display: inline-flex;
  width: var(--touch-target);
  height: var(--touch-target);
  flex: 0 0 var(--touch-target);
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}
.app-dialog__close:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}
.app-dialog__description {
  margin: 0;
  padding: 12px 18px 0;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  line-height: var(--line-normal);
}
.app-dialog__body {
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 14px 18px 16px;
}
.app-dialog__footer {
  display: flex;
  min-height: 58px;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
  border-top: 1px solid var(--color-border-subtle);
  padding: 10px 18px;
}
</style>
