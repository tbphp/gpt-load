<script setup lang="ts">
import { controlAttrs, layoutAttrs } from './field-attrs'
import { matchesSearchOption } from './search-options'
import { useLoadingActivity } from './loading'
import AppTooltip from './AppTooltip.vue'
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
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppField from './AppField.vue'
import AppFieldControl from './AppFieldControl.vue'
import AppIcon from './AppIcon.vue'
import AppMenuSurface from './AppMenuSurface.vue'
import { overlaySideOffset } from './overlay'
import type { ControlSize, FieldProps, SearchSelectOption } from './types'

defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<
    FieldProps & {
      options?: readonly SearchSelectOption[]
      selectedOption?: SearchSelectOption
      loadOptions?: (query: string, signal: AbortSignal) => Promise<readonly SearchSelectOption[]>
      allowCustom?: boolean
      placeholder?: string
      size?: ControlSize
      name?: string
      required?: boolean
      loading?: boolean
    }
  >(),
  {
    options: () => [],
    selectedOption: undefined,
    loadOptions: undefined,
    placeholder: undefined,
    name: undefined,
    size: 'md',
  },
)
const model = defineModel<string>({ required: true })
const { t } = useI18n()
const open = ref(false)
// 仅点击、输入或方向键打开；弹窗恢复焦点时保留已选标签，不自动进入空搜索态。
const search = ref('')
const remoteLoading = ref(false)
const loading = computed(() => props.loading || remoteLoading.value)
useLoadingActivity(loading)
const failed = ref(false)
const remoteOptions = ref<readonly SearchSelectOption[]>([])
const retainedOption = ref<SearchSelectOption>()
const input = ref<{ $el: HTMLInputElement }>()
const combobox = ref<{ highlightedElement?: HTMLElement }>()
defineExpose({ focus: () => input.value?.$el.focus({ preventScroll: true }) })
let controller: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined
let inputChanged = false
let keyboardBrowsing = false
const source = computed(() => (props.loadOptions ? remoteOptions.value : props.options))
const selected = computed({
  get: () => (model.value === '' ? null : model.value),
  set: (value: string | null) => {
    model.value = value ?? ''
    if (props.allowCustom) search.value = value ?? ''
    inputChanged = false
    keyboardBrowsing = false
  },
})
const selectedLabel = computed(() => {
  const option = [
    ...props.options,
    ...source.value,
    ...(props.selectedOption ? [props.selectedOption] : []),
  ].find((item) => item.value === model.value)
  return (
    option?.label ??
    (retainedOption.value?.value === model.value ? retainedOption.value.label : model.value)
  )
})
watch(
  [model, source, () => props.selectedOption],
  () => {
    const option = [
      ...props.options,
      ...source.value,
      ...(props.selectedOption ? [props.selectedOption] : []),
    ].find((item) => item.value === model.value)
    if (option) retainedOption.value = option
    else if (retainedOption.value?.value !== model.value) retainedOption.value = undefined
  },
  { immediate: true },
)
const visible = computed(() =>
  props.loadOptions
    ? source.value
    : source.value.filter((option) => matchesSearchOption(option, search.value)),
)
function updateSearch(value: string): void {
  inputChanged = true
  keyboardBrowsing = false
  search.value = value
  // 自定义模式直接保存输入；候选项只辅助定位，不要求再选择一次。
  if (props.allowCustom) model.value = value
}
function handleCustomKeydown(event: KeyboardEvent): void {
  if (!props.allowCustom || event.isComposing || event.keyCode === 229) return
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') keyboardBrowsing = true
  if (!open.value) return
  if ((event.key === 'Home' || event.key === 'End') && !keyboardBrowsing) {
    event.stopImmediatePropagation()
    return
  }
  if (
    event.key === 'Enter' &&
    (!keyboardBrowsing || !combobox.value?.highlightedElement?.isConnected)
  ) {
    // Reka 会自动高亮首项；只有主动使用方向键时，回车才采用该候选项。
    event.preventDefault()
    event.stopImmediatePropagation()
    open.value = false
  }
}
function cancelRequest(): void {
  clearTimeout(timer)
  controller?.abort()
  remoteLoading.value = false
}
function preventImplicitSubmit(event: KeyboardEvent): void {
  // 选择交给 Reka；无结果或加载中按回车也不能误提交外层表单。
  if (!event.isComposing && event.keyCode !== 229) event.preventDefault()
}
async function load(): Promise<void> {
  cancelRequest()
  if (!open.value || !props.loadOptions) return
  const request = new AbortController()
  controller = request
  remoteLoading.value = true
  failed.value = false
  try {
    const options = await props.loadOptions(search.value.trim(), request.signal)
    if (!request.signal.aborted) remoteOptions.value = options
  } catch {
    if (!request.signal.aborted) failed.value = true
  } finally {
    if (!request.signal.aborted) remoteLoading.value = false
  }
}
watch(search, () => {
  if (!open.value || !props.loadOptions) return
  cancelRequest()
  remoteOptions.value = []
  failed.value = false
  remoteLoading.value = true
  timer = setTimeout(() => void load(), 200)
})
watch(open, async (value) => {
  cancelRequest()
  if (!value) {
    inputChanged = false
    keyboardBrowsing = false
    if (!props.allowCustom) search.value = ''
    return
  }
  if (!inputChanged) search.value = props.allowCustom ? model.value : ''
  await nextTick()
  if (!open.value) return
  input.value?.$el.focus({ preventScroll: true })
  if (props.loadOptions) void load()
})
watch(
  () => props.disabled,
  (disabled) => {
    if (disabled) open.value = false
  },
)
watch(
  () => props.loadOptions,
  () => {
    cancelRequest()
    remoteOptions.value = []
    if (open.value) void load()
  },
)
onScopeDispose(cancelRequest)
</script>

