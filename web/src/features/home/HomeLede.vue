<script setup lang="ts">
import { CircleCheck, CircleHelp, RefreshCw, TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'

import type { HomeHealthState } from './home-presenter'

const props = defineProps<{
  state: HomeHealthState
}>()
const emit = defineEmits<{ retry: [] }>()
const { locale, t } = useI18n()

const health = computed(() => {
  if (
    props.state.kind === 'normal' ||
    props.state.kind === 'problem' ||
    props.state.kind === 'stale'
  ) {
    return props.state.health
  }
  return undefined
})
const problemGroupCount = computed(() => {
  if (props.state.kind === 'problem') return props.state.groups.length
  if (props.state.kind !== 'stale') return 0
  return new Set([
    ...props.state.health.cooldown_keys.map((key) => key.group_id),
    ...props.state.health.blacklisted_keys.map((key) => key.group_id),
  ]).size
})
const normalGroupCount = computed(() =>
  Math.max(0, (health.value?.groups.length ?? 0) - problemGroupCount.value),
)
const tone = computed<'normal' | 'warning' | 'neutral'>(() => {
  if (props.state.kind === 'normal') return 'normal'
  if (props.state.kind === 'problem') return 'warning'
  return 'neutral'
})
const titleParameters = computed(() => ({
  groups: normalGroupCount.value,
  problems: problemGroupCount.value,
  available: health.value?.counts.available ?? 0,
  total: health.value?.counts.total ?? 0,
}))
const problemTitle = computed(() => {
  if (props.state.kind !== 'problem') return undefined
  return {
    normal: t('home.lede.problemNormal', titleParameters.value),
    emphasis: t('home.lede.problemEmphasis', titleParameters.value),
    availability: t('home.lede.problemAvailability', titleParameters.value),
  }
})
const title = computed(() => {
  const report = health.value
  if (props.state.kind === 'unknown' || props.state.kind === 'loading' || report === undefined) {
    return t('home.lede.unknownTitle')
  }
  const parameters = titleParameters.value
  if (props.state.kind === 'normal') return t('home.lede.normal', parameters)
  if (props.state.kind === 'problem') return ''
  return problemGroupCount.value > 0
    ? t('home.lede.staleProblem', parameters)
    : t('home.lede.staleNormal', parameters)
})
const observedTime = computed(() => {
  const report = health.value
  if (report === undefined) return ''
  return new Intl.DateTimeFormat(locale.value, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  }).format(new Date(report.observed_at))
})
const uptime = computed(() => {
  const seconds = health.value?.uptime_seconds
  if (seconds === undefined) return ''
  const days = Math.floor(seconds / 86_400)
  const hours = Math.floor((seconds % 86_400) / 3_600)
  if (days > 0) return `${days}d${hours}h`
  if (hours > 0) return `${hours}h`
  return `${Math.floor(seconds / 60)}m`
})
</script>

<template>
  <section class="home-lede" :class="`home-lede--${tone}`">
    <template v-if="state.kind === 'loading'">
      <span class="sr-only" role="status">{{ t('home.healthLoading') }}</span>
      <div class="home-lede__loading">
        <SkeletonBlock height="1rem" />
        <SkeletonBlock height="3.5rem" />
        <SkeletonBlock height="1rem" />
      </div>
    </template>
    <template v-else>
      <div class="home-lede__content">
        <p class="home-lede__eyebrow">
          <CircleCheck v-if="state.kind === 'normal'" :size="18" aria-hidden="true" />
          <TriangleAlert v-else-if="state.kind === 'problem'" :size="18" aria-hidden="true" />
          <CircleHelp v-else :size="18" aria-hidden="true" />
          {{ t('home.lede.currentStatus') }}
        </p>
        <h1 v-if="problemTitle">
          {{ problemTitle.normal
          }}<span class="home-lede__problem-emphasis">{{ problemTitle.emphasis }}</span
          >{{ problemTitle.availability }}
        </h1>
        <h1 v-else>{{ title }}</h1>
        <p v-if="state.kind === 'unknown'" class="home-lede__description">
          {{ t('home.lede.unknownDescription') }}
        </p>
        <p v-else-if="state.kind === 'stale'" class="home-lede__description">
          {{ t('home.lede.staleDescription') }}
        </p>
      </div>

      <aside v-if="health" class="home-lede__stamp" :aria-label="t('home.lede.observation')">
        <span>
          <span>{{ t('home.lede.updatedAt') }}</span>
          <time :datetime="health.observed_at">
            {{ observedTime }}
          </time>
        </span>
        <span>
          <span>{{ t('home.lede.version') }}</span>
          <b>{{ health.version }}</b>
        </span>
        <span>
          <span>{{ t('home.lede.uptime') }}</span>
          <b>{{ uptime }}</b>
        </span>
      </aside>
      <button v-else class="home-lede__retry" type="button" @click="emit('retry')">
        <RefreshCw :size="16" aria-hidden="true" />
        {{ t('common.retry') }}
      </button>
    </template>
  </section>
</template>

<style scoped>
.home-lede {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
  gap: var(--space-6);
  border-bottom: 1px solid var(--color-border-strong);
  padding: var(--space-2) 0 var(--space-7);
}
.home-lede__content {
  min-width: 0;
}
.home-lede__eyebrow {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0 0 var(--space-3);
  color: var(--color-text-faint);
  font-size: var(--text-md);
  letter-spacing: 0;
}
.home-lede__eyebrow svg {
  border-radius: 50%;
}
.home-lede--normal .home-lede__eyebrow svg {
  background: var(--color-success-bg);
  color: var(--color-success);
  box-shadow: 0 0 0 3px var(--color-success-bg);
}
.home-lede--warning .home-lede__eyebrow svg {
  background: var(--color-warning-bg);
  color: var(--color-warning);
  box-shadow: 0 0 0 3px var(--color-warning-bg);
}
.home-lede--neutral .home-lede__eyebrow svg {
  background: var(--color-neutral-bg);
  color: var(--color-neutral);
  box-shadow: 0 0 0 3px var(--color-neutral-bg);
}
.home-lede h1 {
  max-width: 34ch;
  font-family: var(--font-sans);
  font-size: clamp(1.875rem, 2.35vw, 2.125rem);
  font-weight: 600;
  letter-spacing: -0.028em;
  line-height: 1.18;
}
.home-lede__problem-emphasis {
  color: var(--color-warning);
}
.home-lede--neutral h1 {
  color: var(--color-text-muted);
}
.home-lede__description {
  max-width: 68ch;
  margin: var(--space-3) 0 0;
  color: var(--color-text-muted);
}
.home-lede__stamp {
  display: grid;
  justify-items: end;
  gap: var(--space-1);
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-md);
  white-space: nowrap;
}
.home-lede__stamp > span {
  display: grid;
  grid-template-columns: auto minmax(86px, auto);
  gap: var(--space-2);
}
.home-lede__stamp > span > :last-child {
  color: var(--color-text-muted);
  font-weight: 400;
}
.home-lede__retry {
  display: inline-flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-2);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: var(--space-2) var(--space-4);
  cursor: pointer;
}
.home-lede__loading {
  display: grid;
  max-width: 720px;
  grid-column: 1 / -1;
  gap: var(--space-3);
}
@media (max-width: 759px) {
  .home-lede {
    grid-template-columns: minmax(0, 1fr);
  }
  .home-lede__stamp {
    justify-items: start;
  }
  .home-lede__retry {
    justify-self: start;
  }
}
</style>
