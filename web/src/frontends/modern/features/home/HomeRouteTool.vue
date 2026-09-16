<script setup lang="ts">
import { ChevronDown, Route } from '@lucide/vue'
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { AppButton, AppIcon } from '@modern/components/ui'

defineProps<{ open: boolean }>()
defineEmits<{ 'update:open': [boolean] }>()
const { t } = useI18n()
const panelId = useId()
</script>

<template>
  <section class="modern-home-tool" :class="{ 'is-open': open }">
    <div class="modern-home-tool-bar">
      <AppIcon :icon="Route" size="sm" />
      <div>
        <h2>{{ t('pages.inspector.title') }}</h2>
        <p>{{ t('home.inspectorHelp') }}</p>
      </div>
      <AppButton
        variant="ghost"
        size="sm"
        :icon="ChevronDown"
        :aria-expanded="open"
        :aria-controls="panelId"
        @click="$emit('update:open', !open)"
        >{{ t(open ? 'home.collapse' : 'home.expand') }}</AppButton
      >
    </div>
    <div v-if="open" :id="panelId" class="modern-home-tool-body"><slot /></div>
  </section>
</template>

<style scoped>
.modern-home-tool {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
}
.modern-home-tool-bar {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  padding: var(--modern-space-3) var(--modern-space-4);
  color: var(--modern-muted);
}
.modern-home-tool-bar > div {
  min-width: 0;
  flex: 1;
}
.modern-home-tool-bar h2 {
  color: var(--modern-text);
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-semibold);
}
.modern-home-tool-bar p {
  margin-top: var(--modern-space-0-5);
  font-size: var(--modern-font-size-small);
}
/* 收起时箭头朝右，展开时朝下；旋转的是图标本身，按钮文字位置不动。 */
.modern-home-tool-bar :deep(.modern-button svg) {
  transform: rotate(-90deg);
  transition: transform var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-home-tool.is-open .modern-home-tool-bar :deep(.modern-button svg) {
  transform: none;
}
.modern-home-tool-body {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding: 0 var(--modern-space-4) var(--modern-space-4);
}
@media (prefers-reduced-motion: reduce) {
  .modern-home-tool-bar :deep(.modern-button svg) {
    transition: none;
  }
}
</style>