<template>
  <AppField v-slot="{ id, describedBy, invalid }" v-bind="{ ...props, ...layoutAttrs($attrs) }">
    <ComboboxRoot
      ref="combobox"
      v-model="selected"
      v-model:open="open"
      :disabled="disabled"
      :name="name"
      :required="required"
      ignore-filter
      open-on-click
      :reset-search-term-on-blur="false"
      :reset-search-term-on-select="false"
    >
      <AppFieldControl as-child :invalid="invalid" :disabled="disabled" :size="size">
        <ComboboxAnchor class="modern-search-select-control" :aria-busy="loading || undefined">
          <AppIcon :icon="Search" size="sm" class="modern-search-select-hint" />
          <ComboboxInput
            v-bind="controlAttrs($attrs)"
            :id="id"
            ref="input"
            class="modern-search-select-input"
            :model-value="open ? search : allowCustom && model ? model : selectedLabel"
            :placeholder="placeholder ?? (open ? t('ui.select.search') : label)"
            :aria-invalid="invalid || undefined"
            :aria-describedby="describedBy"
            :aria-labelledby="`${id}-label`"
            @update:model-value="updateSearch"
            @keydown.capture="handleCustomKeydown"
            @keydown.enter="preventImplicitSubmit"
          />
          <AppTooltip :label="label" :disabled="open">
            <ComboboxTrigger class="modern-search-select-trigger" :aria-label="label">
              <AppIcon :icon="ChevronDown" size="sm" />
            </ComboboxTrigger>
          </AppTooltip>
        </ComboboxAnchor>
      </AppFieldControl>
      <ComboboxPortal>
        <AppMenuSurface>
          <ComboboxContent
            position="popper"
            align="start"
            :side-offset="overlaySideOffset"
            :aria-busy="loading || undefined"
          >
            <div v-if="loading" class="modern-search-select-status" role="status">
              {{ t('ui.select.loading') }}
            </div>
            <div v-else-if="failed" class="modern-search-select-status" role="alert">
              {{ t('ui.select.failed')
              }}<AppButton size="xs" @click="load">{{ t('ui.retry') }}</AppButton>
            </div>
            <div v-else-if="!visible.length" class="modern-search-select-status" role="status">
              {{ t(allowCustom && search.trim() ? 'ui.select.customInput' : 'ui.select.empty') }}
            </div>
            <ComboboxViewport v-else>
              <ComboboxItem
                v-for="option in visible"
                :key="option.value"
                :value="option.value === '' ? null : option.value"
                :text-value="option.label"
                :disabled="option.disabled"
                class="modern-menu-option"
                @select="retainedOption = option"
              >
                <span class="modern-search-select-option">
                  <slot name="option" :option="option"
                    ><span>{{ option.label }}</span
                    ><small v-if="option.description">{{ option.description }}</small></slot
                  >
                </span>
                <ComboboxItemIndicator><AppIcon :icon="Check" size="sm" /></ComboboxItemIndicator>
              </ComboboxItem>
            </ComboboxViewport>
          </ComboboxContent>
        </AppMenuSurface>
      </ComboboxPortal>
    </ComboboxRoot>
  </AppField>
</template>

<style scoped>
.modern-search-select-control {
  position: relative;
}
.modern-search-select-hint {
  color: var(--modern-muted);
}
.modern-search-select-input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
  padding: var(--modern-space-1) 0;
  color: var(--modern-text);
  font: inherit;
  text-overflow: ellipsis;
}
.modern-search-select-input::placeholder {
  color: var(--modern-control-placeholder);
  opacity: 1;
}
.modern-search-select-trigger {
  display: grid;
  min-width: var(--modern-inline-action-target);
  min-height: var(--modern-inline-action-target);
  place-items: center;
  border: 0;
  border-radius: var(--modern-radius-small);
  background: transparent;
  padding: 0;
  color: var(--modern-muted);
}
.modern-search-select-option {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-2);
  overflow-wrap: anywhere;
}
.modern-search-select-option small {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-search-select-status {
  display: flex;
  min-height: var(--modern-control-nav);
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  padding: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
@media (max-width: 760px) {
  .modern-search-select-input {
    font-size: var(--modern-font-size-input-mobile);
  }
}
</style>
