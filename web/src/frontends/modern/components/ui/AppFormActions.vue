<script setup lang="ts">
import { Plus } from '@lucide/vue'
import AppIconButton from './AppIconButton.vue'
import AppButton from './AppButton.vue'

defineProps<{ addLabel?: string; addDisabled?: boolean }>()
defineEmits<{ add: [] }>()
</script>

<template>
  <div class="modern-form-actions">
    <AppIconButton
      v-if="addLabel"
      class="modern-form-actions-add-icon"
      :icon="Plus"
      :label="addLabel"
      :disabled="addDisabled"
      size="xs"
      @click="$emit('add')"
    />
    <AppButton
      v-if="addLabel"
      class="modern-form-actions-add-text"
      :icon="Plus"
      size="xs"
      variant="ghost"
      :disabled="addDisabled"
      @click="$emit('add')"
      >{{ addLabel }}</AppButton
    >
    <slot />
  </div>
</template>

<style scoped>
.modern-form-actions {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  max-width: 100%;
  margin-inline-start: auto;
  gap: var(--modern-space-2);
}
.modern-form-actions-add-text {
  display: none;
}
@media (max-width: 760px) {
  .modern-form-actions-add-icon {
    display: none;
  }
  .modern-form-actions-add-text {
    display: inline-flex;
    white-space: normal;
    max-width: 100%;
  }
}
</style>
