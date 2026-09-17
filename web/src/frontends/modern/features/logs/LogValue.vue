<script setup lang="ts">
import { protocolLabel } from '@modern/i18n/protocols'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LogEntry } from '@modern/api/logs'
import type { GroupRow } from '@modern/api/groups'
import type { GroupChannel } from '@modern/api/group-create'
import {
  AppBadge,
  AppProtocolTag,
  AppChannelIcon,
  AppCopyValue,
  AppOverflowText,
  AppTooltip,
} from '@modern/components/ui'
import type { LogColumnId } from './log-columns'
import {
  exactLogMoney,
  logCacheRate,
  logCacheWrites,
  logDuration,
  logHasUsage,
  logMoney,
  logNumber,
  logStatusTone,
  logTime,
} from './log-display'

const props = defineProps<{
  row: LogEntry
  column: LogColumnId
  groups?: ReadonlyMap<number, GroupRow>
  channels?: ReadonlyMap<string, GroupChannel>
  hideIcon?: boolean
  table?: boolean
}>()
const { t, te, locale, n } = useI18n()
const group = computed(() =>
  props.row.group_id ? props.groups?.get(props.row.group_id) : undefined,
)
const channel = computed(() =>
  props.row.channel_id ? props.channels?.get(props.row.channel_id) : undefined,
)
function valueName(value: string | null | undefined): string {
  return !value ? '—' : te('logs.values.' + value) ? t('logs.values.' + value) : value
}
const tokenValue = computed(() => {
  const row = props.row
  if (props.column === 'cache_write_tokens') return logCacheWrites(row)
  if (props.column === 'total_tokens')
    return String(BigInt(row.input_tokens) + BigInt(row.output_tokens))
  if (
    [
      'input_tokens',
      'output_tokens',
      'cache_read_tokens',
      'cache_write_5m_tokens',
      'cache_write_1h_tokens',
      'cache_write_unknown_tokens',
    ].includes(props.column)
  )
    return row[props.column as 'input_tokens']
  return undefined
})
const display = computed(() => {
  const row = props.row
  const column = props.column
  if (tokenValue.value !== undefined)
    return logHasUsage(row) ? logNumber(tokenValue.value, locale.value) : '—'
  switch (column) {
    case 'completed_at_ms':
      return logTime(row.completed_at_ms, locale.value)
    case 'group':
      return row.group_id ? (group.value?.name ?? (props.groups ? t('logs.deleted') : '—')) : '—'
    case 'channel':
      return row.channel_id
        ? (channel.value?.name ?? (props.channels ? t('logs.deleted') : '—'))
        : '—'
    case 'protocol':
    case 'upstream_protocol':
      return protocolLabel(row[column], t)
    case 'access_key':
      if (!row.access_key.id) return '—'
      return (
        row.access_key.name ||
        t(row.access_key.deleted ? 'logs.deletedAccessKey' : 'logs.unavailableAccessKey')
      )
    case 'credential_name':
      return row.credential_name || (row.credential_id ? t('logs.unavailableCredential') : '—')
    case 'stream':
    case 'affinity_hit':
      return t(row[column] ? 'logs.yes' : 'logs.no')
    case 'first_response_ms':
      return row.stream ? logDuration(row.first_response_ms, locale.value) : '—'
    case 'duration_ms':
      return logDuration(row.duration_ms, locale.value)
    case 'attempt_count':
      return n(Math.max(0, row.attempt_count - 1))
    case 'status_code':
      return row.status_code ? String(row.status_code) : '—'
    case 'reasoning_mode': {
      // 三者很少同时出现，按 强度 > 预算 > 开关 取其一；等级保留上游原值（low / high…）。
      const reasoning = row.reasoning
      if (!reasoning) return '—'
      if (reasoning.effort) return reasoning.effort
      if (reasoning.budget_tokens && reasoning.budget_tokens !== '0')
        return reasoning.budget_tokens === '-1'
          ? t('logs.values.auto')
          : logNumber(reasoning.budget_tokens, locale.value)
      return reasoning.mode || '—'
    }
    case 'cache_hit_rate': {
      const rate = logCacheRate(row)
      return logHasUsage(row) && rate !== null ? n(rate, { maximumFractionDigits: 1 }) + '%' : '—'
    }
    case 'estimated_cost_nano_usd':
      return row.cost_state === 'unpriced'
        ? t('logs.values.unpriced')
        : row.cost_state === 'not_applicable'
          ? '—'
          : (row.pricing_completeness === 'partial' ? '≈ ' : '') +
            logMoney(row.estimated_cost_nano_usd, locale.value)
    case 'context_threshold_tokens':
      return row.context_threshold_tokens === null
        ? '—'
        : logNumber(row.context_threshold_tokens, locale.value)
    case 'client_model':
    case 'upstream_model':
    case 'upstream_reported_model':
    case 'error_code':
    case 'error_summary':
    case 'request_id':
      return row[column] || '—'
    default:
      return valueName(row[column as 'client_model'])
  }
})
const deleted = computed(() => {
  const row = props.row
  return (
    (props.column === 'group' &&
      row.group_id !== null &&
      props.groups &&
      !props.groups.has(row.group_id)) ||
    (props.column === 'channel' && Boolean(row.channel_id) && props.channels && !channel.value) ||
    (props.column === 'access_key' && Boolean(row.access_key.id) && row.access_key.deleted) ||
    (props.column === 'credential_name' && row.credential_deleted)
  )
})
const hint = computed(() => {
  const row = props.row
  if (props.column === 'estimated_cost_nano_usd')
    return row.cost_state === 'priced'
      ? `${exactLogMoney(row.estimated_cost_nano_usd)} · ${valueName(row.pricing_completeness)}`
      : valueName(row.cost_state)
  if (props.column === 'attempt_count')
    return t('logs.attemptCount', { count: n(row.attempt_count) })
  return undefined
})
</script>

