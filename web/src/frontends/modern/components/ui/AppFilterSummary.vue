<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppTag from './AppTag.vue'

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
    <AppTag
      v-for="item in items"
      :key="item.key"
      class="modern-filter-chip"
      :text="t('ui.filters.item', { label: item.label, value: item.value })"
      :remove-label="t('ui.filters.remove', { label: item.label })"
      size="xs"
      variant="outline"
      removable
      :disabled="disabled"
      @remove="$emit('remove', item.key)"
    />
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
  max-width: min(100%, 260px);
}
</style>
