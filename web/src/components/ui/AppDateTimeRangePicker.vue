<script setup lang="ts">
import { CalendarClock } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { currentTimeZone, timeRangeMilliseconds, timeRanges } from '@/lib/time'

import AppButton from './AppButton.vue'
import AppPopover from './AppPopover.vue'
import FormField from './FormField.vue'
import OverflowTooltip from './OverflowTooltip.vue'

const props = defineProps<{
  from: string
  to: string
  label: string
  fromLabel: string
  toLabel: string
  timezoneLabel: string
  fromError?: string
  toError?: string
}>()
const emit = defineEmits<{
  'update:from': [value: string]
  'update:to': [value: string]
}>()
const { t } = useI18n()
const open = ref(false)
const timezone = currentTimeZone()
const shortcuts = timeRanges.map((key) => ({ key, milliseconds: timeRangeMilliseconds[key] }))

const display = computed(() => `${displayValue(props.from)} → ${displayValue(props.to)}`)

function displayValue(value: string): string {
  const normalized = normalizeLocalInputValue(value)
  return normalized ? normalized.replace('T', ' ') : '—'
}

function normalizeLocalInputValue(value: string): string {
  if (!value) return ''
  return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/u.test(value) ? `${value}:00` : value
}

function updateLocalInput(field: 'from' | 'to', value: string): void {
  const normalized = normalizeLocalInputValue(value)
  if (field === 'from') {
    emit('update:from', normalized)
  } else {
    emit('update:to', normalized)
  }
}

function localInputValue(milliseconds: number): string {
  const date = new Date(milliseconds)
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(
    date.getHours(),
  )}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function selectShortcut(milliseconds: number): void {
  const now = Math.floor(Date.now() / 1000) * 1000
  emit('update:from', localInputValue(Math.max(0, now - milliseconds)))
  emit('update:to', localInputValue(now + 24 * 60 * 60 * 1000))
}
</script>

<template>
  <AppPopover v-model:open="open" align="start" content-class="app-date-range-popover">
    <template #trigger>
      <AppButton
        class="app-date-range__trigger"
        variant="secondary"
        size="compact"
        :aria-label="label"
      >
        <CalendarClock :size="14" aria-hidden="true" />
        <OverflowTooltip as="span" :content="display" :focusable="false">
          {{ display }}
        </OverflowTooltip>
      </AppButton>
    </template>

    <div class="app-date-range__shortcuts" :aria-label="t('monitor.logs.filters.quickRanges')">
      <AppButton
        v-for="shortcut in shortcuts"
        :key="shortcut.key"
        variant="ghost"
        size="compact"
        @click="selectShortcut(shortcut.milliseconds)"
      >
        {{ t(`monitor.logs.filters.quick.${shortcut.key}`) }}
      </AppButton>
    </div>
    <div class="app-date-range__fields">
      <FormField id="logs-range-from" :label="fromLabel" size="compact" :error="fromError">
        <template #default="{ describedBy, invalid }">
          <span class="app-date-range__input-shell">
            <input
              id="logs-range-from"
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
      <FormField id="logs-range-to" :label="toLabel" size="compact" :error="toError">
        <template #default="{ describedBy, invalid }">
          <span class="app-date-range__input-shell">
            <input
              id="logs-range-to"
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
        </template>
      </FormField>
    </div>
    <p class="app-date-range__timezone">{{ timezoneLabel }} · {{ timezone }}</p>
  </AppPopover>
</template>

<style>
.app-date-range__trigger {
  max-width: 332px;
}

.app-date-range__trigger > span {
  overflow: hidden;
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.app-date-range-popover {
  width: min(420px, var(--reka-popover-content-available-width));
  padding: 14px;
}

.app-date-range__shortcuts {
  display: flex;
  flex-wrap: nowrap;
  gap: 4px;
  overflow-x: auto;
  border-bottom: 1px solid var(--color-border-subtle);
  padding-bottom: 10px;
}

.app-date-range__shortcuts .app-button {
  flex: 1 0 auto;
  white-space: nowrap;
}

.app-date-range__fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  padding-top: 12px;
}

.app-date-range__timezone {
  margin: 10px 0 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.app-date-range__input-shell {
  position: relative;
  display: flex;
  width: 100%;
  min-width: 0;
  min-height: var(--control-xs);
  align-items: center;
  overflow: hidden;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
}

.app-date-range__input-value {
  min-width: 0;
  overflow: hidden;
  padding: 0 10px;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.app-date-range__native-input {
  position: static !important;
  width: 100% !important;
  height: var(--control-xs) !important;
  min-height: var(--control-xs) !important;
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
  .app-date-range__trigger {
    width: 100%;
    max-width: none;
    min-height: var(--touch-target);
  }

  .app-date-range__fields {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
