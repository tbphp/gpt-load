<script setup lang="ts">
import { Search, X } from '@lucide/vue'
import { nextTick, ref, useAttrs } from 'vue'

import IconButton from './IconButton.vue'

defineOptions({ inheritAttrs: false })

withDefaults(
  defineProps<{
    modelValue: string
    label: string
    placeholder: string
    clearLabel: string
    id?: string
    list?: string
    disabled?: boolean
    describedBy?: string
  }>(),
  {
    id: undefined,
    list: undefined,
    disabled: false,
    describedBy: undefined,
  },
)
const emit = defineEmits<{
  'update:modelValue': [value: string]
  clear: []
}>()
const attrs = useAttrs()
const input = ref<HTMLInputElement>()

function focus(): void {
  input.value?.focus()
}

async function clear(): Promise<void> {
  emit('update:modelValue', '')
  emit('clear')
  await nextTick()
  focus()
}

defineExpose({ focus })
</script>

<template>
  <span v-bind="attrs" class="app-search-input">
    <Search :size="15" aria-hidden="true" />
    <input
      :id="id"
      ref="input"
      class="app-search-input__control"
      type="search"
      :list="list"
      :value="modelValue"
      :aria-label="label"
      :aria-describedby="describedBy"
      :placeholder="placeholder"
      :disabled="disabled"
      autocomplete="off"
      :spellcheck="false"
      data-1p-ignore="true"
      data-lpignore="true"
      @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    />
    <IconButton
      v-if="modelValue"
      class="app-search-input__clear"
      size="xs"
      variant="ghost"
      :disabled="disabled"
      :label="clearLabel"
      @click="clear"
    >
      <X :size="14" aria-hidden="true" />
    </IconButton>
  </span>
</template>

<style scoped>
.app-search-input {
  position: relative;
  display: block;
  width: 100%;
  min-width: 0;
}

.app-search-input > svg {
  position: absolute;
  z-index: 1;
  top: 50%;
  left: 11px;
  transform: translateY(-50%);
  color: var(--color-text-faint);
  pointer-events: none;
}

.app-search-input__control {
  width: 100%;
  min-width: 0;
  height: 32px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  appearance: none;
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 38px 0 34px;
  font: inherit;
  font-size: var(--text-meta);
}

.app-search-input__control:hover:not(:disabled) {
  border-color: var(--color-text-faint);
}

.app-search-input__control::placeholder {
  color: var(--color-text-faint);
  opacity: 1;
}

.app-search-input__control::-webkit-search-cancel-button {
  display: none;
}

.app-search-input__control:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.app-search-input__clear {
  position: absolute;
  top: 2px;
  right: 3px;
}

@media (max-width: 860px) {
  .app-search-input__control {
    height: var(--touch-target);
  }

  .app-search-input__clear {
    top: 0;
    right: 0;
    width: var(--touch-target);
    height: var(--touch-target);
  }
}

@media (max-width: 560px) {
  .app-search-input__control {
    font-size: 16px;
  }
}
</style>
