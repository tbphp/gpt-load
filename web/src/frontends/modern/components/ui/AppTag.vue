<script setup lang="ts">
import { X } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import AppIconButton from './AppIconButton.vue'
import AppOverflowText from './AppOverflowText.vue'
withDefaults(
  defineProps<{
    text: string
    removable?: boolean
    disabled?: boolean
    size?: 'xs' | 'sm'
    tone?: 'brand' | 'neutral'
  }>(),
  { size: 'sm', tone: 'brand' },
)
defineEmits<{ remove: [] }>()
const { t } = useI18n()
</script>
<template>
  <span
    class="modern-tag"
    :class="[`modern-tag--${size}`, `modern-tag--${tone}`, { 'is-removable': removable }]"
  >
    <AppOverflowText class="modern-tag-text" :text="text" />
    <AppIconButton
      v-if="removable"
      :icon="X"
      :label="t('ui.select.remove', { label: text })"
      size="xxs"
      :disabled="disabled"
      @click.stop="$emit('remove')"
    />
  </span>
</template>
<style scoped>
.modern-tag {
  display: inline-flex;
  flex: none;
  max-width: 100%;
  min-width: 0;
  min-height: var(--modern-control-xs);
  align-items: center;
  gap: var(--modern-space-1);
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
  min-height: var(--modern-badge-xs);
  padding-inline: var(--modern-space-1-5);
  font-size: var(--modern-font-size-caption);
}
.modern-tag.is-removable {
  padding-block: 0;
  padding-right: var(--modern-space-0-5);
}
.modern-tag-text {
  flex: 0 1 auto;
  min-width: 0;
}
</style>
