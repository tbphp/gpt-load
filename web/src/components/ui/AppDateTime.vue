<script setup lang="ts">
import { computed } from 'vue'

import { formatISOInstant, formatLocalInstant } from '@/lib/format'
import { currentTimeZone } from '@/lib/time'

const props = withDefaults(
  defineProps<{
    instant: number
    locale: string
    timeZone?: string
  }>(),
  {
    timeZone: currentTimeZone(),
  },
)

const dateTime = computed(() => formatISOInstant(props.instant))
const instantMs = computed(() => (dateTime.value === undefined ? null : props.instant))

const absolute = computed(() => {
  if (instantMs.value === null) return String(props.instant)
  return formatLocalInstant(instantMs.value, props.locale, { timeZone: props.timeZone })
})
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
