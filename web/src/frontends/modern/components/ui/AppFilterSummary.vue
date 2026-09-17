<script setup lang="ts">
import AppTooltip from './AppTooltip.vue'
import { X } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppIcon from './AppIcon.vue'
import AppOverflowText from './AppOverflowText.vue'

defineProps<{
  items: readonly { key: string; label: string; value: string }[]
  disabled?: boolean
}>()
defineEmits<{ remove: [key: string]; reset: [] }>()
const { t } = useI18n()
</script>

<template>
  <div
    v-if="items.length"
    class="modern-filter-summary"
    role="group"
    :aria-label="t('ui.filters.active')"
  >
    <div v-for="item in items" :key="item.key" class="modern-filter-chip">
      <AppOverflowText :text="t('ui.filters.item', { label: item.label, value: item.value })" />
      <AppTooltip :label="t('ui.filters.remove', { label: item.label })">
        <button
          type="button"
          :disabled="disabled"
          :aria-label="t('ui.filters.remove', { label: item.label })"
          @click="$emit('remove', item.key)"
        >
          <AppIcon :icon="X" size="xs" />
        </button>
      </AppTooltip>
    </div>
    <AppButton variant="text" size="sm" :disabled="disabled" @click="$emit('reset')">{{
      t('ui.filters.reset')
    }}</AppButton>
  </div>
</template>

<style scoped>
.modern-filter-summary {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  text-align: left;
}
.modern-filter-chip {
  display: inline-flex;
  max-width: 260px;
  min-height: var(--modern-control-xs);
  align-items: center;
  gap: var(--modern-space-1);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  padding: 0 var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-filter-chip button {
  display: grid;
  min-width: var(--modern-inline-action-target);
  min-height: var(--modern-inline-action-target);
  flex: none;
  place-items: center;
  border: 0;
  border-radius: var(--modern-radius-small);
  background: transparent;
  padding: 0;
  color: var(--modern-muted);
}
.modern-filter-chip button:hover {
  background: var(--modern-control-pressed);
  color: var(--modern-text);
}
@media (max-width: 760px) {
  .modern-filter-chip button {
    min-width: var(--modern-touch-target);
    min-height: var(--modern-touch-target);
  }
}
</style>
