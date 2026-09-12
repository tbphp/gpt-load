<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GroupRow, GroupUsage } from '@modern/api/groups'
import {
  AppBadge,
  AppButton,
  AppLoadingIndicator,
  AppOverflowText,
  AppSegmentedBar,
  AppTooltip,
} from '@modern/components/ui'
import { formatCompactNumber, formatNanoUSD } from '@modern/components/ui/format'

const props = defineProps<{
  group: GroupRow
  usage?: GroupUsage
  loading: boolean
  incomplete: boolean
  failed: boolean
}>()
defineEmits<{ retry: [] }>()
const { t, n, locale } = useI18n()
const segments = computed(() =>
  (
    [
      ['available', 'success'],
      ['cooldown', 'warning'],
      ['blacklisted', 'danger'],
      ['disabled', 'neutral'],
    ] as const
  ).map(([key, tone]) => ({ key, tone, value: props.group.credentials[key] })),
)
const statusLabel = computed(() =>
  segments.value
    .map((item) => `${t('groups.credentials.' + item.key)} ${n(item.value)}`)
    .join(' · '),
)
const missing = computed(() => !props.usage || (props.incomplete && props.usage.requests === 0))
const failureRate = computed(() =>
  missing.value || !props.usage?.requests
    ? '—'
    : new Intl.NumberFormat(locale.value, { style: 'percent', maximumFractionDigits: 1 }).format(
        (props.usage.requests - props.usage.successes) / props.usage.requests,
      ),
)
const partial = computed(() => props.incomplete || props.usage?.incomplete)
</script>
<template>
  <section class="modern-group-overview" :aria-label="t('groupDetail.overview')">
    <AppLoadingIndicator :loading="loading" />
    <div class="modern-group-overview-heading">
      <h2>{{ t('groupDetail.overview') }}</h2>
      <AppTooltip v-if="partial" :label="t('groups.row.partialHelp')"
        ><AppBadge size="xs" tabindex="0">{{ t('groups.row.partial') }}</AppBadge></AppTooltip
      >
    </div>
    <div class="modern-group-overview-availability">
      <span>{{ t('groups.credentials.available') }}</span
      ><span
        ><strong>{{ n(group.credentials.available) }}</strong> /
        {{ n(group.credentials.total) }}</span
      >
    </div>
    <AppSegmentedBar :segments="segments" :label="statusLabel" />
    <dl class="modern-group-overview-metrics">
      <div>
        <dt>{{ t('groups.row.requests24h') }}</dt>
        <dd>
          <AppOverflowText
            :text="missing ? '—' : formatCompactNumber(usage!.requests, locale)"
            :full-text="usage ? n(usage.requests) : undefined"
          />
        </dd>
      </div>
      <div>
        <dt>{{ t('groupDetail.failureRate') }}</dt>
        <dd>{{ failureRate }}</dd>
      </div>
      <div>
        <dt>{{ t('groups.row.estimatedCost') }}</dt>
        <dd>{{ missing ? '—' : formatNanoUSD(usage!.costNanoUSD, locale) }}</dd>
      </div>
    </dl>
    <AppButton v-if="failed" variant="text" size="xs" @click="$emit('retry')">{{
      t('groupDetail.retryUsage')
    }}</AppButton>
  </section>
</template>
<style scoped>
.modern-group-overview {
  position: relative;
  display: grid;
  gap: var(--modern-space-3);
  padding-bottom: var(--modern-space-4);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-group-overview-heading,
.modern-group-overview-availability {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
}
.modern-group-overview-heading h2 {
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
}
.modern-group-overview-availability {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-overview-availability strong {
  color: var(--modern-text);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-group-overview-metrics {
  display: grid;
  gap: var(--modern-space-2);
}
.modern-group-overview-metrics > div {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-3);
}
.modern-group-overview-metrics dt {
  flex: 1;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-overview-metrics dd {
  min-width: 0;
  margin: 0;
  font-size: var(--modern-font-size-secondary);
  font-variant-numeric: tabular-nums;
}
</style>
