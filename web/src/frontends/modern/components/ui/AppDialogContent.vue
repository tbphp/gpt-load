<script setup lang="ts">
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogTitle } from 'reka-ui'

defineOptions({ inheritAttrs: false })
withDefaults(
  defineProps<{
    title: string
    description: string
    placement?: 'dialog' | 'sidebar' | 'editor'
    size?: 'default' | 'wide' | 'sheet' | 'confirm'
  }>(),
  { placement: 'dialog', size: 'default' },
)
defineEmits<{ openAutoFocus: [event: Event] }>()
</script>

<template>
  <DialogPortal>
    <DialogOverlay class="modern-overlay" />
    <DialogContent
      v-bind="$attrs"
      class="modern-dialog"
      :class="[`modern-dialog--${placement}`, `modern-dialog--${size}`]"
      @open-auto-focus="$emit('openAutoFocus', $event)"
    >
      <DialogTitle class="modern-sr-only">{{ title }}</DialogTitle>
      <DialogDescription class="modern-sr-only">{{ description }}</DialogDescription>
      <slot />
    </DialogContent>
  </DialogPortal>
</template>

<!-- Portal 内容不带当前组件的 scoped 属性，使用唯一的 modern 类名作用于浮层节点。 -->
<style>
.modern-overlay {
  position: fixed;
  z-index: var(--modern-layer-overlay);
  inset: 0;
  background: var(--modern-overlay);
  animation: modern-appear var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-dialog {
  position: fixed;
  z-index: var(--modern-layer-dialog);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  animation: modern-appear var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-dialog--dialog {
  --modern-dialog-top: min(16dvh, 130px);
  top: var(--modern-dialog-top);
  left: 50%;
  width: min(var(--modern-dialog-width), calc(100vw - var(--modern-space-8)));
  max-height: calc(100dvh - var(--modern-dialog-top) - var(--modern-space-4));
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-dialog);
  background: var(--modern-surface);
  transform: translateX(-50%);
  box-shadow: var(--modern-shadow-dialog);
}
.modern-dialog--wide {
  --modern-dialog-width: var(--modern-dialog-wide-width);
}
.modern-dialog--sheet {
  --modern-dialog-width: var(--modern-dialog-sheet-width);
}
.modern-dialog--confirm {
  --modern-dialog-width: var(--modern-dialog-confirm-width);
  top: 50%;
  max-height: calc(100dvh - var(--modern-space-8));
  transform: translate(-50%, -50%);
}
.modern-dialog--sidebar {
  inset: 0 auto 0 0;
  width: min(var(--modern-drawer-width), calc(100vw - var(--modern-space-10)));
  overflow-y: auto;
  overscroll-behavior: contain;
  background: var(--modern-sidebar);
}
.modern-dialog--editor {
  inset: 0 0 0 auto;
  width: min(var(--modern-dialog-width), 100vw);
  border-left: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-surface);
  box-shadow: var(--modern-shadow-dialog);
}
@media (max-width: 760px) {
  .modern-dialog--dialog {
    --modern-dialog-top: 12dvh;
  }
}
</style>
