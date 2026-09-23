<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import { getRPM, rpmQueryKey, type RPMRange, type RPMScope } from '@modern/api/rpm'
import { AppSegmentedControl, AppSparkline } from '@modern/components/ui'
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
  <section v-if="hasHistory" class="modern-rpm" :aria-label="t('rpm.title')">
    <header class="modern-rpm-heading">
      <h3>{{ t('rpm.title') }}</h3>
      <AppSegmentedControl v-model="range" size="xxs" :label="t('rpm.range')" :options="options" />
    </header>
    <dl v-if="report?.points.length" class="modern-rpm-metrics">
      <div v-if="report.current">
        <dt>{{ t('rpm.current') }}</dt>
        <dd>{{ n(report.current.requests) }}</dd>
        <small v-if="scope.kind === 'access_key'">{{
          t('rpm.currentSplit', {
            passed: n(report.current.requests - report.current.rejected),
            rejected: n(report.current.rejected),
          })
        }}</small>
      </div>
      <div v-if="report.peak !== undefined">
        <dt>{{ t('rpm.peak') }}</dt>
        <dd>{{ n(report.peak) }}</dd>
      </div>
      <div v-if="scope.kind === 'access_key'">
        <dt>{{ t('rpm.totalRejected') }}</dt>
        <dd :class="{ 'modern-rpm-rejected': report.rejected > 0 }">{{ n(report.rejected) }}</dd>
      </div>
      <div v-if="limit !== undefined">
        <dt>{{ t('rpm.limit') }}</dt>
        <dd>{{ limit > 0 ? n(limit) : t('rpm.unlimited') }}</dd>
      </div>
    </dl>
    <AppSparkline
      v-if="report?.points.length"
      :label="t('rpm.title')"
      :values="points.map((point) => point.value)"
      :point-labels="points.map((point) => point.label)"
      :ranges="points.map((point) => point.range)"
      :reference-value="limit && limit > 0 ? limit : undefined"
      show-isolated-points
      size="sm"
      tone="accent"
    />
    <p v-if="report?.points.length" class="modern-rpm-note">
      {{ t('rpm.windowHelp') }} · {{ t('rpm.updatedAt', { time: date.format(report.observedAt) }) }}
    </p>
  </section>
</template>

<style scoped>
.modern-rpm {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-3);
}
.modern-rpm-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
}
.modern-rpm-heading h3 {
  margin: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-rpm-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-3);
  margin: 0;
}
.modern-rpm-metrics dt,
.modern-rpm-metrics small,
.modern-rpm-note {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-rpm-metrics dd {
  margin: var(--modern-space-1) 0;
  font-size: var(--modern-font-size-secondary);
  font-variant-numeric: tabular-nums;
}
.modern-rpm-rejected {
  color: var(--modern-warning);
}
.modern-rpm-note {
  margin: 0;
}
</style>
