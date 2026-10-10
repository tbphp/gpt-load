<script setup lang="ts">
import { X } from '@lucide/vue'
import type { Component } from 'vue'
import { useI18n } from 'vue-i18n'
import AppIconButton from './AppIconButton.vue'
import AppOverflowText from './AppOverflowText.vue'
withDefaults(
  defineProps<{
    text: string
    as?: string | Component
    removable?: boolean
    removeLabel?: string
    disabled?: boolean
    mono?: boolean
    size?: 'xs' | 'sm'
    tone?: 'brand' | 'neutral'
    variant?: 'soft' | 'outline'
  }>(),
  { as: 'span', size: 'sm', tone: 'brand', variant: 'soft', removeLabel: undefined },
)
defineEmits<{ remove: [] }>()
const { t } = useI18n()
</script>
<template>
  <component
    :is="as"
    class="modern-tag"
    :class="[
      `modern-tag--${size}`,
      `modern-tag--${tone}`,
      `modern-tag--${variant}`,
      { 'is-removable': removable, 'is-mono': mono },
    ]"
  >
    <AppOverflowText class="modern-tag-text" :text="text" />
    <AppIconButton
      v-if="removable"
      class="modern-tag-remove"
      :icon="X"
      :label="removeLabel ?? t('ui.select.remove', { label: text })"
      size="xxs"
      :disabled="disabled"
      @click.stop="$emit('remove')"
    />
  </component>
</template>
<style scoped>
.modern-tag {
  display: inline-flex;
  flex: none;
  max-width: 100%;
  min-width: 0;
  min-height: var(--modern-control-xs);
  align-items: center;
  gap: 0;
  border: var(--modern-line-width) solid var(--modern-tooltip-border);
  border-radius: var(--modern-radius-small);
  background: var(--modern-badge-brand-surface);
  padding: var(--modern-space-0-5) var(--modern-space-2);
  color: var(--modern-badge-brand-text);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-compact);
  vertical-align: middle;
}
.modern-tag--neutral {
  background: var(--modern-surface);
  color: var(--modern-muted);
}
.modern-tag--xs {
  min-height: var(--modern-control-xxs);
  padding-inline: var(--modern-space-1-5);
  font-size: var(--modern-font-size-caption);
}
.modern-tag--outline {
  border-color: currentColor;
  background: transparent;
}
.modern-tag--brand.modern-tag--outline {
  color: var(--modern-accent);
}
.modern-tag .modern-tag-remove {
  color: inherit;
}
.modern-tag--xs .modern-tag-remove {
  --modern-button-size: calc(var(--modern-control-xxs) - 2 * var(--modern-line-width));
}
.modern-tag.is-removable {
  padding-block: 0;
  padding-right: var(--modern-space-0-5);
}
.modern-tag.is-mono {
  font-family: var(--modern-font-mono);
}
.modern-tag-text {
  flex: 0 1 auto;
  min-width: 0;
}
a.modern-tag {
  text-decoration: none;
}
a.modern-tag:hover {
  border-color: var(--modern-accent);
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
}
a.modern-tag:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
  box-shadow: var(--modern-shadow-focus);
}
@media (max-width: 760px) {
  a.modern-tag {
    min-height: var(--modern-touch-target);
  }
  .modern-tag--xs .modern-tag-remove {
    --modern-button-size: var(--modern-touch-target);
  }
}
</style>
