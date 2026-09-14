<script setup lang="ts">
import { CalendarClock } from '@lucide/vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppDateTimeEditor from './AppDateTimeEditor.vue'
import AppField from './AppField.vue'
import AppFieldControl from './AppFieldControl.vue'
import AppIcon from './AppIcon.vue'
import AppOverflowText from './AppOverflowText.vue'
import AppSegmentedControl from './AppSegmentedControl.vue'
import { layoutAttrs } from './field-attrs'
import {
  dateRangeFor,
  dateRangePresets,
  dateWithinBounds,
  formatLocalDateTime,
  localTimeZone,
  parseLocalDateTime,
  type DateRangePreset,
} from './date-time'
import { overlaySideOffset } from './overlay'
import type { ControlSize, FieldProps } from './types'
import './date-time.css'
defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<
    FieldProps & {
      min?: string
      max?: string
      size?: ControlSize
      preset?: DateRangePreset
      presets?: readonly DateRangePreset[]
    }
  >(),
  {
    min: undefined,
    max: undefined,
    preset: undefined,
    size: 'sm',
    presets: () => dateRangePresets,
  },
)
const from = defineModel<string>('from', { required: true })
const to = defineModel<string>('to', { required: true })
const emit = defineEmits<{ apply: []; 'update:preset': [value: DateRangePreset | undefined] }>()
const { t } = useI18n()
const open = ref(false)
const start = ref('')
const end = ref('')
const chosen = ref<DateRangePreset>()
const activeBoundary = ref('from')
const startValid = computed(() => dateWithinBounds(start.value, props.min, props.max))
const endValid = computed(() => dateWithinBounds(end.value, props.min, props.max))
const ordered = computed(
  () =>
    (parseLocalDateTime(start.value)?.getTime() ?? Infinity) <
    (parseLocalDateTime(end.value)?.getTime() ?? -Infinity),
)
const valid = computed(() => startValid.value && endValid.value && ordered.value)
const display = computed(() =>
  props.preset
    ? t('ui.date.ranges.' + props.preset)
    : from.value && to.value
      ? `${from.value.replace('T', ' ')} → ${to.value.replace('T', ' ')}`
      : t('ui.date.rangePlaceholder'),
)
watch(open, (value) => {
  if (!value) return
  const fallback = dateRangeFor(props.preset ?? '24h')
  start.value = from.value || fallback.from
  end.value = to.value || fallback.to
  chosen.value = props.preset
  activeBoundary.value = 'from'
})
watch(
  () => props.disabled,
  (disabled) => {
    if (disabled) open.value = false
  },
)
function shortcut(value: DateRangePreset): void {
  if (props.disabled) return
  const range = dateRangeFor(value)
  start.value = range.from
  end.value = range.to
  chosen.value = value
  submit(value)
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
  // 确认当前输入始终固定为自定义区间，即使它来自先前的快捷日期。
  submit(undefined)
}
function submit(preset: DateRangePreset | undefined): void {
  if (!valid.value || props.disabled) return
  from.value = formatLocalDateTime(parseLocalDateTime(start.value)!)
  to.value = formatLocalDateTime(parseLocalDateTime(end.value)!)
  emit('update:preset', preset)
  emit('apply')
  open.value = false
}
</script>
<template>
  <AppField v-slot="{ id, describedBy, invalid }" v-bind="{ ...props, ...layoutAttrs($attrs) }">
    <PopoverRoot v-model:open="open">
      <AppFieldControl as-child :size="size" :invalid="invalid" :disabled="disabled"
        ><PopoverTrigger as-child
          ><AppButton
            :id="id"
            :size="size"
            class="modern-date-trigger"
            :disabled="disabled"
            :aria-labelledby="`${id}-label`"
            :aria-describedby="describedBy"
            :aria-invalid="invalid || undefined"
            ><AppIcon :icon="CalendarClock" size="sm" /><AppOverflowText
              :text="display" /></AppButton></PopoverTrigger
      ></AppFieldControl>
      <PopoverPortal
        ><PopoverContent
          class="modern-date-popover modern-date-popover--range"
          align="start"
          :side-offset="overlaySideOffset"
          :collision-padding="16"
          @keydown.enter="confirmKey"
        >
          <div class="modern-date-shortcuts" :aria-label="t('ui.date.shortcuts')">
            <AppButton
              v-for="option in presets"
              :key="option"
              :variant="chosen === option ? 'brand' : 'ghost'"
              size="xxs"
              :aria-pressed="chosen === option"
              @click="shortcut(option)"
              >{{ t('ui.date.ranges.' + option) }}</AppButton
            >
          </div>
          <AppSegmentedControl
            v-model="activeBoundary"
            class="modern-date-range-switch"
            :label="t('ui.date.rangePlaceholder')"
            :options="[
              { value: 'from', label: t('ui.date.from') },
              { value: 'to', label: t('ui.date.to') },
            ]"
            appearance="field"
            size="xs"
          />
          <div class="modern-date-range-editors">
            <section :class="{ 'is-active': activeBoundary === 'from' }">
              <h4>{{ t('ui.date.from') }}</h4>
              <AppDateTimeEditor
                v-model="start"
                :label="t('ui.date.from')"
                :min="min"
                :max="max"
                :invalid="!startValid"
                :range="{ from: start, to: end }"
                @update:model-value="chosen = undefined"
              />
            </section>
            <section :class="{ 'is-active': activeBoundary === 'to' }">
              <h4>{{ t('ui.date.to') }}</h4>
              <AppDateTimeEditor
                v-model="end"
                :label="t('ui.date.to')"
                :min="min"
                :max="max"
                :invalid="!endValid || !ordered"
                :range="{ from: start, to: end }"
                @update:model-value="chosen = undefined"
              />
            </section>
          </div>
          <p v-if="!valid" class="modern-date-error" role="status">
            {{ t(startValid && endValid ? 'ui.date.orderError' : 'ui.date.invalid') }}
          </p>
          <footer class="modern-date-footer">
            <span class="modern-date-zone">{{ localTimeZone() }}</span>
            <div>
              <AppButton variant="ghost" size="xs" @click="open = false">{{
                t('ui.cancel')
              }}</AppButton
              ><AppButton variant="primary" size="xs" :disabled="!valid" @click="apply">{{
                t('ui.date.apply')
              }}</AppButton>
            </div>
          </footer>
        </PopoverContent></PopoverPortal
      >
    </PopoverRoot>
  </AppField>
</template>
<style>
.modern-date-popover--range {
  width: min(600px, calc(100vw - var(--modern-space-8)));
}
.modern-date-popover--range .modern-date-range-switch {
  display: none;
}
.modern-date-range-editors {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-3);
}
.modern-date-range-editors h4 {
  margin: 0 0 var(--modern-space-2);
  color: var(--modern-text);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
@media (max-width: 760px) {
  .modern-date-popover--range {
    width: min(300px, calc(100vw - var(--modern-space-8)));
  }
  .modern-date-popover--range .modern-date-range-switch {
    display: inline-flex;
    margin-bottom: var(--modern-space-2);
  }
  .modern-date-range-editors {
    grid-template-columns: minmax(0, 1fr);
  }
  .modern-date-range-editors > section:not(.is-active),
  .modern-date-range-editors h4 {
    display: none;
  }
}
</style>
