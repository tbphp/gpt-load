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
    showDescription?: boolean
  }>(),
  { dismissible: true, showDescription: false },
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
          <div class="app-drawer__heading">
            <DialogTitle class="app-drawer__title">{{ title }}</DialogTitle>
            <DialogDescription :class="showDescription ? 'app-drawer__description' : 'sr-only'">
              {{ description }}
            </DialogDescription>
          </div>
          <DialogClose class="app-drawer__close" :aria-label="closeLabel" :disabled="!dismissible">
            <X :size="20" aria-hidden="true" />
          </DialogClose>
        </header>
        <div class="app-drawer__body"><slot /></div>
        <footer v-if="$slots.footer" class="app-drawer__footer"><slot name="footer" /></footer>
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
  opacity: 0;
}
.app-drawer__overlay[data-state='open'] {
  opacity: 1;
  animation: app-drawer-overlay-in var(--duration-normal) var(--easing-standard);
}
.app-drawer__overlay[data-state='closed'] {
  opacity: 0;
  animation: app-drawer-overlay-out var(--duration-normal) var(--easing-standard);
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
  transform: translateX(100%);
}
.app-drawer__content[data-state='open'] {
  transform: translateX(0);
  animation: app-drawer-content-in var(--duration-normal) var(--easing-standard);
}
.app-drawer__content[data-state='closed'] {
  transform: translateX(100%);
  animation: app-drawer-content-out var(--duration-normal) var(--easing-standard);
}
.app-drawer__header {
  display: flex;
  min-height: 64px;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 12px 18px;
}
.app-drawer__heading {
  min-width: 0;
}
.app-drawer__title {
  font-size: 1rem;
  font-weight: 700;
}
.app-drawer__description {
  margin: 2px 0 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
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
  padding: 18px;
  flex: 1;
  min-height: 0;
}
.app-drawer__footer {
  display: flex;
  min-height: 62px;
  flex: none;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
  border-top: 1px solid var(--color-border-subtle);
  padding: 10px 18px;
}
@keyframes app-drawer-overlay-in {
  from { opacity: 0; }
  to { opacity: 1; }
}
@keyframes app-drawer-overlay-out {
  from { opacity: 1; }
  to { opacity: 0; }
}
@keyframes app-drawer-content-in {
  from { transform: translateX(100%); }
  to { transform: translateX(0); }
}
@keyframes app-drawer-content-out {
  from { transform: translateX(0); }
  to { transform: translateX(100%); }
}
@media (max-width: 480px) {
  .app-drawer__content {
    width: 100vw;
  }
}
@media (prefers-reduced-motion: reduce) {
  .app-drawer__overlay,
  .app-drawer__content {
    animation: none !important;
  }
}
</style>
