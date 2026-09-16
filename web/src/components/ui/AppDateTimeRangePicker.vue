<script setup lang="ts">
import { CalendarDate, Time, type DateValue } from '@internationalized/date'
import { CalendarClock, ChevronLeft, ChevronRight } from '@lucide/vue'
import { computed, nextTick, ref, shallowRef, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  RangeCalendarCell,
  RangeCalendarCellTrigger,
  RangeCalendarGrid,
  RangeCalendarGridBody,
  RangeCalendarGridHead,
  RangeCalendarGridRow,
  RangeCalendarHeadCell,
  RangeCalendarHeader,
  RangeCalendarHeading,
  RangeCalendarNext,
  RangeCalendarPrev,
  RangeCalendarRoot,
  TimeFieldInput,
  TimeFieldRoot,
  type TimeValue,
} from 'reka-ui'

import {
  dateTimePresets,
  localDateTimeInput,
  resolveDateTimePreset,
  type DateTimePreset,
} from '@/lib/time'

import AppButton from './AppButton.vue'
import AppPopover from './AppPopover.vue'
import FormField from './FormField.vue'

type CalendarRange = {
  start: DateValue | undefined
  end: DateValue | undefined
}

const props = defineProps<{
  from: string
  to: string
  appliedFrom?: string
  appliedTo?: string
  appliedPreset?: DateTimePreset
  label: string
  fromLabel: string
  toLabel: string
  fromError?: string
  toError?: string
  preset?: DateTimePreset
  applyLabel?: string
  applyDisabled?: boolean
}>()
const emit = defineEmits<{
  'update:from': [value: string]
  'update:to': [value: string]
  'update:preset': [value: DateTimePreset | undefined]
  shortcut: [preset: DateTimePreset, from: number, to: number]
  apply: []
  open: []
}>()
const { t, locale } = useI18n()
const open = ref(false)
const fieldID = useId()
const selectingDateRange = ref(false)
const calendarRange = shallowRef<CalendarRange>(calendarRangeFromInputs(props.from, props.to))
const calendarApplyDisabled = computed(() => props.applyDisabled || selectingDateRange.value)

const rangeDisplay = computed(
  () =>
    `${displayValue(props.appliedFrom ?? props.from)} → ${displayValue(props.appliedTo ?? props.to)}`,
)
const display = computed(() => {
  const preset = props.appliedPreset
  if (!preset) return rangeDisplay.value
  if (preset === 'today' || preset === 'yesterday') {
    return t(`monitor.logs.filters.quick.${preset}`)
  }
  return t(`monitor.logs.filters.quickDisplay.${preset}`)
})

function changeOpen(value: boolean): void {
  open.value = value
  if (!value) {
    selectingDateRange.value = false
    return
  }
  emit('open')
  void nextTick(() => {
    selectingDateRange.value = false
    syncCalendarRange()
  })
}

function displayValue(value: string): string {
  const normalized = normalizeLocalInputValue(value)
  return normalized ? normalized.replace('T', ' ') : '—'
}

function normalizeLocalInputValue(value: string): string {
  if (!value) return ''
  return /^\d{4,}-\d{2}-\d{2}T\d{2}:\d{2}$/u.test(value) ? `${value}:00` : value
}

function inputCalendarDate(value: string): CalendarDate | undefined {
  const match = normalizeLocalInputValue(value).match(/^(\d{4,})-(\d{2})-(\d{2})T/u)
  if (!match) return undefined
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  if (!Number.isSafeInteger(year) || year < 1) return undefined
  try {
    const date = new CalendarDate(year, month, day)
    return date.year === year && date.month === month && date.day === day ? date : undefined
  } catch {
    return undefined
  }
}

function calendarRangeFromInputs(from: string, to: string): CalendarRange {
  return {
    start: inputCalendarDate(from),
    end: inputCalendarDate(to),
  }
}

function syncCalendarRange(): void {
  calendarRange.value = calendarRangeFromInputs(props.from, props.to)
}

function calendarDateString(value: DateValue): string {
  const pad = (part: number, length = 2) => String(part).padStart(length, '0')
  return `${pad(value.year, 4)}-${pad(value.month)}-${pad(value.day)}`
}

