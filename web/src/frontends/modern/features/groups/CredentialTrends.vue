<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getCredentialQuotaHistory,
  type QuotaHistoryWindow,
} from '@modern/api/credential-quota-history'
import type { CredentialQuota } from '@modern/api/credential-observation'
import { getUsage, type UsageMetric } from '@modern/api/usage'
import { resolveTimeRange } from '@modern/app/time-range'
import { AppButton, AppSegmentedControl, AppSparkline } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { chartPoints, formatUsageCost, metricValue } from '@modern/features/usage/usage-display'
import { useApiClient } from '@shared/http/client-context'
import CredentialQuotaTrend from './CredentialQuotaTrend.vue'
import { quotaWindowTitle, sortedQuotaWindows } from './credential-presentation'

type TrendRange = '6h' | '24h' | '7d'

const props = defineProps<{
  group: number
  credential: number
  subscription: boolean
  quotaWindows: readonly CredentialQuota[]
}>()
const { t, n, locale, te } = useI18n()
const client = useApiClient()
const cursor = ref<number>()
const range = ref<TrendRange>('24h')
const rangeOptions = computed(() =>
  (['6h', '24h', '7d'] as const).map((value) => ({
    value,
    label: t('ui.date.ranges.' + value),
  })),
)
const rangeLabel = computed(() => t('ui.date.ranges.' + range.value))
const query = useQuery(
  computed(() => ({
    queryKey: [
      'modern',
      'credential-trends',
      props.group,
      props.credential,
      props.subscription,
      range.value,
    ],
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      // 两个接口使用同一时间范围，刷新时同时推进，任一失败不遮挡另一个图表。
      const timeRange = resolveTimeRange({ preset: range.value })
      const [usage, quota] = await Promise.allSettled([
        getUsage(
          client,
          { ...timeRange, group_id: String(props.group), credential_id: String(props.credential) },
          signal,
        ),
        props.subscription
          ? getCredentialQuotaHistory(client, props.group, props.credential, timeRange, signal)
          : Promise.resolve(undefined),
      ])
      signal.throwIfAborted()
      return {
        from: Number(timeRange.from_ms),
        to: Number(timeRange.to_ms),
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
function quotaWindowKey(window: Pick<CredentialQuota, 'id' | 'scope' | 'windowSeconds'>): string {
  return `${window.id}\u0000${window.scope}\u0000${window.windowSeconds ?? ''}`
}
function orderedQuotaWindows(windows: readonly QuotaHistoryWindow[]): QuotaHistoryWindow[] {
  const positions = new Map<string, number>()
  sortedQuotaWindows(props.quotaWindows).forEach((window, index) => {
    const key = quotaWindowKey(window)
    if (!positions.has(key)) positions.set(key, index)
  })
  return [...windows].sort(
    (left, right) =>
      (positions.get(quotaWindowKey(left)) ?? Number.MAX_SAFE_INTEGER) -
      (positions.get(quotaWindowKey(right)) ?? Number.MAX_SAFE_INTEGER),
  )
}
const quotaHistory = computed(() => {
  const quota = report.value?.quota
  return quota ? { ...quota, windows: orderedQuotaWindows(quota.windows) } : undefined
})
const quotaVisible = computed(
  () =>
    props.subscription &&
    (query.isPending.value ||
      query.isError.value ||
      report.value?.quotaFailed ||
      quotaHistory.value?.windows.some((window) => window.points.length)),
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
  if (!data) return rangeLabel.value
  const windows = quotaHistory.value?.windows.filter((window) => window.points.length) ?? []
  const lines: string[] = []
  for (const window of windows) {
    // 首个真实观测前以其作为图表范围内的已知初值；之后不使用后续重置后的值解释此前额度。
    const point = quotaPointAt(window, at)
    const key = 'credentialCards.quotaLabels.' + window.labelKey
    const label = quotaWindowTitle(window, window.labelKey && te(key) ? t(key) : window.label)
    lines.push(date.value.format(point.observedAt))
    lines.push(
      `${label}  ${n((10_000 - point.usedBasisPoints) / 100, { maximumFractionDigits: 2 })}%`,
    )
  }
  return lines.join('\n')
}
function quotaPointAt(window: QuotaHistoryWindow, at: number) {
  let low = 0,
    high = window.points.length
  while (low < high) {
    const middle = Math.floor((low + high) / 2)
    if (window.points[middle]!.observedAt <= at) low = middle + 1
    else high = middle
  }
  return window.points[Math.max(0, low - 1)]!
}
function usageTooltipAt(at: number, metric: UsageMetric): string {
  const data = report.value
  if (!data) return rangeLabel.value
  const lines: string[] = []
  const point = points.value.find((item) => item.from <= at && at < item.to)
  if (point) {
    lines.push(`${date.value.format(point.from)} – ${date.value.format(point.to)}`)
    if (metric === 'tokens')
      lines.push(
        `${t('usage.tokens')} ${point.value === null ? '—' : formatCompactNumber(point.value, locale.value)}`,
      )
    else if (metric === 'requests')
      lines.push(
        `${t('usage.requests')} · ${t('usage.trendSeries.success')} ${formatCompactNumber(point.requests, locale.value)}`,
      )
    else
      lines.push(
        `${t('usage.cost')} ${point.cost === null ? '—' : formatUsageCost(point.row?.estimated_cost_nano_usd ?? '0', locale.value)}`,
      )
    if (
      metric !== 'tokens' &&
      (data.usage?.collectionIncomplete ||
        point.row?.usage_missing_count ||
        point.row?.partial_count)
    )
      lines.push(t('usage.incomplete'))
    if (
      metric === 'cost' &&
      point.row &&
      (point.row.unpriced_request_count || point.row.pricing_partial_count)
    )
      lines.push(
        t('usage.unpriced', {
          count: formatCompactNumber(point.row.unpriced_request_count, locale.value),
          partial: formatCompactNumber(point.row.pricing_partial_count, locale.value),
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
  selectedAt.value === undefined ? rangeLabel.value : quotaTooltipAt(selectedAt.value),
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
  <section class="modern-credential-trends-section" :aria-label="t('credentialCards.trends')">
    <header class="modern-credential-trends-heading">
      <h3>{{ t('credentialCards.trends') }}</h3>
      <AppSegmentedControl
        v-model="range"
        size="xxs"
        :label="t('credentialCards.trendRange')"
        :options="rangeOptions"
      />
    </header>
    <div
      class="modern-credential-trends"
      :class="{ 'modern-credential-trends-three': !quotaVisible }"
    >
      <section v-if="quotaVisible" class="modern-credential-trends-cell" data-tone="accent">
        <h4>{{ t('credentialCards.quotaHistory') }}</h4>
        <div v-if="query.isPending.value" class="modern-credential-trends-state" role="status">
          {{ t('collection.loading') }}
        </div>
        <div
          v-else-if="query.isError.value || report?.quotaFailed"
          class="modern-credential-trends-state"
          role="alert"
        >
          <span>{{ t('credentialCards.quotaHistoryFailed') }}</span>
          <AppButton size="xs" variant="text" @click="query.refetch()">{{
            t('ui.retry')
          }}</AppButton>
        </div>
        <CredentialQuotaTrend
          v-else-if="quotaHistory"
          :report="quotaHistory"
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
        <h4>{{ chart.label }}</h4>
        <div v-if="query.isPending.value" class="modern-credential-trends-state" role="status">
          {{ t('collection.loading') }}
        </div>
        <div
          v-else-if="query.isError.value || report?.usageFailed"
          class="modern-credential-trends-state"
          role="alert"
        >
          <span>{{ t('credentialCards.statisticsFailed') }}</span>
          <AppButton size="xs" variant="text" @click="query.refetch()">{{
            t('ui.retry')
          }}</AppButton>
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
    </div>
  </section>
</template>

<style scoped>
.modern-credential-trends-section {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-credential-trends-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
}
.modern-credential-trends-heading h3 {
  margin: 0;
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
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
.modern-credential-trends-cell h4 {
  margin: 0;
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.modern-credential-trends-cell[data-tone='info'] h4 {
  color: var(--modern-chart-input);
}
.modern-credential-trends-cell[data-tone='accent'] h4 {
  color: var(--modern-accent);
}
.modern-credential-trends-cell[data-tone='cost'] h4 {
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
