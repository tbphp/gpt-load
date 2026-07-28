<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyDto } from '@/api/control/types'

defineProps<{
  name: string
  status: AccessKeyDto['status']
  rpmLimit: number
  editing: boolean
  disabled: boolean
}>()
const emit = defineEmits<{
  'update:name': [value: string]
  'update:status': [value: AccessKeyDto['status']]
  'update:rpmLimit': [value: number]
}>()
const { t } = useI18n()
const nameInput = ref<HTMLInputElement>()

function focusName(): void {
  nameInput.value?.focus()
}

defineExpose({ focusName })
</script>

<template>
  <label class="access-key-drawer__field" for="access-key-name">
    <span>{{ t('accessKeys.drawer.name') }}</span>
    <input
      id="access-key-name"
      ref="nameInput"
      :value="name"
      data-test="access-key-name"
      type="text"
      autocomplete="off"
      :disabled="disabled"
      @input="emit('update:name', ($event.target as HTMLInputElement).value)"
    />
  </label>

  <label v-if="editing" class="access-key-drawer__field" for="access-key-status">
    <span>{{ t('accessKeys.drawer.status') }}</span>
    <select
      id="access-key-status"
      :value="status"
      data-test="access-key-status"
      :disabled="disabled"
      @change="
        emit('update:status', ($event.target as HTMLSelectElement).value as AccessKeyDto['status'])
      "
    >
      <option value="active">{{ t('accessKeys.status.active') }}</option>
      <option value="disabled">{{ t('accessKeys.status.disabled') }}</option>
    </select>
  </label>

  <label class="access-key-drawer__field" for="access-key-rpm">
    <span>{{ t('accessKeys.drawer.rpm') }}</span>
    <input
      id="access-key-rpm"
      data-test="access-key-rpm"
      type="number"
      min="0"
      step="1"
      :value="rpmLimit"
      :disabled="disabled"
      @input="emit('update:rpmLimit', Number(($event.target as HTMLInputElement).value))"
    />
    <small>{{ t('accessKeys.drawer.rpmDescription') }}</small>
  </label>
</template>

<style scoped>
.access-key-drawer__field {
  display: grid;
  gap: var(--space-2);
}
.access-key-drawer__field > span {
  font-weight: 700;
}
input,
select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}
small {
  margin: 0;
  color: var(--color-text-muted);
}
</style>
