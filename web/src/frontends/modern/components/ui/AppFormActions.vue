<script setup lang="ts">
import { Plus } from '@lucide/vue'
import AppIconButton from './AppIconButton.vue'

defineProps<{ addLabel?: string; addDisabled?: boolean }>()
defineEmits<{ add: [] }>()
</script>

<template>
  <div class="modern-form-actions">
    <AppIconButton
      v-if="addLabel"
      :icon="Plus"
      :label="addLabel"
      :disabled="addDisabled"
      size="xs"
      @click="$emit('add')"
    />
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
</style>
