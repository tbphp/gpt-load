<script setup lang="ts">
import { CalendarClock } from '@lucide/vue'
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  dateTimePresets,
  localDateTimeInput,
  resolveDateTimePreset,
  type DateTimePreset,
} from '@/lib/time'

import AppButton from './AppButton.vue'
import AppPopover from './AppPopover.vue'
import FormField from './FormField.vue'

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
const { t } = useI18n()
const open = ref(false)
const fieldID = useId()

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
  if (value) emit('open')
}

function displayValue(value: string): string {
  const normalized = normalizeLocalInputValue(value)
  return normalized ? normalized.replace('T', ' ') : '—'
}

function normalizeLocalInputValue(value: string): string {
  if (!value) return ''
  return /^\d{4,}-\d{2}-\d{2}T\d{2}:\d{2}$/u.test(value) ? `${value}:00` : value
}

function updateLocalInput(field: 'from' | 'to', value: string): void {
  emit('update:preset', undefined)
  const normalized = normalizeLocalInputValue(value)
  if (field === 'from') {
    emit('update:from', normalized)
  } else {
    emit('update:to', normalized)
  }
}

function selectShortcut(preset: DateTimePreset): void {
  const now = Math.floor(Date.now() / 1000) * 1000
  const range = resolveDateTimePreset(preset, now)
  emit('update:from', localDateTimeInput(range.from_ms))
  emit('update:to', localDateTimeInput(range.to_ms))
  emit('update:preset', preset)
  emit('shortcut', preset, range.from_ms, range.to_ms)
  if (props.applyLabel && range.to_ms > range.from_ms) open.value = false
}

function apply(): void {
  if (props.applyDisabled) return
  emit('update:preset', undefined)
  emit('apply')
  open.value = false
}
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
    <div
      class="app-date-range__fields"
      :class="{ 'app-date-range__fields--with-apply': applyLabel }"
    >
      <FormField :id="`${fieldID}-from`" :label="fromLabel" size="compact" :error="fromError">
        <template #default="{ describedBy, invalid }">
          <span class="app-date-range__input-shell">
            <input
              :id="`${fieldID}-from`"
              class="app-date-range__native-input"
              :value="from"
              type="datetime-local"
              step="1"
              :aria-label="fromLabel"
              :aria-describedby="describedBy"
              :aria-invalid="invalid || undefined"
              @input="updateLocalInput('from', ($event.target as HTMLInputElement).value)"
            />
          </span>
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
            <span class="app-date-range__input-shell">
              <input
                :id="`${fieldID}-to`"
                class="app-date-range__native-input"
                :value="to"
                type="datetime-local"
                step="1"
                :aria-label="toLabel"
                :aria-describedby="describedBy"
                :aria-invalid="invalid || undefined"
                @input="updateLocalInput('to', ($event.target as HTMLInputElement).value)"
              />
            </span>
            <AppButton
              v-if="applyLabel"
              class="app-date-range__apply"
              size="compact"
              :disabled="applyDisabled"
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
  max-width: 332px;
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
  width: min(420px, var(--reka-popover-content-available-width));
  padding: 14px;
}

.app-date-range-popover--with-apply {
  width: min(480px, var(--reka-popover-content-available-width));
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

.app-date-range__native-input {
  position: static !important;
  width: 100% !important;
  height: 100% !important;
  min-height: 0 !important;
  border: 0 !important;
  border-radius: inherit !important;
  background: transparent !important;
  color: var(--color-text) !important;
  cursor: pointer;
  font-family: var(--font-mono) !important;
  font-size: var(--text-meta) !important;
  font-variant-numeric: tabular-nums;
  opacity: 1;
  padding: 0 10px !important;
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

  .app-date-range__fields {
    grid-template-columns: minmax(0, 1fr);
  }

  .app-date-range__fields--with-apply .app-date-range__end-field {
    grid-column: 1;
    grid-template-columns: minmax(0, 1fr) auto;
    column-gap: 10px;
  }
}
</style>
