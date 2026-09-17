<script setup lang="ts">
import { computed, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import AppCalendar from './AppCalendar.vue'
import AppTextField from './AppTextField.vue'
import { formatLocalDateTime, localDay, parseLocalDateTime } from './date-time'
defineProps<{
  label: string
  min?: string
  max?: string
  invalid?: boolean
  range?: { from: string; to: string }
}>()
const model = defineModel<string>({ required: true })
const { t } = useI18n()
const date = computed({
  get: () => model.value.split(/[T ]/)[0] ?? '',
  set: (value: string) => {
    model.value = /[T ]\d{2}:/.test(value)
      ? value.replace(' ', 'T')
      : `${value}T${time.value || '00:00:00'}`
  },
})
const time = computed({
  get: () => model.value.split(/[T ]/)[1] ?? '',
  set: (value: string) => {
    model.value = `${date.value || localDay(new Date())}T${value}`
  },
})
async function adjustTime(event: KeyboardEvent): Promise<void> {
  if (event.isComposing || !(event.target instanceof HTMLInputElement)) return
  const input = event.target
  const value = parseLocalDateTime(model.value)
  if (!value) return
  event.preventDefault()
  const cursor = input.selectionStart ?? 0
  const direction = event.key === 'ArrowUp' ? 1 : -1
  const start = cursor < 3 ? 0 : cursor < 6 ? 3 : 6
  if (start === 0) value.setHours(value.getHours() + direction)
  else if (start === 3) value.setMinutes(value.getMinutes() + direction)
  else value.setSeconds(value.getSeconds() + direction)
  model.value = formatLocalDateTime(value)
  await nextTick()
  input.setSelectionRange(start, start + 2)
}
</script>
<template>
  <div class="modern-date-editor">
    <AppCalendar v-model="date" :label="label" :min="min" :max="max" :range="range" />
    <div class="modern-date-editor-fields">
      <AppTextField
        v-model="date"
        :label="`${label} · ${t('ui.date.date')}`"
        label-hidden
        placeholder="YYYY-MM-DD"
        size="xs"
        :invalid="invalid"
        autocomplete="off"
      />
      <AppTextField
        v-model="time"
        :label="`${label} · ${t('ui.date.time')}`"
        label-hidden
        placeholder="HH:mm:ss"
        size="xs"
        :invalid="invalid"
        autocomplete="off"
        @keydown.up="adjustTime"
        @keydown.down="adjustTime"
      />
    </div>
  </div>
</template>
<style scoped>
.modern-date-editor {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-date-editor-fields {
  display: grid;
  grid-template-columns: minmax(0, 1.2fr) minmax(0, 1fr);
  gap: var(--modern-space-1);
  align-items: start;
}
</style>
