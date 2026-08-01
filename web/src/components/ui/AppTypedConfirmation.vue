<script setup lang="ts">
import { ref } from 'vue'

defineProps<{
  id: string
  label: string
  modelValue: string
  disabled?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const input = ref<HTMLInputElement>()

function focus(): void {
  input.value?.focus()
}

defineExpose({ focus })
</script>

<template>
  <label class="app-typed-confirmation" :for="id">
    <span>{{ label }}</span>
    <input
      :id="id"
      ref="input"
      :value="modelValue"
      type="text"
      autocomplete="off"
      spellcheck="false"
      :disabled="disabled"
      @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    />
  </label>
</template>

<style scoped>
.app-typed-confirmation {
  display: grid;
  gap: 6px;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 600;
}
.app-typed-confirmation input {
  width: 100%;
  min-height: var(--control-md);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  outline: 0;
  background: var(--color-surface-sunken);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
  font-weight: 400;
}
.app-typed-confirmation input:focus {
  border-color: var(--color-action);
  box-shadow: var(--focus-ring);
}
.app-typed-confirmation input:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}
</style>
