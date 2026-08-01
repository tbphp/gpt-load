<script setup lang="ts">
import { X } from '@lucide/vue'
import { computed, nextTick, ref, useAttrs, useId } from 'vue'

import IconButton from './IconButton.vue'

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    modelValue: string
    label: string
    id?: string
    type?: 'text' | 'search'
    placeholder?: string
    disabled?: boolean
    invalid?: boolean
    describedBy?: string
    clearLabel?: string
    appearance?: 'surface' | 'sunken'
    size?: 'compact' | 'sm' | 'md' | 'lg' | 'touch'
    autocomplete?: string
    spellcheck?: boolean
    monospace?: boolean
  }>(),
  {
    id: undefined,
    type: 'text',
    placeholder: undefined,
    disabled: false,
    invalid: false,
    describedBy: undefined,
    clearLabel: undefined,
    appearance: 'surface',
    size: 'md',
    autocomplete: 'off',
    spellcheck: true,
    monospace: false,
  },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const attrs = useAttrs()
const generatedId = useId()
const inputId = computed(() => props.id ?? `${generatedId}-input`)
const input = ref<HTMLInputElement>()

function focus(): void {
  input.value?.focus()
}

async function clear(): Promise<void> {
  emit('update:modelValue', '')
  await nextTick()
  focus()
}

defineExpose({ focus })
</script>

<template>
  <div
    class="app-text-input"
    :class="[
      `app-text-input--${appearance}`,
      `app-text-input--${size}`,
      { 'app-text-input--disabled': disabled, 'app-text-input--invalid': invalid },
    ]"
  >
    <label class="sr-only" :for="inputId">{{ label }}</label>
    <span v-if="$slots.leading" class="app-text-input__leading" aria-hidden="true">
      <slot name="leading" />
    </span>
    <input
      :id="inputId"
      ref="input"
      v-bind="attrs"
      :value="modelValue"
      :type="type"
      :placeholder="placeholder"
      :disabled="disabled"
      :aria-invalid="invalid || undefined"
      :aria-describedby="describedBy"
      :autocomplete="autocomplete"
      :spellcheck="spellcheck"
      :class="{ 'app-text-input__input--mono': monospace }"
      @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    />
    <IconButton
      v-if="clearLabel && modelValue"
      variant="ghost"
      size="compact"
      :disabled="disabled"
      :label="clearLabel"
      @click="clear"
    >
      <X :size="15" aria-hidden="true" />
    </IconButton>
  </div>
</template>

<style scoped>
.app-text-input {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  color: var(--color-text-muted);
  padding-left: var(--space-3);
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    box-shadow var(--duration-fast) var(--easing-standard),
    opacity var(--duration-fast) var(--easing-standard);
}

.app-text-input--surface {
  background: var(--color-surface);
}

.app-text-input--sunken {
  background: var(--color-surface-sunken);
}

.app-text-input--md {
  min-height: var(--control-md);
}

.app-text-input--compact {
  min-height: var(--control-xs);
  font-size: var(--text-meta);
}

.app-text-input--sm {
  min-height: var(--control-sm);
  font-size: var(--text-meta);
}

.app-text-input--lg {
  min-height: var(--control-lg);
}

.app-text-input--touch {
  min-height: var(--touch-target);
}

.app-text-input:focus-within:not(.app-text-input--disabled) {
  border-color: var(--color-action);
  box-shadow: var(--focus-ring);
}

.app-text-input--invalid {
  border-color: var(--color-danger);
}

.app-text-input--disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.app-text-input input {
  width: 100%;
  min-width: 0;
  min-height: inherit;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--color-text);
  padding: 0;
  font: inherit;
}

.app-text-input input:disabled {
  cursor: not-allowed;
}

.app-text-input input.app-text-input__input--mono {
  font-family: var(--font-mono);
}

.app-text-input input::placeholder {
  color: var(--color-text-faint);
  opacity: 1;
}

.app-text-input__leading {
  display: inline-flex;
  flex: none;
}

.app-text-input :deep(.icon-button) {
  width: var(--touch-target);
  height: var(--touch-target);
  flex: none;
}

.app-text-input--compact :deep(.icon-button) {
  width: var(--control-compact);
  height: var(--control-compact);
}

@media (max-width: 860px) {
  .app-text-input--compact {
    min-height: var(--touch-target);
  }

  .app-text-input--sm {
    min-height: var(--touch-target);
  }

  .app-text-input--compact :deep(.icon-button) {
    width: var(--touch-target);
    height: var(--touch-target);
  }
}
</style>
