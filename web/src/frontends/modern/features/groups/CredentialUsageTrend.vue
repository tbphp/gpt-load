<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { getUsage } from '@modern/api/usage'
import { resolveTimeRange } from '@modern/app/time-range'
import { AppButton, AppCollectionState, AppSparkline } from '@modern/components/ui'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import { chartPoints, formatUsageCost, metricValue } from '@modern/features/usage/usage-display'
import { useApiClient } from '@shared/http/client-context'

const props = defineProps<{ group: number; credential: number }>()
const { t, n, locale } = useI18n()
const client = useApiClient()
const metrics = computed(() =>
  (['tokens', 'requests', 'cost'] as const).map((value) => ({
    value,
    label: t('usage.' + value),
    tone: value === 'tokens' ? 'info' : value === 'cost' ? 'cost' : 'accent',
  })),
)
const query = useQuery(
  computed(() => ({
    queryKey: ['modern', 'credential-usage', props.group, props.credential],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getUsage(
        client,
        {
          ...resolveTimeRange({ preset: '7d' }),
          group_id: String(props.group),
          credential_id: String(props.credential),
        },
        signal,
      ),
  })),
)
useLoadingActivity(query.isFetching)
const report = computed(() => query.data.value)
const points = computed(() => {
  if (!report.value) return []
  const rows = new Map(report.value.series.map((row) => [row.bucket_start_ms, row]))
  return chartPoints(report.value, 'tokens').map((point) => {
    const row = rows.get(point.from)
    return {
      ...point,
      requests: row?.success_count ?? 0,
      cost: row ? metricValue(row, 'cost') : 0,
      row,
    }
  })
})
const values = computed(() => points.value.map((point) => point.value))
const overlays = computed(() => [
  { values: points.value.map((point) => point.requests), tone: 'accent' as const },
  { values: points.value.map((point) => point.cost), tone: 'cost' as const },
])
const pointLabels = computed(() => {
  const date = dateFormatter(locale.value, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  })
  return points.value.map((point) => {
    const lines = [
      `${date.format(point.from)} – ${date.format(point.to)}`,
      `${t('usage.tokens')} ${point.value === null ? '—' : n(point.value)}`,
      `${t('usage.requests')} · ${t('usage.trendSeries.success')} ${n(point.requests)}`,
      `${t('usage.cost')} ${point.cost === null ? '—' : formatUsageCost(point.row?.estimated_cost_nano_usd ?? '0', locale.value)}`,
    ]
    if (
      report.value?.collectionIncomplete ||
      point.row?.usage_missing_count ||
      point.row?.partial_count
    )
      lines.push(t('usage.incomplete'))
    if (point.row && (point.row.unpriced_request_count || point.row.pricing_partial_count))
      lines.push(
        t('usage.unpriced', {
          count: n(point.row.unpriced_request_count),
          partial: n(point.row.pricing_partial_count),
        }),
      )
    return lines.join('\n')
  })
})
</script>

<template>
  <section class="modern-credential-usage-trend">
    <header>
      <h3>{{ t('credentialCards.localUsage') }}</h3>
      <div class="modern-credential-usage-legend">
        <span v-for="metric in metrics" :key="metric.value" :data-tone="metric.tone">
          <i aria-hidden="true" />{{ metric.label }}
        </span>
      </div>
    </header>
    <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
    <AppCollectionState
      v-else-if="query.isError.value"
      :title="t('credentialCards.statisticsFailed')"
      error
    >
      <AppButton size="sm" @click="query.refetch()">{{ t('ui.retry') }}</AppButton>
    </AppCollectionState>
    <AppSparkline
      v-else-if="report"
      :values="values"
      :point-labels="pointLabels"
      tone="info"
      :overlays="overlays"
      :show-marker="false"
      :label="t('credentialCards.localUsage')"
    />
  </section>
</template>

<style scoped>
.modern-credential-usage-trend {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
  padding-bottom: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-credential-usage-trend header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
  flex-wrap: wrap;
}
.modern-credential-usage-trend h3 {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-credential-usage-legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-small);
}
.modern-credential-usage-legend span {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-1);
  color: var(--modern-accent);
}
.modern-credential-usage-legend [data-tone='info'] {
  color: var(--modern-chart-input);
}
.modern-credential-usage-legend [data-tone='cost'] {
  color: var(--modern-chart-output);
}
.modern-credential-usage-legend i {
  width: var(--modern-space-1-5);
  height: var(--modern-space-1-5);
  border-radius: var(--modern-radius-round);
  background: currentColor;
}
</style>
