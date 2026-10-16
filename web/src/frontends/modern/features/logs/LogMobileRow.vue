<script setup lang="ts">
import { ChevronRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GroupChannel } from '@modern/api/group-create'
import type { GroupRow } from '@modern/api/groups'
import type { LogEntry } from '@modern/api/logs'
import { AppBadge, AppButton, AppIcon, AppProtocolTag } from '@modern/components/ui'
import {
  logCacheWrites,
  logDuration,
  logHasUsage,
  logModelMismatch,
  logMoney,
  logNumber,
  logOutputRate,
  logStatusTone,
  logTime,
} from './log-display'

const props = defineProps<{
  row: LogEntry
  admin: boolean
  groups?: ReadonlyMap<number, GroupRow>
  channels?: ReadonlyMap<string, GroupChannel>
}>()
defineEmits<{ open: [] }>()
const { t, te, locale } = useI18n()
const model = computed(() => {
  if (props.row.client_model) return props.row.client_model
  const operation = props.row.operation
  return operation
    ? te('logs.values.' + operation)
      ? t('logs.values.' + operation)
      : operation
    : '—'
})
const identities = computed(() => {
  const row = props.row
  const items: { label: string; value: string }[] = []
  if (props.admin && row.group_id) {
    items.push({
      label: t('logs.columns.group'),
      value: props.groups?.get(row.group_id)?.name ?? (props.groups ? t('logs.deleted') : '—'),
    })
  }
  if (row.access_key.id) {
    items.push({
      label: t('logs.columns.access_key'),
      value: row.access_key.deleted
        ? t('logs.deletedAccessKey')
        : row.access_key.name || t('logs.unavailableAccessKey'),
    })
  }
  if (props.admin && row.channel_id) {
    items.push({
      label: t('logs.columns.channel'),
      value:
        props.channels?.get(row.channel_id)?.name ?? (props.channels ? t('logs.deleted') : '—'),
    })
  }
  if (props.admin && (row.credential_id || row.credential_name)) {
    items.push({
      label: t('logs.columns.credential_name'),
      value:
        row.credential_name ||
        t(row.credential_deleted ? 'logs.deleted' : 'logs.unavailableCredential'),
    })
  }
  return items
})
const metrics = computed(() => {
  const row = props.row
  const tokens = (value: string) => (logHasUsage(row) ? logNumber(value, locale.value, true) : '—')
  const cost =
    row.cost_state === 'unpriced'
      ? t('logs.values.unpriced')
      : row.cost_state === 'not_applicable'
        ? '—'
        : (row.pricing_completeness === 'partial' ? '≈ ' : '') +
          logMoney(row.estimated_cost_nano_usd, locale.value)
  return [
    { label: t('logs.columns.duration_ms'), value: logDuration(row.duration_ms, locale.value) },
    {
      label: t('logs.columns.first_response_ms'),
      value:
        row.stream && row.first_response_ms !== null && row.first_response_ms > 0
          ? logDuration(row.first_response_ms, locale.value)
          : '—',
    },
    { label: t('logs.columns.input_tokens') + ' Token', value: tokens(row.input_tokens) },
    { label: t('logs.columns.output_tokens') + ' Token', value: tokens(row.output_tokens) },
    { label: t('logs.columns.estimated_cost_nano_usd'), value: cost },
    ...(row.stream
      ? [{ label: t('logs.outputRate'), value: logOutputRate(row, locale.value) }]
      : []),
  ]
})
const cache = computed(() =>
  logHasUsage(props.row)
    ? [
        { label: t('logs.columns.cache_read_tokens'), value: props.row.cache_read_tokens },
        { label: t('logs.columns.cache_write_tokens'), value: logCacheWrites(props.row) },
      ].filter((item) => BigInt(item.value) > 0n)
    : [],
)
</script>

