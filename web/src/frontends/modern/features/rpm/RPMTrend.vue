<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import { getRPM, rpmQueryKey, type RPMScope } from '@modern/api/rpm'
import { AppButton, AppFormSection, AppSparkline } from '@modern/components/ui'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import { useLoadingActivity } from '@modern/components/ui/loading'

const props = defineProps<{ scope: RPMScope; limit?: number }>()
const { t, n, locale } = useI18n()
const client = useApiClient()
const query = useQuery(
  computed(() => ({
    queryKey: rpmQueryKey(props.scope),
    queryFn: ({ signal }: { signal: AbortSignal }) => getRPM(client, props.scope, signal),
  })),
)
useLoadingActivity(query.isFetching)
const report = computed(() => query.data.value)
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
    label.push(`${t('rpm.pointPeak')} ${row ? n(row.peak) : '—'}`)
    if (row) {
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
  <AppFormSection :title="t('rpm.title')" compact>
    <div class="modern-rpm-layout">
      <div v-if="query.isError.value" class="modern-rpm-state" role="alert">
        <span>{{ t('rpm.loadFailed') }}</span>
        <AppButton variant="text" size="xs" @click="query.refetch()">{{ t('ui.retry') }}</AppButton>
      </div>
      <dl v-else class="modern-rpm-summary">
        <div class="modern-rpm-stat-current">
          <dt>{{ t('rpm.current') }}</dt>
          <dd>{{ report?.current ? n(report.current.requests) : '—' }}</dd>
        </div>
        <div>
          <dt>{{ t('rpm.peak') }}</dt>
          <dd>{{ report?.peak !== undefined ? n(report.peak) : '—' }}</dd>
        </div>
        <div v-if="scope.kind === 'access_key' && limit !== undefined">
          <dt>{{ t('rpm.limit') }}</dt>
          <dd>{{ limit > 0 ? n(limit) : t('rpm.unlimited') }}</dd>
        </div>
        <div v-if="scope.kind === 'access_key' && report?.rejected" class="modern-rpm-rejected">
          <dt>{{ t('rpm.totalRejected') }}</dt>
          <dd>{{ n(report.rejected) }}</dd>
        </div>
      </dl>
      <AppSparkline
        class="modern-rpm-chart"
        :label="t('rpm.trendHour')"
        :values="points.map((point) => point.value)"
        :point-labels="points.map((point) => point.label)"
        :ranges="points.map((point) => point.range)"
        :reference-value="report?.points.length && limit && limit > 0 ? limit : undefined"
        :show-marker="false"
        show-isolated-points
        size="sm"
        tone="accent"
      />
    </div>
  </AppFormSection>
</template>

<style scoped>
.modern-rpm-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 42%);
  align-items: center;
  gap: var(--modern-space-4);
  min-width: 0;
}
.modern-rpm-summary {
  display: grid;
  gap: var(--modern-space-1);
  margin: 0;
  min-width: 0;
}
.modern-rpm-summary > div {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--modern-space-1);
}
.modern-rpm-summary dt,
.modern-rpm-state {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-rpm-summary dd {
  flex: none;
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
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-rpm-state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
}
</style>
