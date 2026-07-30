<script setup lang="ts">
import { CircleCheck, CircleHelp, RefreshCw, TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppDateTime from '@/components/ui/AppDateTime.vue'
import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'

import type { HomeHealthState } from './home-presenter'

const props = defineProps<{
  state: HomeHealthState
  observedDate: string
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
const title = computed(() => {
  const report = health.value
  if (props.state.kind === 'unknown' || props.state.kind === 'loading' || report === undefined) {
    return t('home.lede.unknownTitle')
  }
  const parameters = {
    groups: normalGroupCount.value,
    problems: problemGroupCount.value,
    available: report.counts.available,
    total: report.counts.total,
  }
  if (props.state.kind === 'normal') return t('home.lede.normal', parameters)
  if (props.state.kind === 'problem') return t('home.lede.problem', parameters)
  return problemGroupCount.value > 0
    ? t('home.lede.staleProblem', parameters)
    : t('home.lede.staleNormal', parameters)
})
</script>

<template>
  <section class="home-lede" :class="`home-lede--${tone}`" data-test="home-lede">
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
          {{ t('home.lede.currentStatus') }} ·
          <time :datetime="observedDate">{{ observedDate }}</time>
        </p>
        <h1>{{ title }}</h1>
        <p v-if="state.kind === 'unknown'" class="home-lede__description">
          {{ t('home.lede.unknownDescription') }}
        </p>
        <p v-else-if="state.kind === 'stale'" class="home-lede__description">
          {{ t('home.lede.staleDescription') }}
        </p>
      </div>

      <aside v-if="health" class="home-lede__stamp" :aria-label="t('home.lede.observation')">
        <span>
          {{ t('home.lede.observedAt') }}
          <AppDateTime :instant="health.observed_at" :locale="locale" />
        </span>
        <span>{{ t('home.lede.revision', { revision: health.snapshot_revision }) }}</span>
      </aside>
      <button
        v-else
        class="home-lede__retry"
        data-test="home-health-retry"
        type="button"
        @click="emit('retry')"
      >
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
  padding: var(--space-5) 0 var(--space-6);
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
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  letter-spacing: 0.05em;
}
.home-lede--normal .home-lede__eyebrow svg {
  color: var(--color-success);
}
.home-lede--warning .home-lede__eyebrow svg {
  color: var(--color-warning);
}
.home-lede--neutral .home-lede__eyebrow svg {
  color: var(--color-neutral);
}
.home-lede h1 {
  max-width: 34ch;
  font-family: var(--font-serif);
  font-size: clamp(2rem, 4vw, 3.35rem);
  font-weight: 500;
  letter-spacing: -0.035em;
  line-height: 1.14;
}
.home-lede--warning h1 {
  text-decoration-color: var(--color-warning);
  text-decoration-line: underline;
  text-decoration-thickness: 0.08em;
  text-underline-offset: 0.12em;
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
  font-size: var(--text-sm);
  white-space: nowrap;
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
