<script setup lang="ts">
import { computed } from 'vue'

import { formatLocalInstant } from '@/lib/format'
import { currentTimeZone } from '@/lib/time'

const props = withDefaults(
  defineProps<{
    instant: number | string
    locale: string
    timeZone?: string
  }>(),
  {
    timeZone: currentTimeZone(),
  },
)

const instantMs = computed(() => {
  if (typeof props.instant === 'number') {
    return Number.isSafeInteger(props.instant) ? props.instant : null
  }
  const value = Date.parse(props.instant)
  return Number.isNaN(value) ? null : value
})

const absolute = computed(() => {
  if (instantMs.value === null) return String(props.instant)
  return formatLocalInstant(instantMs.value, props.locale, { timeZone: props.timeZone })
})

const dateTime = computed(() =>
  instantMs.value === null ? undefined : new Date(instantMs.value).toISOString(),
)
</script>

<template>
  <time v-if="dateTime" class="app-date-time" :datetime="dateTime">{{ absolute }}</time>
  <span v-else class="app-date-time app-date-time--invalid">{{ absolute }}</span>
</template>

<style scoped>
.app-date-time--invalid {
  color: var(--color-danger);
  overflow-wrap: anywhere;
}
</style>
