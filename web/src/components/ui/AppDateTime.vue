<script setup lang="ts">
import { computed } from 'vue'

import { resolveLocalTimeZone } from './date-time'

const props = withDefaults(
  defineProps<{
    instant: string
    locale: string
    timeZone?: string
    relativeTo?: Date
  }>(),
  {
    timeZone: resolveLocalTimeZone(),
    relativeTo: undefined,
  },
)

const parsed = computed(() => {
  const value = new Date(props.instant)
  return Number.isNaN(value.getTime()) ? null : value
})

const absolute = computed(() => {
  if (!parsed.value) return props.instant
  try {
    return new Intl.DateTimeFormat(props.locale, {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      timeZone: props.timeZone,
      timeZoneName: 'short',
      hourCycle: 'h23',
    }).format(parsed.value)
  } catch {
    return props.instant
  }
})

const relative = computed(() => {
  if (!parsed.value || !props.relativeTo) return ''
  const seconds = Math.round((parsed.value.getTime() - props.relativeTo.getTime()) / 1000)
  const absoluteSeconds = Math.abs(seconds)
  let value = seconds
  let unit: Intl.RelativeTimeFormatUnit = 'second'
  if (absoluteSeconds >= 86_400) {
    value = Math.round(seconds / 86_400)
    unit = 'day'
  } else if (absoluteSeconds >= 3_600) {
    value = Math.round(seconds / 3_600)
    unit = 'hour'
  } else if (absoluteSeconds >= 60) {
    value = Math.round(seconds / 60)
    unit = 'minute'
  }
  return new Intl.RelativeTimeFormat(props.locale, { numeric: 'always' }).format(value, unit)
})
</script>

<template>
  <span v-if="parsed" class="app-date-time">
    <time :datetime="instant">{{ absolute }}</time>
    <span v-if="relative" class="app-date-time__relative">({{ relative }})</span>
  </span>
  <span v-else class="app-date-time app-date-time--invalid">{{ instant }}</span>
</template>

<style scoped>
.app-date-time {
  display: inline-flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.app-date-time__relative {
  color: var(--color-text-muted);
}

.app-date-time--invalid {
  color: var(--color-danger);
  overflow-wrap: anywhere;
}
</style>
