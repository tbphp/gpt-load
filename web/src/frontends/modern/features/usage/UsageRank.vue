<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ScrollText } from '@lucide/vue'
import type { UsageDistribution, UsageItem, UsageMetric, UsageDimension } from '@modern/api/usage'
import type { GroupRow } from '@modern/api/groups'
import type { LogAccessKeyOption } from '@modern/api/logs'
import {
  AppButton,
  AppChannelIcon,
  AppIconButton,
  AppOverflowText,
  AppTooltip,
} from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import { amount, distributionValue, percentage } from './usage-display'
const props = defineProps<{
  dimension: UsageDimension
  distribution: UsageDistribution
  metric: UsageMetric
  costUnavailable: boolean
  groups?: readonly GroupRow[]
  accessKeys?: readonly LogAccessKeyOption[]
  disabled?: boolean
}>()
const emit = defineEmits<{ select: [item: UsageItem]; logs: [item: UsageItem] }>()
const { t, locale } = useI18n()
const groupMap = computed(() => new Map(props.groups?.map((row) => [row.id, row])))
const keyMap = computed(() => new Map(props.accessKeys?.map((row) => [row.id, row])))
const rows = computed(() => [
  ...props.distribution.items.map((row) => ({ ...row, other: false })),
  ...(props.distribution.other ? [{ ...props.distribution.other, other: true }] : []),
])
const total = computed(() =>
  rows.value.reduce((sum, row) => sum + distributionValue(row, props.metric), 0),
)
const share = (row: UsageItem) =>
  total.value ? (distributionValue(row, props.metric) / total.value) * 100 : 0
const display = (row: UsageItem, metric: UsageMetric) =>
  metric === 'cost' && props.costUnavailable ? '—' : amount(row, metric, locale.value)
const name = (row: UsageItem & { other: boolean }) =>
  row.other
    ? t('usage.other')
    : props.dimension === 'model'
      ? row.model || t('usage.unknownModel')
      : props.dimension === 'group'
        ? (groupMap.value.get(row.group_id!)?.name ?? (props.groups ? t('logs.deleted') : '—'))
        : (keyMap.value.get(row.access_key_id!)?.name ??
          (props.accessKeys ? t('logs.deleted') : '—'))
const details = (row: UsageItem) =>
  `${t('usage.requests')} ${formatCompactNumber(row.request_count, locale.value)}\n${t('usage.tokens')} ${formatCompactNumber(row.total_tokens, locale.value)}\n${t('usage.cost')} ${display(row, 'cost')}`
</script>

