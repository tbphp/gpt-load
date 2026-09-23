<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import { getRPM, rpmQueryKey, type RPMRange, type RPMScope } from '@modern/api/rpm'
import { AppButton, AppFormSection, AppSegmentedControl, AppSparkline } from '@modern/components/ui'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import { useLoadingActivity } from '@modern/components/ui/loading'

const props = defineProps<{ scope: RPMScope; limit?: number }>()
const { t, n, locale } = useI18n()
const client = useApiClient()
const range = ref<RPMRange>('24h')
const options = computed(() =>
  (['1h', '6h', '24h', '7d'] as const).map((value) => ({
    value,
    label: t('rpm.ranges.' + value),
  })),
)
const query = useQuery(
  computed(() => ({
    queryKey: rpmQueryKey(props.scope, range.value),
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getRPM(client, props.scope, range.value, signal),
  })),
)
useLoadingActivity(query.isFetching)
const report = computed(() => query.data.value)
const hasTrend = computed(
  () => (report.value?.points.filter((point) => point.requests > 0).length ?? 0) > 1,
)
const hasHistory = ref(false)
watch(
  () => rpmQueryKey(props.scope, '24h').join(':'),
  () => {
    hasHistory.value = false
    range.value = '24h'
  },
)
watch(
  report,
  (value) => {
    if (value?.points.length) hasHistory.value = true
  },
  { immediate: true },
)
const date = computed(() =>
  dateFormatter(locale.value, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }),
)
const points = computed(() => {
  const data = report.value
  if (!data) return []
  const rows = new Map(data.points.map((point) => [point.at, point]))
  const start = Math.floor(data.from / data.bucketWidth) * data.bucketWidth
  return Array.from({ length: Math.ceil((data.to - start) / data.bucketWidth) }, (_, index) => {
    const at = start + index * data.bucketWidth
    const from = Math.max(data.from, at)
    const to = Math.min(data.to, at + data.bucketWidth)
    const row = rows.get(at)
    const label = [`${date.value.format(from)} – ${date.value.format(to)}`]
    if (row) {
      label.push(`${t('rpm.pointPeak')} ${n(row.peak)}`)
      label.push(
        `${t(props.scope.kind === 'access_key' ? 'rpm.arrivals' : 'rpm.attempts')} ${n(row.requests)}`,
      )
      if (props.scope.kind === 'access_key') {
        label.push(`${t('rpm.passed')} ${n(row.requests - row.rejected)}`)
        label.push(`${t('rpm.rejected')} ${n(row.rejected)}`)
      }
      if (props.limit !== undefined) {
        label.push(`${t('rpm.limit')} ${props.limit > 0 ? n(props.limit) : t('rpm.unlimited')}`)
      }
    }
    return {
      value: row?.peak ?? null,
      label: label.join('\n'),
      range: {
        from: (from - data.from) / (data.to - data.from),
        to: (to - data.from) / (data.to - data.from),
      },
    }
  })
})
</script>

<template>
  <AppFormSection v-if="hasHistory || query.isError.value" :title="t('rpm.title')" compact>
    <template #actions>
      <AppSegmentedControl v-model="range" size="xxs" :label="t('rpm.range')" :options="options" />
    </template>
    <div v-if="query.isError.value" class="modern-rpm-state" role="alert">
      <span>{{ t('rpm.loadFailed') }}</span>
      <AppButton variant="text" size="xs" @click="query.refetch()">{{ t('ui.retry') }}</AppButton>
    </div>
    <dl v-else-if="report?.points.length" class="modern-rpm-summary">
      <div v-if="report.current" class="modern-rpm-stat modern-rpm-stat-current">
        <dt>{{ t('rpm.current') }}</dt>
        <dd>{{ n(report.current.requests) }}</dd>
      </div>
      <div v-if="report.peak !== undefined">
        <dt>{{ t('rpm.peak') }}</dt>
        <dd>{{ n(report.peak) }}</dd>
      </div>
      <div v-if="scope.kind === 'access_key' && limit !== undefined">
        <dt>{{ t('rpm.limit') }}</dt>
        <dd>{{ limit > 0 ? n(limit) : t('rpm.unlimited') }}</dd>
      </div>
      <div v-if="scope.kind === 'access_key' && report.rejected > 0" class="modern-rpm-rejected">
        <dt>{{ t('rpm.totalRejected') }}</dt>
        <dd>{{ n(report.rejected) }}</dd>
      </div>
    </dl>
    <AppSparkline
      v-if="hasTrend"
      class="modern-rpm-chart"
      :label="t('rpm.title')"
      :values="points.map((point) => point.value)"
      :point-labels="points.map((point) => point.label)"
      :ranges="points.map((point) => point.range)"
      :reference-value="limit && limit > 0 ? limit : undefined"
      :show-marker="false"
      show-isolated-points
      size="sm"
      tone="accent"
    />
  </AppFormSection>
</template>

<style scoped>
.modern-rpm-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--modern-space-2) var(--modern-space-4);
  margin: 0;
}
.modern-rpm-summary > div {
  display: inline-flex;
  align-items: baseline;
  gap: var(--modern-space-1);
  white-space: nowrap;
}
.modern-rpm-summary dt,
.modern-rpm-state {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-rpm-summary dd {
  margin: 0;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-rpm-stat-current dd {
  color: var(--modern-text);
  font-size: var(--modern-font-size-section);
}
.modern-rpm-rejected dd {
  color: var(--modern-warning);
}
.modern-rpm-chart {
  margin-top: var(--modern-space-1);
}
.modern-rpm-state {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
</style>
