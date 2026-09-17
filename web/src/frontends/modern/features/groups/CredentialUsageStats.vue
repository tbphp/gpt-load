<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getUsage, type UsageMetric } from '@modern/api/usage'
import {
  getCredentialQuotaHistory,
  type QuotaHistoryWindow,
} from '@modern/api/credential-quota-history'
import { resolveTimeRange } from '@modern/app/time-range'
import { useLoadingActivity } from '@modern/components/ui/loading'
import {
  AppButton,
  AppCollectionState,
  AppIconButton,
  AppOverflowText,
  AppSelect,
} from '@modern/components/ui'
import { formatCompactNumber, formatNanoUSD } from '@modern/components/ui/format'
import { type DateRangePreset } from '@modern/components/ui/date-time'
import { useApiClient } from '@shared/http/client-context'
import { percentage, successRate } from '@modern/features/usage/usage-display'
import UsageTrend from '@modern/features/usage/UsageTrend.vue'
import { credentialTime } from './credential-presentation'
import CredentialQuotaTrend from './CredentialQuotaTrend.vue'

const props = defineProps<{ group: number; credential: number; subscription: boolean }>()
const { t, n, locale, te } = useI18n()
const client = useApiClient()
const preset = ref<DateRangePreset>('24h')
const range = ref(resolveTimeRange({ preset: preset.value }))
const metric = ref<UsageMetric>('tokens')
const chosenWindow = ref('')
const cursor = ref<number>()
watch(preset, () => {
  range.value = resolveTimeRange({ preset: preset.value })
  cursor.value = undefined
})
const query = useQuery(
  computed(() => ({
    queryKey: ['modern', 'credential-usage', props.group, props.credential, preset.value],
    queryFn: ({ signal }: { signal: AbortSignal }) => {
      range.value = resolveTimeRange({ preset: preset.value })
      return getUsage(
        client,
        { ...range.value, group_id: String(props.group), credential_id: String(props.credential) },
        signal,
      )
    },
  })),
)
const quota = useQuery(
  computed(() => ({
    queryKey: ['modern', 'credential-quota-history', props.group, props.credential, range.value],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getCredentialQuotaHistory(client, props.group, props.credential, range.value, signal),
    enabled: props.subscription,
  })),
)
const report = computed(() => query.data.value)
const window = computed(
  () =>
    quota.data.value?.windows.find((value) => value.key === chosenWindow.value) ??
    quota.data.value?.windows[0],
)
const selectedWindow = computed({
  get: () => window.value?.key ?? '',
  set: (value: string) => {
    chosenWindow.value = value
    cursor.value = undefined
  },
})
const latest = computed(() => window.value?.points.at(-1))
const displayedPoint = computed(() => {
  if (cursor.value === undefined) return latest.value
  const point = [...(window.value?.points ?? [])]
    .reverse()
    .find((point) => point.observedAt <= cursor.value!)
  return point &&
    cursor.value - point.observedAt <= Math.max(300_000, (quota.data.value?.bucketWidth ?? 0) * 2)
    ? point
    : undefined
})
const windows = computed(
  () =>
    quota.data.value?.windows.map((window) => ({
      value: window.key,
      label: windowLabel(window),
    })) ?? [],
)
const presets = computed(() =>
  (['1h', '24h', '7d', '30d'] as const).map((value) => ({
    value,
    label: t('ui.date.ranges.' + value),
  })),
)
const metrics = computed(() =>
  (['tokens', 'requests', 'cost'] as const).map((value) => ({ value, label: t('usage.' + value) })),
)
const busy = computed(
  () => query.isFetching.value || (props.subscription && quota.isFetching.value),
)
useLoadingActivity(busy)
function windowLabel(window: QuotaHistoryWindow): string {
  const key = 'credentialCards.quotaLabels.' + window.labelKey
  return window.labelKey && te(key) ? t(key) : window.label
}
const cards = computed(() => {
  const row = report.value?.summary
  if (!row) return []
  return [
    {
      label: t('usage.requests'),
      value: formatCompactNumber(row.request_count, locale.value),
      full: n(row.request_count),
    },
    {
      label: t('usage.tokens'),
      value:
        !row.total_tokens && (row.usage_missing_count || row.partial_count)
          ? '—'
          : formatCompactNumber(row.total_tokens, locale.value),
      full: n(row.total_tokens),
    },
    {
      label: t('usage.cost'),
      value:
        row.estimated_cost_nano_usd === '0' &&
        (row.unpriced_request_count || row.pricing_partial_count)
          ? '—'
          : formatNanoUSD(row.estimated_cost_nano_usd, locale.value, 'narrowSymbol'),
      full: t('usage.costDetail'),
    },
    {
      label: t('usage.success'),
      value: percentage(successRate(row), locale.value),
      full: t('usage.requestDetail', {
        success: n(row.success_count),
        failure: n(row.failure_count),
      }),
    },
  ]
})
async function refresh(): Promise<void> {
  cursor.value = undefined
  const previous = range.value
  await query.refetch()
  // 时间改变会自然触发额度查询；时间未改变时主动刷新该查询。
  if (
    props.subscription &&
    previous.from_ms === range.value.from_ms &&
    previous.to_ms === range.value.to_ms
  )
    await quota.refetch()
}
</script>
<template>
  <section class="modern-credential-usage">
    <header class="modern-credential-usage-header">
      <h3>{{ t('credentialCards.usageStatistics') }}</h3>
      <div class="modern-credential-usage-controls">
        <AppSelect
          v-model="preset"
          :label="t('credentialCards.statisticsRange')"
          label-hidden
          size="sm"
          :options="presets"
        />
        <AppIconButton
          :icon="RefreshCw"
          :label="t('shell.refresh')"
          size="sm"
          :loading="busy"
          :disabled="busy"
          @click="refresh"
        />
      </div>
    </header>
    <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
    <AppCollectionState
      v-else-if="query.isError.value"
      :title="t('credentialCards.statisticsFailed')"
      error
    >
      <AppButton size="sm" @click="refresh">{{ t('ui.retry') }}</AppButton>
    </AppCollectionState>
    <template v-else-if="report">
      <dl class="modern-credential-usage-summary">
        <div v-for="card in cards" :key="card.label">
          <dt>{{ card.label }}</dt>
          <dd><AppOverflowText :text="card.value" :hint="card.full" /></dd>
        </div>
      </dl>
      <p
        v-if="
          report.collectionIncomplete ||
          report.summary.usage_missing_count ||
          report.summary.partial_count ||
          report.summary.unpriced_request_count ||
          report.summary.pricing_partial_count
        "
        class="modern-credential-usage-hint"
      >
        {{ t('usage.incomplete') }}
      </p>
      <template v-if="subscription">
        <AppCollectionState v-if="quota.isPending.value" :title="t('collection.loading')" loading />
        <AppCollectionState
          v-else-if="quota.isError.value"
          :title="t('credentialCards.quotaHistoryFailed')"
          error
          ><AppButton size="sm" @click="quota.refetch()">{{
            t('ui.retry')
          }}</AppButton></AppCollectionState
        >
        <section v-else-if="quota.data.value?.hasHistory" class="modern-credential-usage-quota">
          <div class="modern-credential-usage-header">
            <h4>{{ t('credentialCards.quotaHistory') }}</h4>
            <AppSelect
              v-if="windows.length > 1"
              v-model="selectedWindow"
              :label="t('credentialCards.window')"
              label-hidden
              size="sm"
              :options="windows"
            />
          </div>
          <template v-if="window && latest">
            <div class="modern-credential-usage-current">
              <span>{{ windowLabel(window) }}</span
              ><strong>{{
                displayedPoint
                  ? n((10_000 - displayedPoint.usedBasisPoints) / 100, {
                      maximumFractionDigits: 2,
                    }) + '%'
                  : '—'
              }}</strong
              ><small>{{ credentialTime(displayedPoint?.observedAt, locale) }}</small>
            </div>
            <CredentialQuotaTrend
              :window="window"
              :from="report.from_ms"
              :to="report.to_ms"
              :bucket-width="quota.data.value.bucketWidth"
              :cursor-at-m-s="cursor"
              @cursor="cursor = $event"
            />
            <p class="modern-credential-usage-hint">{{ t('credentialCards.quotaHistoryHint') }}</p>
          </template>
          <AppCollectionState v-else :title="t('credentialCards.quotaHistoryEmpty')" />
        </section>
      </template>
      <div class="modern-credential-usage-header">
        <h4>{{ t('credentialCards.localUsage') }}</h4>
        <AppSelect
          v-model="metric"
          :label="t('credentialCards.statisticsMetric')"
          label-hidden
          size="sm"
          :options="metrics"
        />
      </div>
      <UsageTrend
        :report="report"
        :metric="metric"
        :cursor-at-m-s="cursor"
        :align-left="subscription && window ? 64 : undefined"
        @cursor="cursor = $event"
      />
      <small class="modern-credential-usage-hint">{{
        t('credentialCards.statisticsBucket', { minutes: report.bucket_width_ms / 60_000 })
      }}</small>
    </template>
  </section>
</template>
<style scoped>
.modern-credential-usage,
.modern-credential-usage-quota {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
}
.modern-credential-usage-header,
.modern-credential-usage-controls,
.modern-credential-usage-current {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-credential-usage-controls {
  flex: none;
}
.modern-credential-usage h3,
.modern-credential-usage h4 {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-credential-usage-summary {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-3);
}
.modern-credential-usage-summary dt,
.modern-credential-usage-hint,
.modern-credential-usage-current small {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-credential-usage-summary dd {
  margin: 0;
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-credential-usage-quota {
  padding-top: var(--modern-space-3);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-credential-usage-current {
  justify-content: flex-start;
  flex-wrap: wrap;
  font-size: var(--modern-font-size-secondary);
}
.modern-credential-usage-current strong {
  color: var(--modern-accent);
}
.modern-credential-usage-current small {
  margin-left: auto;
}
</style>