<template>
  <span v-if="deleted" class="modern-log-deleted">{{ t('logs.deleted') }}</span>
  <AppBadge
    v-else-if="column === 'status'"
    :tone="logStatusTone[row.status]"
    :variant="table ? 'soft' : 'plain'"
    size="xs"
    :class="{ 'modern-log-status-badge': table }"
    dot
  >
    {{ t('logs.values.' + row.status) }}
  </AppBadge>
  <AppCopyValue
    v-else-if="column === 'request_id'"
    :value="row.request_id"
    :label="t('logs.copyRequest')"
  />
  <div v-else-if="column === 'group' && group" class="modern-log-channel">
    <AppChannelIcon
      v-if="!hideIcon && table"
      :icon="group.channelIcon"
      :name="group.channelName"
      :mark="group.channelMark"
      size="sm"
      :tooltip="false"
    /><AppOverflowText :text="display" />
  </div>
  <div v-else-if="column === 'channel'" class="modern-log-channel">
    <AppChannelIcon
      v-if="channel && !hideIcon"
      :icon="channel.icon"
      :name="channel.name"
      :mark="channel.mark"
      size="sm"
      :tooltip="false"
    /><AppOverflowText :text="display" />
  </div>
  <AppProtocolTag
    v-else-if="column === 'protocol' || column === 'upstream_protocol'"
    :protocol="row[column]"
  />
  <span
    v-else-if="table && (column === 'stream' || column === 'affinity_hit')"
    class="modern-log-boolean"
    :class="{ 'is-true': row[column] }"
    >{{ display }}</span
  >
  <span
    v-else-if="table && column === 'status_code'"
    class="modern-log-http"
    :class="{ 'is-failure': row.status_code >= 400, 'is-empty': !row.status_code }"
    >{{ row.status_code ? 'HTTP ' + row.status_code : '—' }}</span
  >
  <AppTooltip v-else-if="table && column === 'attempt_count' && row.attempt_count > 1" :label="hint"
    ><span class="modern-log-retry">{{ display }}</span></AppTooltip
  >
  <AppOverflowText
    v-else
    :text="display"
    :full-text="hint"
    :class="{
      'modern-log-empty': table && display === '—',
      'modern-log-amount':
        table && column === 'estimated_cost_nano_usd' && row.cost_state === 'priced',
      'modern-log-number':
        table &&
        (tokenValue !== undefined || column === 'duration_ms' || column === 'first_response_ms'),
    }"
  />
</template>

<style scoped>
.modern-log-deleted {
  color: var(--modern-muted);
}
.modern-log-channel {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  min-width: 0;
}
.modern-log-status-badge {
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-medium);
  padding-inline: var(--modern-space-1-5);
}
.modern-log-boolean {
  color: var(--modern-muted);
}
.modern-log-boolean.is-true {
  color: color-mix(in srgb, var(--modern-info) 70%, var(--modern-muted));
}
.modern-log-http {
  color: var(--modern-control-placeholder);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
.modern-log-http.is-failure {
  color: var(--modern-danger);
}
.modern-log-empty,
.modern-log-http.is-empty {
  color: var(--modern-control-placeholder);
}
.modern-log-retry {
  display: inline-flex;
  align-items: center;
  min-height: var(--modern-badge-xs);
  border-radius: var(--modern-radius-small);
  background: var(--modern-warning-soft);
  color: var(--modern-warning);
  padding-inline: var(--modern-space-1-5);
  font-variant-numeric: tabular-nums;
}
.modern-log-number {
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-log-amount {
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
  font-variant-numeric: tabular-nums;
}
</style>
