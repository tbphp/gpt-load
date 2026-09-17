<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { HomeStatistics } from '@modern/api/home'
import { AppButton, AppPanel, AppSparkline } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import HomeSectionLink from './HomeSectionLink.vue'

const props = defineProps<{ report?: HomeStatistics; failed: boolean }>()
defineEmits<{ retry: [] }>()
const { t, n, locale } = useI18n()
const compact = (value: number) => formatCompactNumber(value, locale.value)
const time = (value: number, date = false) =>
  dateFormatter(locale.value, {
    ...(date ? ({ month: 'short', day: 'numeric' } as const) : {}),
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(value)
const values = computed(() => props.report?.series.map((point) => point.requests) ?? [])
const pointLabels = computed(() =>
  props.report?.series.map((point) =>
    t('home.trendPoint', {
      time: time(point.startMs, true),
      requests: compact(point.requests),
      failures: compact(point.failures),
    }),
  ),
)
const rate = computed(() => {
  const report = props.report
  return report?.requests
    ? n((report.requests - report.failures) / report.requests, {
        style: 'percent',
        maximumFractionDigits: 1,
      })
    : '—'
})
const midpoint = computed(() => (props.report ? (props.report.fromMs + props.report.toMs) / 2 : 0))
function endpointLabel(value: number): string {
  const current = new Date(props.report?.observedAt ?? value)
  const date = new Date(value).toDateString()
  if (date === current.toDateString()) return t('home.todayAt', { time: time(value) })
  current.setDate(current.getDate() - 1)
  return date === current.toDateString()
    ? t('home.yesterdayAt', { time: time(value) })
    : time(value, true)
}
</script>

<template>
  <AppPanel :title="t('home.trend')" :description="t('home.trendWindow')" compact>
    <template #actions>
      <HomeSectionLink
        :to="{ name: 'modern-usage', query: { preset: '24h' } }"
        :label="t('pages.usage.title')"
      />
    </template>
    <div v-if="failed" class="modern-home-trend-state" role="status">
      <span>{{ t(report ? 'home.refreshFailed' : 'home.trendFailed') }}</span>
      <AppButton size="xs" variant="text" @click="$emit('retry')">{{ t('ui.retry') }}</AppButton>
    </div>
    <p v-if="!report && !failed" class="modern-home-trend-state">{{ t('ui.loading') }}</p>
    <div v-if="report" class="modern-home-trend">
      <dl class="modern-home-trend-facts">
        <div>
          <dt>{{ t('home.requests') }}</dt>
          <dd>{{ compact(report.requests) }}</dd>
        </div>
        <div>
          <dt>{{ t('credentialCards.successShort') }}</dt>
          <dd>{{ rate }}</dd>
        </div>
      </dl>
      <AppSparkline
        :label="t('home.trend')"
        :values="values"
        :point-labels="pointLabels"
        tone="info"
        class="modern-home-trend-chart"
      />
      <p class="modern-home-trend-axis">
        <span>{{ endpointLabel(report.fromMs) }}</span>
        <span>{{ time(midpoint) }}</span>
        <span>{{ endpointLabel(report.toMs) }}</span>
      </p>
    </div>
  </AppPanel>
</template>

<style scoped>
.modern-home-trend {
  display: grid;
  gap: var(--modern-space-2);
}
.modern-home-trend-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-trend-state + .modern-home-trend {
  margin-top: var(--modern-space-3);
}
.modern-home-trend-facts {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--modern-space-2) var(--modern-space-5);
  margin: 0;
}
.modern-home-trend-facts > div {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-1);
  min-width: 0;
}
.modern-home-trend-facts dt {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-trend-facts dd {
  order: -1;
  margin: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-home-trend-chart {
  height: 80px;
}
.modern-home-trend-axis {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
</style>
