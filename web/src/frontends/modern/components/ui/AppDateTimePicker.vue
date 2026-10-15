<script setup lang="ts">
import { CalendarClock } from '@lucide/vue'
import {
  DialogRoot,
  DialogTrigger,
  PopoverContent,
  PopoverPortal,
  PopoverRoot,
  PopoverTrigger,
} from 'reka-ui'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppDialogContent from './AppDialogContent.vue'
import AppDialogHeader from './AppDialogHeader.vue'
import AppDateTimeEditor from './AppDateTimeEditor.vue'
import AppField from './AppField.vue'
import AppFieldControl from './AppFieldControl.vue'
import AppIcon from './AppIcon.vue'
import AppOverflowText from './AppOverflowText.vue'
import { layoutAttrs } from './field-attrs'
import {
  dateWithinBounds,
  formatLocalDateTime,
  localTimeZone,
  parseLocalDateTime,
  type DateTimeShortcut,
} from './date-time'
import { overlaySideOffset } from './overlay'
import type { ControlSize, FieldProps } from './types'
import './date-time.css'
defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<
    FieldProps & {
      mobile?: boolean
      min?: string
      max?: string
      placeholder?: string
      clearable?: boolean
      shortcuts?: readonly DateTimeShortcut[]
      size?: ControlSize
    }
  >(),
  {
    min: undefined,
    max: undefined,
    placeholder: undefined,
    clearable: true,
    size: 'sm',
    shortcuts: () => [],
  },
)
const model = defineModel<string>({ required: true })
const { t } = useI18n()
const open = ref(false)
const draft = ref('')
const activeShortcut = ref('')
const valid = computed(() => dateWithinBounds(draft.value, props.min, props.max))
const display = computed(() =>
  model.value ? model.value.replace('T', ' ') : (props.placeholder ?? t('ui.date.placeholder')),
)
watch(open, (value) => {
  if (value) {
    activeShortcut.value = ''
    draft.value = model.value || formatLocalDateTime(Math.floor(Date.now() / 1000) * 1000)
    if (!model.value && props.min && !dateWithinBounds(draft.value, props.min))
      draft.value = props.min
  }
})
watch(
  () => props.disabled,
  (disabled) => {
    if (disabled) open.value = false
  },
)
function shortcut(value: DateTimeShortcut): void {
  if (props.disabled) return
  draft.value = value.resolve()
  activeShortcut.value = value.label
  apply()
}
function confirmKey(event: KeyboardEvent): void {
  if (
    event.defaultPrevented ||
    event.isComposing ||
    !(event.target instanceof HTMLInputElement) ||
    event.target.getAttribute('role') === 'combobox'
  )
    return
  event.preventDefault()
  apply()
}
function apply(): void {
  if (!valid.value || props.disabled) return
  model.value = formatLocalDateTime(parseLocalDateTime(draft.value)!)
  open.value = false
}
function clear(): void {
  if (!props.disabled) {
    model.value = ''
    open.value = false
  }
}
</script>
<template>
  <AppField v-slot="{ id, describedBy, invalid }" v-bind="{ ...props, ...layoutAttrs($attrs) }">
    <component :is="mobile ? DialogRoot : PopoverRoot" v-model:open="open">
      <AppFieldControl as-child :size="size" :disabled="disabled" :invalid="invalid">
        <component :is="mobile ? DialogTrigger : PopoverTrigger" as-child
          ><AppButton
            :id="id"
            :size="size"
            class="modern-date-trigger"
            :disabled="disabled"
            :aria-labelledby="`${id}-label`"
            :aria-describedby="describedBy"
            :aria-invalid="invalid || undefined"
            ><AppIcon :icon="CalendarClock" size="sm" /><AppOverflowText
              :text="display"
              :class="{ 'modern-date-placeholder': !model }" /></AppButton
        ></component>
      </AppFieldControl>
      <component
        :is="mobile ? 'div' : PopoverPortal"
        :class="mobile ? 'modern-date-portal' : undefined"
      >
        <component
          :is="mobile ? AppDialogContent : PopoverContent"
          :class="mobile ? 'modern-date-sheet' : 'modern-date-popover'"
          v-bind="
            mobile
              ? { placement: 'bottom', title: label, description: label }
              : { align: 'start', sideOffset: overlaySideOffset, collisionPadding: 16 }
          "
          @keydown.enter="confirmKey"
        >
          <AppDialogHeader
            v-if="mobile"
            :title="label"
            :close-label="t('ui.close')"
            @close="open = false"
          />
          <div class="modern-date-body">
            <div
              v-if="shortcuts.length"
              class="modern-date-shortcuts"
              :aria-label="t('ui.date.shortcuts')"
            >
              <AppButton
                v-for="option in shortcuts"
                :key="option.label"
                size="xxs"
                :variant="activeShortcut === option.label ? 'brand' : 'ghost'"
                :aria-pressed="activeShortcut === option.label"
                @click="shortcut(option)"
                >{{ option.label }}</AppButton
              >
            </div>
            <AppDateTimeEditor
              v-model="draft"
              :label="label"
              :min="min"
              :max="max"
              :invalid="!valid"
              @update:model-value="activeShortcut = ''"
            />
            <p v-if="!valid" class="modern-date-error" role="status">{{ t('ui.date.invalid') }}</p>
          </div>
          <footer class="modern-date-footer">
            <span class="modern-date-zone">{{ localTimeZone() }}</span>
            <div>
              <AppButton v-if="clearable && model" variant="ghost" size="xs" @click="clear">{{
                t('ui.date.clear')
              }}</AppButton
              ><AppButton variant="ghost" size="xs" @click="open = false">{{
                t('ui.cancel')
              }}</AppButton
              ><AppButton variant="primary" size="xs" :disabled="!valid" @click="apply">{{
                t('ui.date.apply')
              }}</AppButton>
            </div>
          </footer>
        </component></component
      >
    </component>
  </AppField>
</template>
