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
    appearance?: 'default' | 'ledger'
  }>(),
  { dismissible: true, showDescription: false, appearance: 'default' },
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
      <DialogOverlay class="app-drawer__overlay" :class="`app-drawer__overlay--${appearance}`" />
      <DialogContent
        class="app-drawer__content"
        :class="`app-drawer__content--${appearance}`"
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
        <div v-if="$slots.filters" class="app-drawer__filters"><slot name="filters" /></div>
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
}
.app-drawer__header {
  display: flex;
  min-height: 64px;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 12px 18px;
}
.app-drawer__filters {
  flex: none;
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
.app-drawer__content--ledger {
  display: grid;
  width: min(92vw, 480px);
  grid-template-areas:
    'header'
    'filters'
    'body'
    'footer';
  grid-template-rows: auto auto minmax(0, 1fr) auto;
  border-left-color: var(--color-border-control);
  box-shadow: -16px 0 44px light-dark(rgba(0, 0, 0, 0.12), rgba(0, 0, 0, 0.42));
}
.app-drawer__content--ledger .app-drawer__header {
  grid-area: header;
  min-height: 0;
  align-items: flex-start;
  gap: 16px;
  padding: 20px 20px 16px;
}
.app-drawer__content--ledger .app-drawer__title {
  display: block;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: -0.01em;
  line-height: 1.35;
}
.app-drawer__content--ledger .app-drawer__description {
  margin-top: 4px;
  font-size: var(--text-sm);
}
.app-drawer__content--ledger .app-drawer__close {
  width: var(--control-compact);
  height: var(--control-compact);
  flex: 0 0 var(--control-compact);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
.app-drawer__content--ledger .app-drawer__close svg {
  width: 15px;
  height: 15px;
}
.app-drawer__content--ledger .app-drawer__filters {
  display: flex;
  grid-area: filters;
  align-items: center;
  gap: 8px;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 13px 20px;
}
.app-drawer__content--ledger .app-drawer__body {
  grid-area: body;
  overflow-y: auto;
  padding: 0 20px;
}
.app-drawer__content--ledger .app-drawer__footer {
  grid-area: footer;
  min-height: 0;
  justify-content: space-between;
  gap: 14px;
  border-top-color: var(--color-border-control);
  padding: 12px 20px;
}
@keyframes app-drawer-overlay-in {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}
@keyframes app-drawer-content-in {
  from {
    transform: translateX(100%);
  }
  to {
    transform: translateX(0);
  }
}
@media (max-width: 520px) {
  .app-drawer__content {
    width: 100vw;
  }
  .app-drawer__content--ledger .app-drawer__close {
    width: var(--touch-target);
    height: var(--touch-target);
    flex-basis: var(--touch-target);
  }
  .app-drawer__content--ledger .app-drawer__header,
  .app-drawer__content--ledger .app-drawer__filters,
  .app-drawer__content--ledger .app-drawer__body,
  .app-drawer__content--ledger .app-drawer__footer {
    padding-inline: 16px;
  }
  .app-drawer__content--ledger .app-drawer__footer {
    align-items: stretch;
    flex-direction: column;
  }
}
@media (prefers-reduced-motion: reduce) {
  .app-drawer__overlay,
  .app-drawer__content {
    animation: none !important;
  }
}
</style>
