<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { formatISOInstant, formatLocalInstant, formatRelativeInstant } from '@/lib/format'
import { currentTimeZone } from '@/lib/time'

import AppTooltip from './AppTooltip.vue'

const props = withDefaults(
  defineProps<{
    instant: number | null
    locale: string
    emptyLabel: string
    timeZone?: string
    /**
     * hint：用统一的 AppTooltip 展示精确时间点，并加虚线下划线与 help 光标提示可悬停。
     * 关闭时退回原生 title，供不需要额外视觉噪音的密集表格使用。
     */
    hint?: boolean
    /** 自定义 hint 内容；未提供时仍展示该时间点的完整本地时间。 */
    tooltipContent?: string
    tooltipSide?: 'top' | 'right' | 'bottom' | 'left'
  }>(),
  {
    timeZone: currentTimeZone(),
    hint: false,
    tooltipContent: undefined,
    tooltipSide: 'top',
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
    : formatRelativeInstant(props.instant, now.value, props.locale, props.timeZone),
)
const resolvedTooltipContent = computed(() => props.tooltipContent ?? absolute.value)
let timer: number | undefined

watch(
  () => [props.instant, props.timeZone] as const,
  () => {
    now.value = Date.now()
  },
)

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
  <AppTooltip
    v-if="instant !== null && dateTime && hint"
    :content="resolvedTooltipContent"
    :side="tooltipSide"
  >
    <time class="app-relative-time app-relative-time--hint" :datetime="dateTime" tabindex="0">
      {{ relative }}
    </time>
  </AppTooltip>
  <time
    v-else-if="instant !== null && dateTime"
    class="app-relative-time"
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

.app-relative-time--hint:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
  border-radius: 3px;
}

.app-relative-time--empty {
  color: var(--color-text-faint);
  font-family: var(--font-sans);
}
</style>
