<script setup lang="ts">
import { Primitive } from 'reka-ui'
</script>

<template>
  <Primitive as-child class="modern-menu-surface"><slot /></Primitive>
</template>

<!-- 菜单与选择器的 Portal 共用面板及选项样式。 -->
<style>
.modern-menu-surface {
  z-index: var(--modern-layer-menu);
  min-width: max(var(--modern-menu-min-width), var(--reka-popper-anchor-width, 0px));
  max-width: calc(100vw - var(--modern-space-8));
  max-height: min(var(--modern-menu-max-height), var(--reka-popper-available-height, 80dvh));
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  overscroll-behavior: contain;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  padding: var(--modern-space-1);
  color: var(--modern-text);
  box-shadow: var(--modern-shadow-menu);
  animation: modern-appear var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-menu-surface .modern-menu-label {
  padding: var(--modern-space-1-5) var(--modern-space-2) var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-medium);
}
.modern-menu-surface .modern-menu-option {
  display: flex;
  min-height: var(--modern-control-sm);
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-5);
  border-radius: var(--modern-radius-small);
  padding: var(--modern-space-1-5) var(--modern-space-3);
  font-size: var(--modern-font-size-secondary);
  line-height: var(--modern-leading-compact);
  cursor: pointer;
  user-select: none;
}
.modern-menu-surface .modern-menu-option[data-highlighted] {
  outline: none;
  background: var(--modern-control-hover);
}
.modern-menu-surface .modern-menu-option[data-state='checked'] {
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
  font-weight: var(--modern-weight-medium);
}
.modern-menu-surface .modern-menu-option[data-disabled] {
  opacity: var(--modern-opacity-disabled);
  pointer-events: none;
}
@media (max-width: 760px) {
  .modern-menu-surface .modern-menu-option {
    min-height: var(--modern-touch-target);
    font-size: var(--modern-font-size-body);
  }
}
</style>
