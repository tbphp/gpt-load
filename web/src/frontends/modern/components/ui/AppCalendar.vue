<script setup lang="ts">
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import { ChevronLeft, ChevronRight } from '@lucide/vue'
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'
import AppIconButton from './AppIconButton.vue'
import AppSearchSelect from './AppSearchSelect.vue'
import AppSelect from './AppSelect.vue'
import { localDay, parseLocalDateTime } from './date-time'
const props = defineProps<{
  modelValue: string
  label: string
  min?: string
  max?: string
  range?: { from: string; to: string }
}>()
const emit = defineEmits<{ 'update:modelValue': [date: string] }>()
const { t, locale } = useI18n()
const grid = ref<HTMLElement>()
const focused = ref('')
const month = ref(new Date())
const minimum = computed(() => props.min?.slice(0, 10))
const maximum = computed(() => props.max?.slice(0, 10))
const dateFor = (value: string) => parseLocalDateTime(value + 'T12:00:00')
function allowed(value: string): boolean {
  return (!minimum.value || value >= minimum.value) && (!maximum.value || value <= maximum.value)
}
function view(date: Date): void {
  month.value = new Date(date.getTime())
  month.value.setDate(1)
  if (focused.value.slice(0, 7) !== localDay(month.value).slice(0, 7))
    focused.value = limit(localDay(month.value))
}
function limit(value: string): string {
  return minimum.value && value < minimum.value
    ? minimum.value
    : maximum.value && value > maximum.value
      ? maximum.value
      : value
}
watch(
  () => [props.modelValue, minimum.value, maximum.value],
  () => {
    const value = limit(dateFor(props.modelValue) ? props.modelValue : localDay(new Date()))
    focused.value = value
    view(dateFor(value) ?? new Date())
  },
  { immediate: true },
)
const yearValue = computed({
  get: () => String(month.value.getFullYear()),
  set: (value: string) => {
    const year = Number(value)
    if (Number.isInteger(year) && year > 0 && year <= 9999) {
      const next = new Date(month.value)
      next.setFullYear(year)
      view(next)
    }
  },
})
const monthValue = computed({
  get: () => String(month.value.getMonth()),
  set: (value: string) => {
    const next = new Date(month.value)
    next.setMonth(Number(value))
    view(next)
  },
})
const years = computed(() =>
  Array.from({ length: 201 }, (_, index) => month.value.getFullYear() - 100 + index)
    .filter((year) => year > 0 && year <= 9999)
    .map((year) => ({ value: String(year), label: String(year) })),
)
const months = computed(() =>
  Array.from({ length: 12 }, (_, index) => ({
    value: String(index),
    label: dateFormatter(locale.value, { month: 'long' }).format(new Date(2024, index, 1)),
  })),
)
const weekdays = computed(() =>
  Array.from({ length: 7 }, (_, index) =>
    dateFormatter(locale.value, { weekday: 'short' }).format(new Date(2024, 0, 1 + index)),
  ),
)
const weeks = computed(() => {
  const first = new Date(month.value)
  first.setDate(1 - ((first.getDay() + 6) % 7))
  const cells = Array.from({ length: 42 }, (_, index) => {
    const date = new Date(first)
    date.setDate(date.getDate() + index)
    const value = localDay(date)
    return {
      value,
      date,
      day: date.getDate(),
      outside: date.getMonth() !== month.value.getMonth(),
      disabled: !allowed(value) || date.getFullYear() < 1 || date.getFullYear() > 9999,
      label: dateFormatter(locale.value, { dateStyle: 'full' }).format(date),
    }
  })
  return Array.from({ length: 6 }, (_, index) => cells.slice(index * 7, index * 7 + 7))
})
function moveMonth(offset: number): void {
  const next = new Date(month.value)
  next.setMonth(next.getMonth() + offset)
  if (next.getFullYear() < 1 || next.getFullYear() > 9999) return
  view(next)
  focused.value = limit(localDay(next))
}
function select(value: string): void {
  if (allowed(value)) {
    focused.value = value
    emit('update:modelValue', value)
  }
}
async function navigate(event: KeyboardEvent, value: string): Promise<void> {
  const date = dateFor(value)
  if (!date) return
  const day = (date.getDay() + 6) % 7
  const offsets: Record<string, number> = {
    ArrowLeft: -1,
    ArrowRight: 1,
    ArrowUp: -7,
    ArrowDown: 7,
    Home: -day,
    End: 6 - day,
  }
  const offset = offsets[event.key]
  if (offset !== undefined) date.setDate(date.getDate() + offset)
  else if (event.key === 'PageUp' || event.key === 'PageDown') {
    const delta = (event.key === 'PageUp' ? -1 : 1) * (event.shiftKey ? 12 : 1)
    const originalDay = date.getDate()
    date.setDate(1)
    date.setMonth(date.getMonth() + delta)
    const end = new Date(date)
    end.setMonth(end.getMonth() + 1, 0)
    date.setDate(Math.min(originalDay, end.getDate()))
  } else return
  event.preventDefault()
  if (date.getFullYear() < 1 || date.getFullYear() > 9999) return
  const target = limit(localDay(date))
  focused.value = target
  view(dateFor(target) ?? date)
  await nextTick()
  grid.value?.querySelector<HTMLButtonElement>(`[data-date="${target}"]`)?.focus()
}
function inRange(value: string): boolean {
  return Boolean(
    props.range?.from &&
    props.range?.to &&
    value > props.range.from.slice(0, 10) &&
    value < props.range.to.slice(0, 10),
  )
}
</script>
<template>
  <div class="modern-calendar">
    <div class="modern-calendar-heading">
      <AppIconButton
        :icon="ChevronLeft"
        :label="t('ui.date.previousMonth')"
        size="xs"
        @click="moveMonth(-1)"
      />
      <AppSearchSelect
        v-model="yearValue"
        :label="t('ui.date.year')"
        label-hidden
        :options="years"
        size="xs"
      />
      <AppSelect
        v-model="monthValue"
        :label="t('ui.date.month')"
        label-hidden
        :options="months"
        size="xs"
      />
      <AppIconButton
        :icon="ChevronRight"
        :label="t('ui.date.nextMonth')"
        size="xs"
        @click="moveMonth(1)"
      />
    </div>
    <div ref="grid" role="grid" :aria-label="label" class="modern-calendar-grid">
      <div role="row" class="modern-calendar-weekdays">
        <span v-for="(day, index) in weekdays" :key="index" role="columnheader">{{ day }}</span>
      </div>
      <div v-for="(week, index) in weeks" :key="index" role="row" class="modern-calendar-week">
        <div
          v-for="day in week"
          :key="day.value"
          role="gridcell"
          :aria-selected="day.value === modelValue"
          class="modern-calendar-cell"
          :class="{ 'is-range': inRange(day.value) }"
        >
          <AppButton
            class="modern-calendar-day"
            :class="{ 'is-outside': day.outside, 'is-today': day.value === localDay(new Date()) }"
            :variant="day.value === modelValue ? 'primary' : 'ghost'"
            size="xxs"
            :disabled="day.disabled"
            :tabindex="day.value === focused ? 0 : -1"
            :aria-label="day.label"
            :aria-current="day.value === localDay(new Date()) ? 'date' : undefined"
            :data-date="day.value"
            @click="select(day.value)"
            @keydown="navigate($event, day.value)"
            >{{ day.day }}</AppButton
          >
        </div>
      </div>
    </div>
  </div>
</template>
<style scoped>
.modern-calendar {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-1);
}
.modern-calendar-heading {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-calendar-grid {
  display: grid;
  gap: var(--modern-space-0-5);
}
.modern-calendar-weekdays,
.modern-calendar-week {
  display: grid;
  grid-template-columns: repeat(7, minmax(0, 1fr));
  gap: var(--modern-space-0-5);
}
.modern-calendar-weekdays {
  padding-block: var(--modern-space-0-5);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  text-align: center;
}
.modern-calendar-cell {
  min-width: 0;
  border-radius: var(--modern-radius-small);
}
.modern-calendar-cell.is-range {
  background: var(--modern-accent-soft);
}
.modern-calendar-day {
  width: 100%;
  padding: 0;
  font-variant-numeric: tabular-nums;
}
.modern-calendar-day.is-outside:not(:disabled) {
  opacity: var(--modern-opacity-quiet);
}
.modern-calendar-day.is-today {
  box-shadow: inset 0 0 0 var(--modern-line-width) var(--modern-tooltip-border);
}
</style>