<template>
  <section class="modern-usage-rank" :data-metric="metric">
    <header class="modern-usage-rank-heading">
      <h3>{{ t('usage.dimensions.' + dimension) }}</h3>
      <span>{{ t('usage.topFive') }}</span>
    </header>
    <div v-if="!rows.length" class="modern-usage-rank-empty">{{ t('usage.noData') }}</div>
    <table v-else class="modern-usage-rank-table" :aria-label="t('usage.dimensions.' + dimension)">
      <colgroup>
        <col />
        <col class="modern-usage-value-column" />
        <col class="modern-usage-share-column" />
        <col class="modern-usage-action-column" />
      </colgroup>
      <thead>
        <tr>
          <th scope="col">{{ t('usage.name') }}</th>
          <th scope="col">{{ t('usage.' + metric) }}</th>
          <th scope="col">{{ t('usage.share') }}</th>
          <th scope="col">
            <span class="modern-sr-only">{{ t('usage.viewLogs') }}</span>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="(row, index) in rows"
          :key="row.other ? 'other' : (row.model ?? row.group_id ?? row.access_key_id ?? index)"
        >
          <td>
            <div class="modern-usage-rank-identity">
              <span class="modern-usage-position">{{ row.other ? '·' : index + 1 }}</span>
              <AppButton
                v-if="!row.other"
                variant="text"
                class="modern-usage-rank-name"
                :disabled="disabled || (dimension === 'model' && !row.model)"
                @click="emit('select', row)"
              >
                <AppChannelIcon
                  v-if="dimension === 'group' && groupMap.get(row.group_id!)"
                  :icon="groupMap.get(row.group_id!)!.channelIcon"
                  :name="groupMap.get(row.group_id!)!.channelName"
                  :mark="groupMap.get(row.group_id!)!.channelMark"
                  size="sm"
                />
                <AppOverflowText :text="name(row)" />
              </AppButton>
              <span v-else class="modern-usage-rank-other">{{ name(row) }}</span>
            </div>
          </td>
          <td class="modern-usage-rank-number">
            <AppTooltip :label="details(row)"
              ><span tabindex="0">{{ display(row, metric) }}</span></AppTooltip
            >
          </td>
          <td class="modern-usage-rank-share">
            {{ total && display(row, metric) !== '—' ? percentage(share(row), locale) : '—' }}
            <div class="modern-usage-rank-track" aria-hidden="true">
              <span :style="{ width: share(row) + '%' }" />
            </div>
          </td>
          <td>
            <AppIconButton
              v-if="!row.other"
              :icon="ScrollText"
              :label="t('usage.viewLogs')"
              :tooltip="true"
              size="xs"
              :disabled="disabled || (dimension === 'model' && !row.model)"
              @click="emit('logs', row)"
            />
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<style scoped>
.modern-usage-rank {
  --modern-usage-rank-tone: var(--modern-accent);
  min-width: 0;
}
.modern-usage-rank[data-metric='tokens'] {
  --modern-usage-rank-tone: var(--modern-chart-input);
}
.modern-usage-rank[data-metric='cost'] {
  --modern-usage-rank-tone: var(--modern-chart-output);
}
.modern-usage-rank-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  margin-bottom: var(--modern-space-3);
}
.modern-usage-rank-heading h3 {
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-semibold);
}
.modern-usage-rank-heading span {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-usage-rank-table {
  width: 100%;
  table-layout: fixed;
  border-collapse: collapse;
  text-align: left;
  font-size: var(--modern-font-size-secondary);
  font-variant-numeric: tabular-nums;
}
.modern-usage-value-column {
  width: 76px;
}
.modern-usage-share-column {
  width: 64px;
}
.modern-usage-action-column {
  width: var(--modern-control-xs);
}
.modern-usage-rank-table th {
  padding-bottom: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-regular);
}
.modern-usage-rank-table td {
  padding-block: var(--modern-space-2);
  border-top: var(--modern-line-width) solid
    color-mix(in srgb, var(--modern-border) 65%, var(--modern-surface));
  vertical-align: middle;
}
.modern-usage-rank-table td:first-child {
  padding-right: var(--modern-space-3);
}
.modern-usage-rank-table tbody tr:hover,
.modern-usage-rank-table tbody tr:focus-within {
  background: var(--modern-control-hover);
}
.modern-usage-rank-identity {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-usage-position {
  flex: none;
  width: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-usage-rank-table tbody tr:first-child .modern-usage-position {
  color: var(--modern-usage-rank-tone);
  font-weight: var(--modern-weight-semibold);
}
.modern-usage-rank-name {
  min-width: 0;
  max-width: 100%;
}
.modern-usage-rank-other {
  color: var(--modern-muted);
}
.modern-usage-rank-number {
  color: var(--modern-text);
  overflow-wrap: anywhere;
}
.modern-usage-rank-share {
  padding-right: var(--modern-space-3);
  color: var(--modern-muted);
}
.modern-usage-rank-track {
  height: var(--modern-space-1);
  margin-top: var(--modern-space-1);
  overflow: hidden;
  border-radius: var(--modern-radius-small);
  background: color-mix(in srgb, var(--modern-usage-rank-tone) 8%, var(--modern-surface));
}
.modern-usage-rank-track span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: color-mix(in srgb, var(--modern-usage-rank-tone) 62%, var(--modern-surface));
}
.modern-usage-rank-empty {
  padding-block: var(--modern-space-8);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  text-align: center;
}
@media (max-width: 760px) {
  .modern-usage-action-column {
    width: var(--modern-touch-target);
  }
  .modern-usage-value-column {
    width: 68px;
  }
  .modern-usage-share-column {
    width: 48px;
  }
}
</style>
