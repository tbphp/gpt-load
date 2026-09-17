<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getCredentialQuotaHistory } from '@modern/api/credential-quota-history'
import { getUsage, type UsageMetric } from '@modern/api/usage'
import { resolveTimeRange } from '@modern/app/time-range'
import { AppButton, AppSparkline } from '@modern/components/ui'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { chartPoints, formatUsageCost, metricValue } from '@modern/features/usage/usage-display'
import { useApiClient } from '@shared/http/client-context'
import CredentialQuotaTrend from './CredentialQuotaTrend.vue'

const props = defineProps<{ group: number; credential: number; subscription: boolean }>()
const { t, n, locale, te } = useI18n()
const client = useApiClient()
const cursor = ref<number>()
const query = useQuery(
  computed(() => ({
    queryKey: ['modern', 'credential-trends', props.group, props.credential, props.subscription],
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      // 两个接口使用同一时间范围，刷新时同时推进，任一失败不遮挡另一个图表。
      const range = resolveTimeRange({ preset: '7d' })
      const [usage, quota] = await Promise.allSettled([
        getUsage(
          client,
          { ...range, group_id: String(props.group), credential_id: String(props.credential) },
          signal,
        ),
        props.subscription
          ? getCredentialQuotaHistory(client, props.group, props.credential, range, signal)
          : Promise.resolve(undefined),
      ])
      signal.throwIfAborted()
      return {
        from: Number(range.from_ms),
        to: Number(range.to_ms),
        usage: usage.status === 'fulfilled' ? usage.value : undefined,
        quota: quota.status === 'fulfilled' ? quota.value : undefined,
        usageFailed: usage.status === 'rejected',
        quotaFailed: quota.status === 'rejected',
      }
    },
  })),
)
useLoadingActivity(query.isFetching)
const report = computed(() => query.data.value)
watch(report, () => {
  cursor.value = undefined
})
const quotaVisible = computed(
  () =>
    props.subscription &&
    (query.isPending.value ||
      query.isError.value ||
      report.value?.quotaFailed ||
      report.value?.quota?.windows.some((window) => window.points.length)),
)
const points = computed(() => {
  const usage = report.value?.usage
  if (!usage) return []
  const rows = new Map(usage.series.map((row) => [row.bucket_start_ms, row]))
  return chartPoints(usage, 'tokens').map((point) => {
    const row = rows.get(point.from)
    return {
      ...point,
      requests: row?.success_count ?? 0,
      cost: row ? metricValue(row, 'cost') : 0,
      row,
    }
  })
})
const charts = computed(() => [
  {
    key: 'tokens' as const,
    label: t('usage.tokens'),
    tone: 'info' as const,
    values: points.value.map((point) => point.value),
  },
  {
    key: 'requests' as const,
    label: t('usage.requests'),
    tone: 'accent' as const,
    values: points.value.map((point) => point.requests),
  },
  {
    key: 'cost' as const,
    label: t('usage.cost'),
    tone: 'cost' as const,
    values: points.value.map((point) => point.cost),
  },
])
const ranges = computed(() => {
  const data = report.value
  if (!data) return []
  return points.value.map((point) => ({
    from: (point.from - data.from) / (data.to - data.from),
    to: (point.to - data.from) / (data.to - data.from),
  }))
})
const date = computed(() =>
  dateFormatter(locale.value, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }),
)
function quotaTooltipAt(at: number): string {
  const data = report.value
  if (!data) return t('ui.date.ranges.7d')
  const lines = [date.value.format(at)]
  for (const window of data.quota?.windows ?? []) {
    if (!window.points.length) continue
    // 只用选中时刻之前的真实观测，不能拿后续重置后的值解释之前的额度。
    let low = 0,
      high = window.points.length
    while (low < high) {
      const middle = Math.floor((low + high) / 2)
      if (window.points[middle]!.observedAt <= at) low = middle + 1
      else high = middle
    }
    const point = window.points[low - 1]
    const key = 'credentialCards.quotaLabels.' + window.labelKey
    const label = window.labelKey && te(key) ? t(key) : window.label
    lines.push(
      `${label}  ${point ? n((10_000 - point.usedBasisPoints) / 100, { maximumFractionDigits: 2 }) + '%' : '—'}`,
    )
    if (point)
      lines.push(
        t('credentialCards.quotaObservedAt', { time: date.value.format(point.observedAt) }),
      )
  }
  return lines.join('\n')
}
function usageTooltipAt(at: number, metric: UsageMetric): string {
  const data = report.value
  if (!data) return t('ui.date.ranges.7d')
  const lines = [date.value.format(at)]
  const point = points.value.find((item) => item.from <= at && at < item.to)
  if (point) {
    lines.push(`${date.value.format(point.from)} – ${date.value.format(point.to)}`)
    if (metric === 'tokens')
      lines.push(`${t('usage.tokens')} ${point.value === null ? '—' : n(point.value)}`)
    else if (metric === 'requests')
      lines.push(`${t('usage.requests')} · ${t('usage.trendSeries.success')} ${n(point.requests)}`)
    else
      lines.push(
        `${t('usage.cost')} ${point.cost === null ? '—' : formatUsageCost(point.row?.estimated_cost_nano_usd ?? '0', locale.value)}`,
      )
    if (
      data.usage?.collectionIncomplete ||
      (metric === 'tokens' && (point.row?.usage_missing_count || point.row?.partial_count))
    )
      lines.push(t('usage.incomplete'))
    if (
      metric === 'cost' &&
      point.row &&
      (point.row.unpriced_request_count || point.row.pricing_partial_count)
    )
      lines.push(
        t('usage.unpriced', {
          count: n(point.row.unpriced_request_count),
          partial: n(point.row.pricing_partial_count),
        }),
      )
  } else if (data.usageFailed) lines.push(t('credentialCards.statisticsFailed'))
  return lines.join('\n')
}
const selectedAt = computed(() => {
  const data = report.value
  if (!data || cursor.value === undefined) return undefined
  return Math.min(data.to - 1, Math.round(data.from + cursor.value * (data.to - data.from)))
})
const quotaTooltip = computed(() =>
  selectedAt.value === undefined ? t('ui.date.ranges.7d') : quotaTooltipAt(selectedAt.value),
)
const usageTooltips = computed(() => ({
  tokens: selectedAt.value === undefined ? undefined : usageTooltipAt(selectedAt.value, 'tokens'),
  requests:
    selectedAt.value === undefined ? undefined : usageTooltipAt(selectedAt.value, 'requests'),
  cost: selectedAt.value === undefined ? undefined : usageTooltipAt(selectedAt.value, 'cost'),
}))
const pointLabels = computed(() => ({
  tokens: points.value.map((point) => usageTooltipAt((point.from + point.to) / 2, 'tokens')),
  requests: points.value.map((point) => usageTooltipAt((point.from + point.to) / 2, 'requests')),
  cost: points.value.map((point) => usageTooltipAt((point.from + point.to) / 2, 'cost')),
}))
</script>

