<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { formatISOInstant, formatLocalInstant, formatRelativeInstant } from '@/lib/format'
import { currentTimeZone } from '@/lib/time'

const props = withDefaults(
  defineProps<{
    instant: number | null
    locale: string
    emptyLabel: string
    timeZone?: string
    /**
     * hint：给相对时间加虚线下划线与 help 光标，提示可以悬停查看精确时间点。
     * 组件一直都带 title，但没有视觉线索时用户无法发现，因此按调用点显式开启。
     */
    hint?: boolean
  }>(),
  {
    timeZone: currentTimeZone(),
    hint: false,
  },
)

const now = ref(Date.now())
const dateTime = computed(() =>
  props.instant === null ? undefined : formatISOInstant(props.instant),
)
const absolute = computed(() =>
  props.instant === null
    ? props.emptyLabel
    : formatLocalInstant(props.instant, props.locale, { timeZone: props.timeZone }),
)
const relative = computed(() =>
  props.instant === null
    ? props.emptyLabel
    : formatRelativeInstant(props.instant, now.value, props.locale),
)
let timer: number | undefined

onMounted(() => {
  timer = window.setInterval(() => {
    now.value = Date.now()
  }, 30_000)
})

onBeforeUnmount(() => {
  if (timer !== undefined) window.clearInterval(timer)
})
</script>

<template>
  <time
    v-if="instant !== null && dateTime"
    class="app-relative-time"
    :class="{ 'app-relative-time--hint': hint }"
    :datetime="dateTime"
    :title="absolute"
  >
    {{ relative }}
  </time>
  <span v-else class="app-relative-time app-relative-time--empty">{{ relative }}</span>
</template>

<style scoped>
.app-relative-time {
  white-space: nowrap;
}

.app-relative-time--hint {
  cursor: help;
  text-decoration: underline dotted;
  text-decoration-color: var(--color-border-control);
  text-underline-offset: 3px;
}

.app-relative-time--empty {
  color: var(--color-text-faint);
  font-family: var(--font-sans);
}
</style>
