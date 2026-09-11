<script setup lang="ts">
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogTitle } from 'reka-ui'

defineOptions({ inheritAttrs: false })
withDefaults(
  defineProps<{ title: string; description: string; placement?: 'dialog' | 'sidebar' }>(),
  { placement: 'dialog' },
)
defineEmits<{ openAutoFocus: [event: Event] }>()
</script>

<template>
  <DialogPortal>
    <DialogOverlay class="modern-overlay" />
    <DialogContent
      v-bind="$attrs"
      class="modern-dialog"
      :class="`modern-dialog--${placement}`"
      @open-auto-focus="$emit('openAutoFocus', $event)"
    >
      <DialogTitle class="modern-sr-only">{{ title }}</DialogTitle>
      <DialogDescription class="modern-sr-only">{{ description }}</DialogDescription>
      <slot />
    </DialogContent>
  </DialogPortal>
</template>

<style scoped>
.modern-overlay {
  position: fixed;
  z-index: var(--modern-layer-overlay);
  inset: 0;
  background: var(--modern-overlay);
}
.modern-dialog {
  position: fixed;
  z-index: var(--modern-layer-dialog);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.modern-dialog--dialog {
  top: min(16vh, 130px);
  left: 50%;
  width: min(var(--modern-dialog-width), calc(100vw - var(--modern-space-8)));
  max-height: calc(100dvh - min(16vh, 130px) - var(--modern-space-4));
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-dialog);
  background: var(--modern-surface);
  transform: translateX(-50%);
  box-shadow: var(--modern-shadow-dialog);
}
.modern-dialog--sidebar {
  inset: 0 auto 0 0;
  width: min(var(--modern-drawer-width), calc(100vw - var(--modern-space-10)));
  overflow-y: auto;
  background: var(--modern-sidebar);
}
@media (max-width: 760px) {
  .modern-dialog--dialog {
    top: 12vh;
    max-height: calc(88dvh - var(--modern-space-4));
  }
}
</style>
