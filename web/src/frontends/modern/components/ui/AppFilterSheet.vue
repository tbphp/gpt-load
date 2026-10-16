<script setup lang="ts">
import { DialogRoot } from 'reka-ui'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppDialogContent from './AppDialogContent.vue'
import AppDialogHeader from './AppDialogHeader.vue'

defineProps<{ mobile: boolean; title: string; actionLabel?: string; activeCount?: number }>()
defineEmits<{ reset: [] }>()
const open = defineModel<boolean>('open', { default: false })
const { t } = useI18n()
</script>

<template>
  <DialogRoot v-if="mobile" v-model:open="open">
    <AppDialogContent placement="bottom" :title="title" :description="title">
      <AppDialogHeader :title="title" :close-label="t('ui.close')" @close="open = false" />
      <div class="modern-filter-sheet-body"><slot /></div>
      <footer class="modern-filter-sheet-footer">
        <slot name="footer" />
        <AppButton
          v-if="activeCount !== undefined"
          :disabled="!activeCount"
          @click="$emit('reset')"
          >{{ t('ui.filters.reset') }}</AppButton
        >
        <AppButton variant="primary" @click="open = false">{{
          actionLabel ?? t('ui.mobile.showResults')
        }}</AppButton>
      </footer>
    </AppDialogContent>
  </DialogRoot>
  <slot v-else />
</template>

<style scoped>
.modern-filter-sheet-body {
  display: grid;
  gap: var(--modern-space-4);
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: var(--modern-space-5);
}
.modern-filter-sheet-footer {
  display: flex;
  flex: none;
  justify-content: flex-end;
  gap: var(--modern-space-3);
  padding: var(--modern-space-4) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-filter-sheet-footer > * {
  flex: 1;
}
</style>