<template>
  <AppButton class="modern-log-mobile" @click="$emit('open')">
    <span class="modern-log-mobile-heading">
      <AppBadge :tone="logStatusTone[row.status]" variant="plain" size="xs" dot>{{
        t('logs.values.' + row.status)
      }}</AppBadge>
      <span class="modern-log-mobile-http" :class="{ 'is-error': row.status_code >= 400 }">{{
        row.status_code ? 'HTTP ' + row.status_code : t('logs.noResponse')
      }}</span>
      <time :datetime="new Date(row.completed_at_ms).toISOString()">{{
        logTime(row.completed_at_ms, locale)
      }}</time>
    </span>
    <span class="modern-log-mobile-model">
      <strong>{{ model }}</strong>
      <AppIcon :icon="ChevronRight" size="sm" />
      <span class="modern-sr-only">{{ t('logs.details') }}</span>
    </span>
    <span class="modern-log-mobile-badges">
      <AppProtocolTag :protocol="row.protocol" />
      <AppBadge v-if="row.stream" size="xs">{{ t('logs.columns.stream') }}</AppBadge>
      <AppBadge v-if="admin && row.attempt_count > 1" tone="warning" variant="plain" size="xs">
        {{ t('logs.columns.attempt_count') }} {{ logNumber(row.attempt_count - 1, locale) }}
      </AppBadge>
      <AppBadge v-if="row.usage_state === 'partial'" tone="warning" variant="plain" size="xs">
        {{ t('logs.usageInfo') }} · {{ t('logs.values.partial') }}
      </AppBadge>
      <AppBadge v-if="admin && logModelMismatch(row)" tone="warning" variant="plain" size="xs">
        {{ t('logs.returnedModelMismatch') }}
      </AppBadge>
    </span>
    <span
      v-if="admin && row.upstream_model && row.upstream_model !== row.client_model"
      class="modern-log-mobile-upstream"
    >
      <span class="modern-log-mobile-label">{{ t('logs.columns.upstream_model') }}</span>
      <span class="modern-log-mobile-upstream-value">{{ row.upstream_model }}</span>
    </span>
    <span v-if="identities.length" class="modern-log-mobile-sources">
      <span v-for="item in identities" :key="item.label" class="modern-log-mobile-source">
        <span class="modern-log-mobile-label">{{ item.label }}</span>
        <span class="modern-log-mobile-source-value">{{ item.value }}</span>
      </span>
    </span>
    <span class="modern-log-mobile-metrics">
      <span v-for="item in metrics" :key="item.label" class="modern-log-mobile-metric">
        <span class="modern-log-mobile-label">{{ item.label }}</span>
        <strong>{{ item.value }}</strong>
      </span>
    </span>
    <span v-if="cache.length" class="modern-log-mobile-cache">
      <span v-for="item in cache" :key="item.label">
        {{ item.label }} {{ logNumber(item.value, locale, true) }}
      </span>
    </span>
    <span v-if="row.error_code || row.error_summary" class="modern-log-mobile-error">{{
      row.error_summary || row.error_code
    }}</span>
  </AppButton>
</template>

<style scoped>
.modern-log-mobile {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  justify-content: stretch;
  color: var(--modern-text);
  width: 100%;
  min-width: 0;
  gap: var(--modern-space-1-5);
  height: auto;
  padding: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  text-align: left;
  white-space: normal;
  font-weight: var(--modern-weight-regular);
  box-shadow: none;
}
.modern-log-mobile + .modern-log-mobile {
  margin-top: var(--modern-space-2);
}
.modern-log-mobile:focus-visible {
  outline-offset: calc(-1 * var(--modern-focus-width));
}
.modern-log-mobile-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1) var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
.modern-log-mobile-heading time {
  margin-left: auto;
}
.modern-log-mobile-http.is-error {
  color: var(--modern-danger);
}
.modern-log-mobile-model {
  display: flex;
  align-items: flex-start;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-log-mobile-model strong {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  white-space: normal;
  overflow-wrap: anywhere;
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-semibold);
}
.modern-log-mobile-model > :deep(.modern-icon) {
  margin-top: var(--modern-space-0-5);
  color: var(--modern-muted);
}
.modern-log-mobile-badges,
.modern-log-mobile-cache {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1) var(--modern-space-2);
}
.modern-log-mobile-upstream {
  display: flex;
  min-width: 0;
  gap: var(--modern-space-1-5);
  font-size: var(--modern-font-size-small);
  overflow-wrap: anywhere;
}
.modern-log-mobile-upstream-value {
  display: -webkit-box;
  min-width: 0;
  overflow: hidden;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.modern-log-mobile-sources,
.modern-log-mobile-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-1-5) var(--modern-space-3);
  min-width: 0;
  font-size: var(--modern-font-size-small);
}
.modern-log-mobile-source {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: var(--modern-space-1-5);
}
.modern-log-mobile-source-value {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-log-mobile-label {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-log-mobile-source > .modern-log-mobile-label,
.modern-log-mobile-upstream > .modern-log-mobile-label {
  flex: none;
}
.modern-log-mobile-metrics {
  padding-top: var(--modern-space-2);
  border-top: var(--modern-line-width) solid var(--modern-border);
  font-variant-numeric: tabular-nums;
}
.modern-log-mobile-metric {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--modern-space-0-5) var(--modern-space-1);
  min-width: 0;
}
.modern-log-mobile-metric strong {
  margin-left: auto;
  overflow-wrap: anywhere;
  font-weight: var(--modern-weight-medium);
}
.modern-log-mobile-cache {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-log-mobile-error {
  color: var(--modern-danger);
  background: var(--modern-danger-soft);
  border-radius: var(--modern-radius-small);
  padding: var(--modern-space-1-5) var(--modern-space-2);
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  white-space: normal;
  overflow-wrap: anywhere;
  font-size: var(--modern-font-size-small);
}
</style>