<template>
  <section
    class="modern-credential-trends"
    :class="{ 'modern-credential-trends-three': !quotaVisible }"
    :aria-label="t('credentialCards.localUsage')"
  >
    <section v-if="quotaVisible" class="modern-credential-trends-cell" data-tone="accent">
      <h3>{{ t('credentialCards.quotaHistory') }}</h3>
      <div v-if="query.isPending.value" class="modern-credential-trends-state" role="status">
        {{ t('collection.loading') }}
      </div>
      <div
        v-else-if="query.isError.value || report?.quotaFailed"
        class="modern-credential-trends-state"
        role="alert"
      >
        <span>{{ t('credentialCards.quotaHistoryFailed') }}</span>
        <AppButton size="xs" variant="text" @click="query.refetch()">{{ t('ui.retry') }}</AppButton>
      </div>
      <CredentialQuotaTrend
        v-else-if="report?.quota"
        :report="report.quota"
        :cursor="cursor"
        :tooltip="quotaTooltip"
        @cursor-change="cursor = $event"
      />
    </section>
    <section
      v-for="chart in charts"
      :key="chart.key"
      class="modern-credential-trends-cell"
      :data-tone="chart.tone"
    >
      <h3>{{ chart.label }}</h3>
      <div v-if="query.isPending.value" class="modern-credential-trends-state" role="status">
        {{ t('collection.loading') }}
      </div>
      <div
        v-else-if="query.isError.value || report?.usageFailed"
        class="modern-credential-trends-state"
        role="alert"
      >
        <span>{{ t('credentialCards.statisticsFailed') }}</span>
        <AppButton size="xs" variant="text" @click="query.refetch()">{{ t('ui.retry') }}</AppButton>
      </div>
      <AppSparkline
        v-else-if="report?.usage"
        size="sm"
        :values="chart.values"
        :ranges="ranges"
        :point-labels="pointLabels[chart.key]"
        :tone="chart.tone"
        :show-marker="false"
        :label="chart.label"
        :cursor="cursor"
        :cursor-label="usageTooltips[chart.key]"
        :tooltip-side="quotaVisible && chart.key !== 'tokens' ? 'bottom' : 'top'"
        @cursor-change="cursor = $event"
      />
    </section>
  </section>
</template>

<style scoped>
.modern-credential-trends {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-2) var(--modern-space-4);
  padding: var(--modern-space-2);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
}
.modern-credential-trends-three {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
.modern-credential-trends-cell {
  display: grid;
  align-content: start;
  gap: var(--modern-space-1);
  min-width: 0;
}
.modern-credential-trends-cell h3 {
  margin: 0;
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.modern-credential-trends-cell[data-tone='info'] h3 {
  color: var(--modern-chart-input);
}
.modern-credential-trends-cell[data-tone='accent'] h3 {
  color: var(--modern-accent);
}
.modern-credential-trends-cell[data-tone='cost'] h3 {
  color: var(--modern-chart-cost);
}
.modern-credential-trends-state {
  display: grid;
  align-content: center;
  justify-items: start;
  gap: var(--modern-space-1);
  min-height: var(--modern-trend-sm-height);
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
</style>
