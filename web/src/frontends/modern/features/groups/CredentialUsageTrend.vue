<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getUsage, type UsageMetric } from '@modern/api/usage'
import { resolveTimeRange } from '@modern/app/time-range'
import {
  AppButton,
  AppCollectionState,
  AppSegmentedControl,
  AppSparkline,
} from '@modern/components/ui'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import { chartPoints, formatUsageCost } from '@modern/features/usage/usage-display'
import { useApiClient } from '@shared/http/client-context'

const props = defineProps<{ group: number; credential: number }>()
const { t, n, locale } = useI18n()
const client = useApiClient()
const metric = ref<UsageMetric>('tokens')
const metrics = computed(() =>
  (['tokens', 'requests', 'cost'] as const).map((value) => ({
    value,
    label: t('usage.' + value),
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
  return chartPoints(report.value, metric.value).map((point) => ({
    ...point,
    value: metric.value === 'requests' ? (rows.get(point.from)?.success_count ?? 0) : point.value,
    row: rows.get(point.from),
  }))
})
const values = computed(() => points.value.map((point) => point.value))
const tone = computed(() =>
  metric.value === 'tokens' ? 'info' : metric.value === 'cost' ? 'cost' : 'accent',
)
const pointLabels = computed(() => {
  const date = dateFormatter(locale.value, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  })
  return points.value.map((point) => {
    const value =
      point.value === null
        ? '—'
        : metric.value === 'cost'
          ? formatUsageCost(point.row?.estimated_cost_nano_usd ?? '0', locale.value)
          : n(point.value)
    const lines = [
      `${date.format(point.from)} – ${date.format(point.to)}`,
      `${t(metric.value === 'requests' ? 'usage.trendSeries.success' : 'usage.' + metric.value)} ${value}`,
    ]
    if (
      report.value?.collectionIncomplete ||
      (metric.value === 'tokens' && (point.row?.usage_missing_count || point.row?.partial_count))
    )
      lines.push(t('usage.incomplete'))
    if (
      metric.value === 'cost' &&
      point.row &&
      (point.row.unpriced_request_count || point.row.pricing_partial_count)
    )
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
      <AppSegmentedControl
        v-model="metric"
        :label="t('credentialCards.statisticsMetric')"
        appearance="field"
        size="xs"
        :options="metrics"
      />
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
      :tone="tone"
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
}
.modern-credential-usage-trend h3 {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
</style>
