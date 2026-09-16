<script setup lang="ts">
import { Check, ChevronDown, Search } from '@lucide/vue'
import {
  ComboboxAnchor,
  ComboboxContent,
  ComboboxInput,
  ComboboxItem,
  ComboboxItemIndicator,
  ComboboxPortal,
  ComboboxRoot,
  ComboboxTrigger,
  ComboboxViewport,
} from 'reka-ui'
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppTag from './AppTag.vue'
import AppField from './AppField.vue'
import AppFieldControl from './AppFieldControl.vue'
import AppIcon from './AppIcon.vue'
import AppMenuSurface from './AppMenuSurface.vue'
import AppOverflowText from './AppOverflowText.vue'
import AppTooltip from './AppTooltip.vue'
import { useLoadingActivity } from './loading'
import { overlaySideOffset } from './overlay'
import { matchesSearchOption } from './search-options'
import type { ControlSize, FieldProps, SearchSelectOption } from './types'

const props = withDefaults(
  defineProps<
    FieldProps & {
      options: readonly SearchSelectOption[]
      allowCustom?: boolean
      placeholder?: string
      loading?: boolean
      size?: ControlSize
    }
  >(),
  { size: 'sm', placeholder: undefined },
)
const model = defineModel<string[]>({ required: true })
const { t, n } = useI18n()
const search = ref('')
const open = ref(false)
const input = ref<{ $el: HTMLInputElement }>()
const browsing = ref(false)
const labels = computed(() => new Map(props.options.map((option) => [option.value, option.label])))
const candidates = computed(() => {
  const options = props.options.filter((option) => matchesSearchOption(option, search.value))
  const custom = search.value.trim()
  return props.allowCustom &&
    custom &&
    !props.options.some((option) => option.value === custom) &&
    !model.value.includes(custom)
    ? [{ value: custom, label: t('ui.select.add', { value: custom }) }, ...options]
    : options
})
async function removeValue(value: string): Promise<void> {
  if (props.disabled) return
  model.value = model.value.filter((item) => item !== value)
  await nextTick()
  input.value?.$el.focus({ preventScroll: true })
}
function change(value: string): void {
  search.value = value
  browsing.value = false
}
function keydown(event: KeyboardEvent): void {
  if (event.isComposing || event.keyCode === 229) return
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') browsing.value = true
  if (event.key === 'Enter' && props.allowCustom && !browsing.value && search.value.trim()) {
    event.preventDefault()
    event.stopImmediatePropagation()
    const value = search.value.trim()
    if (!props.disabled && !model.value.includes(value)) model.value = [...model.value, value]
    search.value = ''
    open.value = true
  }
}
function preventSubmit(event: KeyboardEvent): void {
  if (!event.isComposing && event.keyCode !== 229) event.preventDefault()
}
watch(open, (value) => {
  if (!value) {
    search.value = ''
    browsing.value = false
  }
})
watch(
  () => props.disabled,
  (value) => {
    if (value) open.value = false
  },
)
useLoadingActivity(() => Boolean(props.loading))
defineExpose({ focus: () => input.value?.$el.focus({ preventScroll: true }) })
</script>
<template>
  <AppField v-slot="{ id, describedBy, invalid }" v-bind="props">
    <ComboboxRoot
      v-model="model"
      v-model:open="open"
      multiple
      ignore-filter
      open-on-click
      :disabled="disabled"
      :reset-search-term-on-blur="false"
      :reset-search-term-on-select="false"
    >
      <AppFieldControl as-child :size="size" :invalid="invalid" :disabled="disabled">
        <ComboboxAnchor :aria-busy="loading || undefined">
          <AppIcon :icon="Search" size="sm" class="modern-multi-hint" />
          <ComboboxInput
            :id="id"
            ref="input"
            :model-value="search"
            :placeholder="placeholder ?? t('ui.select.search')"
            :aria-describedby="describedBy"
            :aria-invalid="invalid || undefined"
            :aria-labelledby="`${id}-label`"
            class="modern-multi-input"
            @update:model-value="change"
            @keydown.capture="keydown"
            @keydown.enter="preventSubmit"
          />
          <AppTooltip :label="label" :disabled="open"
            ><ComboboxTrigger class="modern-multi-trigger" :aria-label="label"
              ><AppIcon :icon="ChevronDown" size="sm" /></ComboboxTrigger
          ></AppTooltip>
        </ComboboxAnchor>
      </AppFieldControl>
      <ComboboxPortal
        ><AppMenuSurface
          ><ComboboxContent position="popper" align="start" :side-offset="overlaySideOffset">
            <p v-if="loading" class="modern-multi-empty" role="status">
              {{ t('ui.select.loading') }}
            </p>
            <p v-else-if="!candidates.length" class="modern-multi-empty" role="status">
              {{ t('ui.select.empty') }}
            </p>
            <ComboboxViewport v-else>
              <ComboboxItem
                v-for="option in candidates"
                :key="option.value"
                :value="option.value"
                :text-value="option.label"
                :disabled="option.disabled"
                class="modern-menu-option"
              >
                <span class="modern-multi-option"
                  ><slot name="option" :option="option"
                    ><AppOverflowText :text="option.label" /></slot
                ></span>
                <ComboboxItemIndicator><AppIcon :icon="Check" size="sm" /></ComboboxItemIndicator>
              </ComboboxItem>
            </ComboboxViewport> </ComboboxContent></AppMenuSurface
      ></ComboboxPortal>
    </ComboboxRoot>
    <div
      v-if="model.length"
      class="modern-multi-selected"
      :aria-label="t('ui.select.selected', { count: n(model.length) })"
    >
      <slot
        v-for="value in model"
        :key="value"
        name="tag"
        :value="value"
        :label="labels.get(value) ?? value"
        :remove="() => removeValue(value)"
      >
        <AppTag
          :text="labels.get(value) ?? value"
          removable
          :disabled="disabled"
          @remove="removeValue(value)"
        />
      </slot>
    </div>
  </AppField>
</template>
<style scoped>
.modern-multi-input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
  padding: var(--modern-space-1) 0;
  font: inherit;
  text-overflow: ellipsis;
}
.modern-multi-input::placeholder {
  color: var(--modern-control-placeholder);
}
.modern-multi-hint,
.modern-multi-trigger {
  color: var(--modern-muted);
}
.modern-multi-trigger {
  display: grid;
  place-items: center;
  min-width: var(--modern-inline-action-target);
  min-height: var(--modern-inline-action-target);
  padding: 0;
  border: 0;
  border-radius: var(--modern-radius-small);
  background: transparent;
}
.modern-multi-option {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  flex: 1;
  min-width: 0;
}
.modern-multi-selected {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: var(--modern-space-1-5);
  min-width: 0;
}
.modern-multi-empty {
  padding: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
@media (max-width: 760px) {
  .modern-multi-input {
    font-size: var(--modern-font-size-input-mobile);
  }
}
</style>
