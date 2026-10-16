<script setup lang="ts">
import { controlAttrs, layoutAttrs } from './field-attrs'
import { useLoadingActivity } from './loading'
import { ref, type Component } from 'vue'
import { X } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import AppIconButton from './AppIconButton.vue'
import AppField from './AppField.vue'
import AppFieldControl from './AppFieldControl.vue'
import AppIcon from './AppIcon.vue'
import type { ControlSize, FieldProps } from './types'

defineOptions({ inheritAttrs: false })
const props = defineProps<
  FieldProps & { icon?: Component; size?: ControlSize; loading?: boolean }
>()
const model = defineModel<string>({ required: true })
const input = ref<HTMLInputElement>()
const { t } = useI18n()
function clearSearch(): void {
  if (!input.value || props.disabled || input.value.readOnly) return
  input.value.value = ''
  model.value = ''
  input.value.dispatchEvent(new Event('input', { bubbles: true }))
  input.value.focus({ preventScroll: true })
}
useLoadingActivity(() => Boolean(props.loading))
defineExpose({
  focus: () => input.value?.focus({ preventScroll: true }),
  select: () => input.value?.select(),
})
</script>

<template>
  <AppField v-bind="{ ...props, ...layoutAttrs($attrs) }">
    <template v-if="$slots['label-extra']" #label-extra><slot name="label-extra" /></template>
    <template #default="{ id, describedBy, invalid }">
      <AppFieldControl
        class="modern-text-field-control"
        :invalid="invalid"
        :disabled="disabled"
        :size="size"
      >
        <AppIcon v-if="icon" :icon="icon" size="sm" :label="label" />
        <input
          v-bind="controlAttrs($attrs)"
          :id="id"
          ref="input"
          v-model="model"
          :type="($attrs.type as string) ?? 'text'"
          :autocapitalize="
            ($attrs.autocapitalize as string) ??
            ($attrs.type === 'search' || String($attrs.spellcheck) === 'false' ? 'none' : undefined)
          "
          :autocorrect="
            ($attrs.autocorrect as string) ??
            ($attrs.type === 'search' || String($attrs.spellcheck) === 'false' ? 'off' : undefined)
          "
          :disabled="disabled"
          :aria-invalid="invalid || undefined"
          :aria-describedby="describedBy"
          :aria-busy="loading || undefined"
        />
        <AppIconButton
          v-if="
            $attrs.type === 'search' &&
            model &&
            !disabled &&
            ($attrs.readonly === undefined || $attrs.readonly === false)
          "
          class="modern-search-clear"
          :icon="X"
          :label="t('ui.clearSearch')"
          size="xs"
          @pointerdown.prevent
          @click="clearSearch"
        />
        <slot name="suffix" />
      </AppFieldControl>
    </template>
  </AppField>
</template>

<style scoped>
.modern-text-field-control {
  position: relative;
}
.modern-search-clear {
  display: none;
}
.modern-text-field-control > svg {
  color: var(--modern-muted);
}
.modern-text-field-control input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
  padding: var(--modern-space-1) 0;
  color: var(--modern-text);
  font-size: inherit;
  line-height: var(--modern-leading-compact);
}
.modern-text-field-control input:disabled {
  color: var(--modern-muted);
  cursor: not-allowed;
}
.modern-text-field-control input::placeholder {
  color: var(--modern-control-placeholder);
  opacity: 1;
}
@media (max-width: 760px) {
  .modern-search-clear {
    display: inline-flex;
    margin-inline-end: calc(-1 * var(--modern-space-2));
  }
  .modern-text-field-control input[type='search']::-webkit-search-cancel-button {
    display: none;
  }
  .modern-text-field-control input {
    font-size: var(--modern-font-size-input-mobile);
  }
}
</style>