function datePart(value: string): string {
  return normalizeLocalInputValue(value).match(/^(\d{4,}-\d{2}-\d{2})T/u)?.[1] ?? ''
}

function timePart(value: string): string {
  return normalizeLocalInputValue(value).match(/T(\d{2}:\d{2}:\d{2})(?:\.\d{1,3})?$/u)?.[1] ?? ''
}

function inputTimeValue(value: string): Time | undefined {
  const match = timePart(value).match(/^(\d{2}):(\d{2}):(\d{2})$/u)
  if (!match) return undefined
  return new Time(Number(match[1]), Number(match[2]), Number(match[3]))
}

function timeValueString(value: TimeValue): string {
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${pad(value.hour)}:${pad(value.minute)}:${pad(value.second)}`
}

function combineDateAndTime(date: DateValue, currentValue: string): string {
  return `${calendarDateString(date)}T${timePart(currentValue) || '00:00:00'}`
}

function calendarDateForField(field: 'from' | 'to'): DateValue | undefined {
  return field === 'from' ? calendarRange.value.start : calendarRange.value.end
}

function updateLocalTime(field: 'from' | 'to', value: TimeValue | undefined): void {
  emit('update:preset', undefined)
  const currentValue = field === 'from' ? props.from : props.to
  const selectedDate = calendarDateForField(field)
  const selectedDatePart = selectedDate ? calendarDateString(selectedDate) : ''
  const currentDatePart = datePart(currentValue)
  const resolvedDatePart = currentDatePart || selectedDatePart
  const normalized =
    value && resolvedDatePart ? `${resolvedDatePart}T${timeValueString(value)}` : ''
  if (field === 'from') {
    emit('update:from', normalized)
  } else {
    emit('update:to', normalized)
  }
}

function selectShortcut(preset: DateTimePreset): void {
  const now = Math.floor(Date.now() / 1000) * 1000
  const range = resolveDateTimePreset(preset, now)
  const from = localDateTimeInput(range.from_ms)
  const to = localDateTimeInput(range.to_ms)
  selectingDateRange.value = false
  calendarRange.value = calendarRangeFromInputs(from, to)
  emit('update:from', from)
  emit('update:to', to)
  emit('update:preset', preset)
  emit('shortcut', preset, range.from_ms, range.to_ms)
  if (props.applyLabel && range.to_ms > range.from_ms) open.value = false
}

function updateCalendarRange(value: CalendarRange): void {
  calendarRange.value = value
  emit('update:preset', undefined)
  if (!value.start || !value.end) {
    selectingDateRange.value = Boolean(value.start)
    return
  }
  selectingDateRange.value = false
  emit('update:from', combineDateAndTime(value.start, props.from))
  emit('update:to', combineDateAndTime(value.end, props.to))
}

function formatCalendarMonth(value: DateValue): string {
  const date = new Date(Date.UTC(value.year, value.month - 1, 1))
  return new Intl.DateTimeFormat(locale.value, {
    year: 'numeric',
    month: 'long',
    timeZone: 'UTC',
  }).format(date)
}

function apply(): void {
  if (calendarApplyDisabled.value) return
  emit('update:preset', undefined)
  emit('apply')
  open.value = false
}

watch(
  () => [props.from, props.to] as const,
  () => {
    if (!selectingDateRange.value) syncCalendarRange()
  },
)
</script>

<template>
  <AppPopover
    :open="open"
    align="start"
    :content-class="
      applyLabel
        ? 'app-date-range-popover app-date-range-popover--with-apply'
        : 'app-date-range-popover'
    "
    @update:open="changeOpen"
  >
    <template #trigger>
      <AppButton
        class="app-date-range__trigger"
        :class="{ 'app-date-range__trigger--custom': !appliedPreset }"
        variant="secondary"
        size="compact"
        :aria-label="`${label}: ${display}`"
      >
        <CalendarClock :size="14" aria-hidden="true" />
        <span>{{ display }}</span>
      </AppButton>
    </template>

    <div class="app-date-range__shortcuts" :aria-label="t('monitor.logs.filters.quickRanges')">
      <AppButton
        v-for="shortcut in dateTimePresets"
        :key="shortcut"
        variant="ghost"
        size="compact"
        :aria-pressed="preset === shortcut"
        @click="selectShortcut(shortcut)"
      >
        {{ t(`monitor.logs.filters.quick.${shortcut}`) }}
      </AppButton>
    </div>
    <RangeCalendarRoot
      v-slot="{ grid, weekDays }"
      class="app-date-range__calendar"
      :model-value="calendarRange"
      :locale="locale"
      :number-of-months="2"
      :calendar-label="t('monitor.logs.filters.calendar')"
      fixed-weeks
      prevent-deselect
      @update:model-value="updateCalendarRange"
    >
      <RangeCalendarHeader class="app-date-range__calendar-header">
        <RangeCalendarPrev
          class="app-date-range__calendar-nav"
          :aria-label="t('monitor.logs.filters.previousMonth')"
        >
          <ChevronLeft :size="16" aria-hidden="true" />
        </RangeCalendarPrev>
        <RangeCalendarHeading class="sr-only" />
        <h3
          v-for="month in grid"
          :key="month.value.toString()"
          class="app-date-range__calendar-header-heading"
          aria-hidden="true"
        >
          {{ formatCalendarMonth(month.value) }}
        </h3>
        <RangeCalendarNext
          class="app-date-range__calendar-nav"
          :aria-label="t('monitor.logs.filters.nextMonth')"
        >
          <ChevronRight :size="16" aria-hidden="true" />
        </RangeCalendarNext>
      </RangeCalendarHeader>
      <div class="app-date-range__calendar-months">
        <section
          v-for="month in grid"
          :key="month.value.toString()"
          class="app-date-range__calendar-month"
        >
          <h3 class="app-date-range__calendar-month-heading" aria-hidden="true">
            {{ formatCalendarMonth(month.value) }}
          </h3>
          <RangeCalendarGrid class="app-date-range__calendar-grid">
            <RangeCalendarGridHead>
              <RangeCalendarGridRow>
                <RangeCalendarHeadCell
                  v-for="weekDay in weekDays"
                  :key="weekDay"
                  class="app-date-range__calendar-weekday"
                >
                  {{ weekDay }}
                </RangeCalendarHeadCell>
              </RangeCalendarGridRow>
            </RangeCalendarGridHead>
            <RangeCalendarGridBody>
              <RangeCalendarGridRow v-for="(weekDates, index) in month.rows" :key="index">
                <RangeCalendarCell
                  v-for="day in weekDates"
                  :key="day.toString()"
                  class="app-date-range__calendar-cell"
                  :date="day"
                >
                  <RangeCalendarCellTrigger
                    class="app-date-range__calendar-day"
                    :day="day"
                    :month="month.value"
                  >
                    {{ day.day }}
                  </RangeCalendarCellTrigger>
                </RangeCalendarCell>
              </RangeCalendarGridRow>
            </RangeCalendarGridBody>
          </RangeCalendarGrid>
        </section>
      </div>
    </RangeCalendarRoot>
    <div
      class="app-date-range__fields"
      :class="{ 'app-date-range__fields--with-apply': applyLabel }"
    >
      <FormField :id="`${fieldID}-from`" :label="fromLabel" size="compact" :error="fromError">
        <template #default="{ describedBy, invalid }">
          <TimeFieldRoot
            :id="`${fieldID}-from`"
            v-slot="{ segments }"
            class="app-date-range__input-shell app-date-range__time-field"
            :model-value="inputTimeValue(from)"
            :locale="locale"
            :hour-cycle="24"
            granularity="second"
            :aria-label="fromLabel"
            :aria-describedby="describedBy"
            :aria-invalid="invalid || undefined"
            data-input-shell
            @update:model-value="updateLocalTime('from', $event)"
          >
            <TimeFieldInput
              v-for="(segment, index) in segments"
              :key="`${segment.part}:${index}`"
              as="span"
              class="app-date-range__time-segment"
              :part="segment.part"
            >
              {{ segment.value }}
            </TimeFieldInput>
          </TimeFieldRoot>
        </template>
      </FormField>
      <FormField
        :id="`${fieldID}-to`"
        class="app-date-range__end-field"
        :label="toLabel"
        size="compact"
        :error="toError"
      >
        <template #default="{ describedBy, invalid }">
          <div class="app-date-range__end-controls">
            <TimeFieldRoot
              :id="`${fieldID}-to`"
              v-slot="{ segments }"
              class="app-date-range__input-shell app-date-range__time-field"
              :model-value="inputTimeValue(to)"
              :locale="locale"
              :hour-cycle="24"
              granularity="second"
              :aria-label="toLabel"
              :aria-describedby="describedBy"
              :aria-invalid="invalid || undefined"
              data-input-shell
              @update:model-value="updateLocalTime('to', $event)"
            >
              <TimeFieldInput
                v-for="(segment, index) in segments"
                :key="`${segment.part}:${index}`"
                as="span"
                class="app-date-range__time-segment"
                :part="segment.part"
              >
                {{ segment.value }}
              </TimeFieldInput>
            </TimeFieldRoot>
            <AppButton
              v-if="applyLabel"
              class="app-date-range__apply"
              size="compact"
              :disabled="calendarApplyDisabled"
              @click="apply"
            >
              {{ applyLabel }}
            </AppButton>
          </div>
        </template>
      </FormField>
    </div>
  </AppPopover>
</template>

<style>
.app-date-range__trigger {
  max-width: 380px;
}

.app-date-range__trigger > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.app-date-range__trigger--custom > span {
  font-family: var(--font-mono);
}

.app-date-range-popover {
  --date-range-control-height: var(--control-xs);
  width: min(500px, var(--reka-popover-content-available-width));
  padding: 14px;
}

.app-date-range-popover--with-apply {
  width: min(520px, var(--reka-popover-content-available-width));
}

.app-date-range__shortcuts {
  display: flex;
  min-width: 0;
  flex-wrap: nowrap;
  gap: 2px;
  overflow-x: auto;
  overscroll-behavior-x: contain;
  scrollbar-width: thin;
  border-bottom: 1px solid var(--color-border-subtle);
  padding-bottom: 10px;
}

.app-date-range__shortcuts .app-button {
  flex: 1 0 auto;
  padding-inline: 6px;
  white-space: nowrap;
}

.app-date-range__shortcuts .app-button[aria-pressed='true'] {
  background: var(--color-action-soft);
  color: var(--color-action);
}

.app-date-range__calendar {
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 12px 0;
}

.app-date-range__calendar-header {
  display: grid;
  grid-template-columns: 30px repeat(2, minmax(0, 1fr)) 30px;
  align-items: center;
  gap: 12px;
  margin-bottom: 6px;
}

.app-date-range__calendar-header-heading,
.app-date-range__calendar-month-heading {
  margin: 0;
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 650;
  text-align: center;
}

.app-date-range__calendar-header-heading {
  min-width: 0;
}

.app-date-range__calendar-header-heading:first-of-type {
  grid-column: 2;
}

.app-date-range__calendar-header-heading:last-of-type {
  grid-column: 3;
}

.app-date-range__calendar-header .app-date-range__calendar-nav:last-child {
  grid-column: 4;
}

.app-date-range__calendar-nav {
  display: inline-flex;
  width: 30px;
  height: 30px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-tag);
  background: transparent;
  color: var(--color-text-muted);
  padding: 0;
  cursor: pointer;
}

.app-date-range__calendar-nav:hover:not([data-disabled]) {
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.app-date-range__calendar-nav:focus-visible {
  outline: 2px solid var(--color-action);
  outline-offset: 1px;
}

.app-date-range__calendar-nav[data-disabled] {
  cursor: not-allowed;
  opacity: 0.45;
}

.app-date-range__calendar-months {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.app-date-range__calendar-month {
  min-width: 0;
}

.app-date-range__calendar-month-heading {
  display: none;
  margin-bottom: 6px;
}

.app-date-range__calendar-grid {
  width: 100%;
  table-layout: fixed;
  border-collapse: collapse;
}

.app-date-range__calendar-weekday {
  height: 26px;
  color: var(--color-text-faint);
  font-size: var(--text-meta);
  font-weight: 500;
  text-align: center;
}

.app-date-range__calendar-cell {
  height: 32px;
  padding: 1px;
  text-align: center;
}

.app-date-range__calendar-day {
  display: inline-flex;
  width: 100%;
  height: 30px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-tag);
  background: transparent;
  color: var(--color-text-muted);
  padding: 0;
  font: inherit;
  font-size: var(--text-meta);
  cursor: pointer;
  outline: 0;
}

.app-date-range__calendar-day:hover:not([data-disabled]):not([data-outside-view]),
.app-date-range__calendar-day[data-focused]:not([data-selected]) {
  background: var(--color-surface-sunken);
  color: var(--color-text);
}

.app-date-range__calendar-day[data-highlighted],
.app-date-range__calendar-day[data-selected] {
  background: var(--color-action-soft);
  color: var(--color-action);
}

.app-date-range__calendar-day[data-selection-start],
.app-date-range__calendar-day[data-selection-end] {
  background: var(--color-action);
  color: var(--color-action-ink);
}

.app-date-range__calendar-day[data-today] {
  box-shadow: inset 0 0 0 1px var(--color-action);
}

.app-date-range__calendar-day[data-outside-view] {
  visibility: hidden;
}

.app-date-range__calendar-day[data-disabled],
.app-date-range__calendar-day[data-unavailable] {
  cursor: not-allowed;
  opacity: 0.42;
}

.app-date-range__end-controls .app-date-range__apply {
  min-height: var(--date-range-control-height);
  height: var(--date-range-control-height);
}

.app-date-range__fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-items: start;
  gap: 10px;
  padding-top: 12px;
}

.app-date-range__fields--with-apply {
  grid-template-columns: repeat(2, minmax(0, 1fr)) auto;
}

.app-date-range__fields--with-apply .app-date-range__end-field {
  grid-column: 2 / -1;
  grid-template-columns: subgrid;
}

.app-date-range__end-controls {
  display: grid;
  min-width: 0;
  align-items: center;
}

.app-date-range__fields--with-apply .app-date-range__end-controls {
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
}

.app-date-range__fields--with-apply .app-date-range__end-field > .form-field__error {
  grid-column: 1 / -1;
}

.app-date-range__input-shell {
  position: relative;
  display: flex;
  width: 100%;
  min-width: 0;
  min-height: var(--date-range-control-height);
  height: var(--date-range-control-height);
  align-items: center;
  overflow: hidden;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
}

.app-date-range__time-field {
  gap: 0;
  padding: 0 10px;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-variant-numeric: tabular-nums;
  cursor: text;
}

.app-date-range__time-segment {
  min-width: 2ch;
  border-radius: 3px;
  color: var(--color-text);
  caret-color: transparent;
  outline: 0;
  text-align: center;
}

.app-date-range__time-segment[data-reka-time-field-segment='literal'] {
  min-width: auto;
  color: var(--color-text-faint);
  padding-inline: 1px;
  pointer-events: none;
}

.app-date-range__time-segment[data-placeholder] {
  color: var(--color-text-faint);
}

.app-date-range__time-segment:focus {
  background: var(--color-action-soft);
  color: var(--color-action);
}

.app-date-range__input-shell:focus-within {
  border-color: var(--color-action);
  box-shadow: 0 0 0 2px var(--color-action-soft);
}

@media (max-width: 560px) {
  .app-date-range-popover {
    --date-range-control-height: var(--touch-target);
  }

  .app-date-range__trigger {
    width: 100%;
    max-width: none;
    min-height: var(--touch-target);
  }

  .app-date-range__calendar-months {
    grid-template-columns: minmax(0, 1fr);
  }

  .app-date-range__calendar-header {
    grid-template-columns: 30px minmax(0, 1fr) 30px;
    gap: 4px;
  }

  .app-date-range__calendar-header-heading:first-of-type {
    grid-column: 2;
  }

  .app-date-range__calendar-header-heading:last-of-type {
    display: none;
  }

  .app-date-range__calendar-header .app-date-range__calendar-nav:last-child {
    grid-column: 3;
  }

  .app-date-range__calendar-month:not(:first-child) .app-date-range__calendar-month-heading {
    display: block;
  }

  .app-date-range__fields {
    grid-template-columns: minmax(0, 1fr);
  }

  .app-date-range__fields--with-apply .app-date-range__end-field {
    grid-column: 1;
    grid-template-columns: minmax(0, 1fr);
  }

  .app-date-range__fields--with-apply .app-date-range__end-controls {
    row-gap: 10px;
  }

  .app-date-range__apply {
    width: 100%;
  }
}
</style>
