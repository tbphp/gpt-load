<script setup lang="ts">
import { numberFormatter } from '@modern/components/ui/intl-formatters'
import { protocolLabel } from '@modern/i18n/protocols'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccessKey, CostRule, CostWindow } from '@modern/api/access-keys'
import { AppProgressBar, AppTag, AppTooltip, type SemanticTone } from '@modern/components/ui'
import { accessNanoUSD, accessTime } from './access-key-display'
import { periodUnits } from './access-key-draft'

const props = defineProps<{ row: AccessKey; groups: ReadonlyMap<string, { name: string }> }>()
const { t, n, locale } = useI18n()
const tags = computed(() => {
  const scope = props.row.filters
  const definitions = [
    {
      key: 'groups',
      label: t('accessKeys.groups'),
      values: scope.groups.map(
        (id) => props.groups.get(String(id))?.name ?? t('accessKeys.groupUnavailable'),
      ),
    },
    {
      key: 'protocols',
      label: t('accessKeys.protocols'),
      values: scope.protocols.map((value) => protocolLabel(value, t)),
    },
    { key: 'models', label: t('accessKeys.models'), values: scope.models },
    { key: 'sources', label: t('accessKeys.sourceTag'), values: scope.allowed_cidrs },
  ]
  return [
    ...definitions
      .filter((item) => item.values.length)
      .map((item) => ({
        key: item.key,
        text: `${item.label} ${n(item.values.length)}`,
        tooltip: [item.label, ...item.values].join('\n'),
      })),
    ...(props.row.rpm_limit > 0
      ? [
          {
            key: 'rpm',
            text: `${t('accessKeys.rateTag')} ${n(props.row.rpm_limit)}`,
            tooltip: `${t('accessKeys.rpm')}：${n(props.row.rpm_limit)}`,
          },
        ]
      : []),
    ...(props.row.expires_at_ms !== null
      ? [
          {
            key: 'expiry',
            text: t('accessKeys.expires'),
            tooltip: `${t('accessKeys.expires')}：${accessTime(props.row.expires_at_ms, locale.value)}`,
          },
        ]
      : []),
  ]
})
interface QuotaEntry {
  rule: CostRule
  window?: CostWindow
  remaining?: bigint
  limit?: bigint
}
const entries = computed<QuotaEntry[]>(() => {
  const windows = new Map(props.row.cost_limit_status?.rules.map((rule) => [rule.id, rule]))
  return props.row.cost_limit_rules.map((rule) => {
    const window = rule.id === undefined ? undefined : windows.get(rule.id)
    const limit = accessNanoUSD(rule.limit_usd)
    if (
      !window ||
      limit <= 0n ||
      window.kind !== rule.kind ||
      (window.period_seconds ?? 0) !== (rule.period_seconds ?? 0) ||
      accessNanoUSD(window.limit_usd) !== limit
    )
      return { rule }
    return { rule, window, limit, remaining: accessNanoUSD(window.remaining_usd) }
  })
})
const lowest = computed(() => {
  let selected: QuotaEntry | undefined
  for (const entry of entries.value) {
    if (entry.remaining === undefined || entry.limit === undefined) continue
    if (!selected || entry.remaining * selected.limit! < selected.remaining! * entry.limit)
      selected = entry
  }
  return selected
})
function percentage(entry?: QuotaEntry): number | undefined {
  if (entry?.remaining === undefined || entry.limit === undefined) return undefined
  return Math.min(100, Number((entry.remaining * 10000n) / entry.limit) / 100)
}
const percent = computed(() => percentage(lowest.value))
function percentText(entry: QuotaEntry): string {
  const value = percentage(entry)
  if (value === undefined) return '—'
  const formatter = numberFormatter(locale.value, {
    style: 'percent',
    maximumFractionDigits: 2,
  })
  return value === 0 && entry.remaining! > 0n
    ? '<' + formatter.format(0.0001)
    : formatter.format(value / 100)
}
function ruleName(rule: CostRule): string {
  if (rule.kind === 'total') return t('accessKeys.totalQuota')
  if (!rule.period_seconds) return t('accessKeys.periodicQuota')
  const unit = rule.period_seconds % periodUnits.day === 0 ? 'day' : 'hour'
  return t('accessKeys.every', {
    count: numberFormatter(locale.value, { maximumFractionDigits: 12 }).format(
      rule.period_seconds / periodUnits[unit],
    ),
    unit: t('accessKeys.units.' + unit),
  })
}
const quotaLabel = computed(() => {
  const heading = t('accessKeys.quotaRules', { count: n(entries.value.length) })
  const current = lowest.value
    ? t('accessKeys.quotaMinimum', { value: percentText(lowest.value) })
    : t('accessKeys.quotaUnavailable')
  const details = entries.value.map((entry) => {
    const title = `${ruleName(entry.rule)} · $${entry.rule.limit_usd}`
    if (!entry.window) return `${title}\n${t('accessKeys.quotaUnavailable')}`
    const remaining = `${t('accessKeys.remaining')} $${entry.window.remaining_usd} · ${percentText(entry)}`
    const used = `${t('accessKeys.used')} $${entry.window.used_usd}`
    const recovery =
      entry.window.status === 'inactive'
        ? t('accessKeys.inactive')
        : entry.window.window_ends_at_ms
          ? t('accessKeys.recovers', {
              time: accessTime(entry.window.window_ends_at_ms, locale.value),
            })
          : t('accessKeys.noRecovery')
    return [title, remaining, used, recovery].join('\n')
  })
  return [heading, current, ...details].join('\n\n')
})
const tone = computed<SemanticTone>(() =>
  percent.value === undefined
    ? 'neutral'
    : percent.value <= 10
      ? 'danger'
      : percent.value <= 30
        ? 'warning'
        : 'success',
)
</script>
<template>
  <div class="modern-access-restrictions">
    <span v-if="!tags.length && !entries.length" class="modern-access-unrestricted">—</span>
    <AppTooltip v-if="entries.length" :label="quotaLabel">
      <div class="modern-access-quota-summary" tabindex="0" :aria-label="quotaLabel">
        <span class="modern-access-quota-count">{{ n(entries.length) }}</span>
        <AppProgressBar
          class="modern-access-quota-bar"
          :value="percent"
          :tone="tone"
          :label="
            lowest
              ? t('accessKeys.quotaMinimum', { value: percentText(lowest) })
              : t('accessKeys.quotaUnavailable')
          "
          size="sm"
        />
      </div>
    </AppTooltip>
    <div v-if="tags.length" class="modern-access-restriction-tags">
      <AppTooltip v-for="tag in tags" :key="tag.key" :label="tag.tooltip"
        ><AppTag
          class="modern-access-restriction-tag"
          :text="tag.text"
          size="xs"
          tone="neutral"
          tabindex="0"
      /></AppTooltip>
    </div>
  </div>
</template>
<style scoped>
.modern-access-restrictions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-access-unrestricted {
  color: var(--modern-muted);
}
.modern-access-restriction-tags {
  display: flex;
  flex: 0 1 auto;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-access-restriction-tag {
  flex: 0 1 auto;
}
.modern-access-quota-summary {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: var(--modern-space-1-5);
  min-width: 0;
}
.modern-access-quota-count {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
.modern-access-quota-bar {
  width: 72px;
  flex: none;
}
</style>
