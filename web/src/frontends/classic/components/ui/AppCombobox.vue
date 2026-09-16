<script setup lang="ts">
import { Check, ChevronDown } from '@lucide/vue'
import { computed, useAttrs, useId } from 'vue'
import {
  ComboboxAnchor,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxItemIndicator,
  ComboboxPortal,
  ComboboxRoot,
  ComboboxTrigger,
} from 'reka-ui'

interface ComboboxOption {
  value: string
  label: string
}

defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    modelValue: string
    label: string
    options: ComboboxOption[]
    emptyText: string
    id?: string
    placeholder?: string
    disabled?: boolean
    invalid?: boolean
    describedBy?: string
    size?: 'sm' | 'md' | 'touch'
    spellcheck?: boolean
    monospace?: boolean
  }>(),
  {
    id: undefined,
    placeholder: undefined,
    disabled: false,
    invalid: false,
    describedBy: undefined,
    size: 'md',
    spellcheck: true,
    monospace: false,
  },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const attrs = useAttrs()
const generatedId = useId()
const inputId = computed(() => props.id ?? `${generatedId}-input`)
const normalizedOptions = computed(() => props.options.filter((option) => option.value !== ''))

function selectOption(value: unknown): void {
  if (typeof value === 'string') emit('update:modelValue', value)
}
</script>

<template>
  <ComboboxRoot
    class="app-combobox"
    :model-value="modelValue"
    :disabled="disabled"
    :open-on-click="true"
    :reset-search-term-on-blur="true"
    @update:model-value="selectOption"
  >
    <ComboboxAnchor
      class="app-combobox__anchor"
      :class="[
        `app-combobox__anchor--${size}`,
        {
          'app-combobox__anchor--disabled': disabled,
          'app-combobox__anchor--invalid': invalid,
        },
      ]"
      data-input-shell
    >
      <label class="sr-only" :for="inputId">{{ label }}</label>
      <ComboboxInput
        :id="inputId"
        v-bind="attrs"
        class="app-combobox__input"
        :class="{ 'app-combobox__input--mono': monospace }"
        :model-value="modelValue"
        :placeholder="placeholder"
        :disabled="disabled"
        :aria-invalid="invalid || undefined"
        :aria-describedby="describedBy"
        :spellcheck="spellcheck"
        data-input-inner
        @update:model-value="emit('update:modelValue', $event)"
      />
      <ComboboxTrigger class="app-combobox__trigger" :disabled="disabled">
        <ChevronDown :size="16" aria-hidden="true" />
      </ComboboxTrigger>
    </ComboboxAnchor>

    <ComboboxPortal>
      <ComboboxContent
        class="app-combobox__content"
        position="popper"
        align="start"
        :side-offset="6"
        :collision-padding="8"
      >
        <ComboboxEmpty class="app-combobox__empty">{{ emptyText }}</ComboboxEmpty>
        <ComboboxItem
          v-for="option in normalizedOptions"
          :key="option.value"
          class="app-combobox__item"
          :value="option.value"
          :text-value="option.label"
        >
          <ComboboxItemIndicator class="app-combobox__indicator">
            <Check :size="15" aria-hidden="true" />
          </ComboboxItemIndicator>
          <span>{{ option.label }}</span>
        </ComboboxItem>
      </ComboboxContent>
    </ComboboxPortal>
  </ComboboxRoot>
</template>

<style>
.app-combobox {
  display: block;
  width: 100%;
  min-width: 0;
}

.app-combobox__anchor {
  display: flex;
  width: 100%;
  min-width: 0;
  min-height: var(--control-md);
  align-items: center;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    box-shadow var(--duration-fast) var(--easing-standard),
    opacity var(--duration-fast) var(--easing-standard);
}

.app-combobox__anchor:hover:not(.app-combobox__anchor--disabled) {
  border-color: var(--color-text-faint);
}

.app-combobox__anchor--sm {
  min-height: var(--control-sm);
  height: var(--control-sm);
  font-size: var(--text-meta);
}

.app-combobox__anchor--touch {
  min-height: var(--touch-target);
}

.app-combobox__anchor--disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.app-combobox__anchor--invalid {
  border-color: var(--color-danger);
}

.app-combobox__input {
  width: 100%;
  min-width: 0;
  min-height: inherit;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--color-text);
  padding: 0 0 0 var(--space-3);
  font: inherit;
}

.app-combobox__input::placeholder {
  color: var(--color-text-faint);
  opacity: 1;
}

.app-combobox__input:disabled {
  cursor: not-allowed;
}

.app-combobox__input:focus-visible {
  outline: 0;
}

.app-combobox__input--mono {
  font-family: var(--font-mono);
}

.app-combobox__trigger {
  display: inline-flex;
  width: var(--control-sm);
  height: 100%;
  min-height: inherit;
  flex: none;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: var(--color-text-faint);
  padding: 0;
  cursor: pointer;
}

.app-combobox__trigger:disabled {
  cursor: not-allowed;
}

.app-combobox__trigger[data-state='open'] svg {
  transform: rotate(180deg);
}

.app-combobox__trigger svg {
  transition: transform var(--duration-fast) var(--easing-standard);
}

.app-combobox__content {
  z-index: var(--z-popover);
  width: var(--reka-combobox-trigger-width);
  max-width: calc(100vw - 16px);
  max-height: min(320px, var(--reka-combobox-content-available-height));
  overflow-y: auto;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: var(--space-1);
  box-shadow: var(--shadow-overlay);
  transform-origin: var(--reka-combobox-content-transform-origin);
  scrollbar-width: none;
  -ms-overflow-style: none;
  -webkit-overflow-scrolling: touch;
}

.app-combobox__content::-webkit-scrollbar {
  display: none;
}

.app-combobox__content[data-state='open'] {
  animation: app-combobox-in var(--duration-fast) var(--easing-standard);
}

.app-combobox__item {
  position: relative;
  display: flex;
  min-width: 0;
  min-height: 44px;
  align-items: center;
  border-radius: var(--radius-tag);
  color: var(--color-text);
  padding: 7px 10px 7px 32px;
  cursor: pointer;
  outline: none;
  overflow-wrap: anywhere;
}

.app-combobox__item[data-highlighted] {
  background: var(--color-surface-sunken);
}

.app-combobox__indicator {
  position: absolute;
  left: 10px;
  display: inline-flex;
  color: var(--color-action);
}

.app-combobox__empty {
  color: var(--color-text-faint);
  padding: 12px 10px;
  font-size: var(--text-sm);
}

@keyframes app-combobox-in {
  from {
    opacity: 0;
    transform: translateY(-4px) scale(0.98);
  }
}

@media (max-width: 860px) {
  .app-combobox__anchor--sm {
    min-height: var(--touch-target);
    height: var(--touch-target);
  }
}

@media (prefers-reduced-motion: reduce) {
  .app-combobox__content[data-state='open'] {
    animation: none;
  }

  .app-combobox__trigger svg {
    transition: none;
  }
}
</style>
