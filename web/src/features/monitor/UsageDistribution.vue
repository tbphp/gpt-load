<script setup lang="ts">
import { Boxes, KeyRound, Layers3 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupOptionDto } from '@/api/control/types'
import type { AccessKeyOptionDto } from '@/api/control/types'
import type { ChannelDto } from '@/app/resources/channels'
import type {
  UsageAggregateDto,
  UsageDistributionAggregateDto,
  UsageDistributionDto,
} from '@/app/resources/usage'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import { formatEstimatedCost, formatInteger, formatPercent, formatTokens } from '@/lib/format'

type DistributionItem = UsageDistributionDto['items'][number]
type DistributionRow = {
  key: string
  item: UsageDistributionAggregateDto
  identity?: DistributionItem
  rank?: number
}

const props = defineProps<{
  distribution: UsageDistributionDto
  summary: UsageAggregateDto
  groups: GroupOptionDto[]
  channels: ChannelDto[]
  accessKeys: AccessKeyOptionDto[]
}>()

const { locale, t } = useI18n()

const rows = computed<DistributionRow[]>(() => {
  const items: DistributionRow[] = props.distribution.items.map((item, index) => ({
    key:
      props.distribution.dimension === 'group'
        ? `group:${item.group_id ?? 0}`
        : props.distribution.dimension === 'access_key'
          ? `access-key:${item.access_key_id ?? 0}`
          : `model:${item.model ?? ''}`,
    item,
    identity: item,
    rank: index + 1,
  }))
  if (props.distribution.other !== null) {
    items.push({ key: 'other', item: props.distribution.other })
  }
  return items
})

function group(item: DistributionItem): GroupOptionDto | undefined {
  return props.groups.find(({ id }) => id === item.group_id)
}

function channel(item: DistributionItem): ChannelDto | undefined {
  const channelID = group(item)?.channel_id
  return props.channels.find(({ channel_id }) => channel_id === channelID)
}

function accessKey(item: DistributionItem): AccessKeyOptionDto | undefined {
  return props.accessKeys.find(({ id }) => id === item.access_key_id)
}

function identityLabel(row: DistributionRow): string {
  if (row.identity === undefined) return t('monitor.usage.distribution.other')
  if (props.distribution.dimension === 'model') {
    return row.identity.model || t('monitor.usage.distribution.unknownModel')
  }
  if (props.distribution.dimension === 'access_key') {
    const accessKeyID = row.identity.access_key_id ?? 0
    return (
      accessKey(row.identity)?.name ??
      t('monitor.usage.distribution.deletedOrUnknownAccessKey', { id: accessKeyID })
    )
  }
  const groupID = row.identity.group_id ?? 0
  return group(row.identity)?.name ?? t('monitor.usage.filters.deletedOrUnknown', { id: groupID })
}

function identityMeta(row: DistributionRow): string {
  if (row.identity === undefined) {
    if (props.distribution.dimension === 'group') {
      return t('monitor.usage.distribution.otherGroupHint')
    }
    if (props.distribution.dimension === 'access_key') {
      return t('monitor.usage.distribution.otherAccessKeyHint')
    }
    return t('monitor.usage.distribution.otherHint')
  }
  if (props.distribution.dimension === 'model') {
    return t('monitor.usage.distribution.modelHint')
  }
  if (props.distribution.dimension === 'access_key') {
    return t('monitor.usage.distribution.accessKeyValue', {
      id: row.identity.access_key_id ?? 0,
    })
  }
  const groupID = row.identity.group_id ?? 0
  const groupChannel = channel(row.identity)
  const groupLabel = t('monitor.usage.distribution.groupValue', { id: groupID })
  return groupChannel ? `${groupChannel.name} · ${groupLabel}` : groupLabel
}

function metricValue(item: UsageDistributionAggregateDto): number | bigint {
  if (props.distribution.metric === 'cost') return BigInt(item.estimated_cost_nano_usd)
  if (props.distribution.metric === 'tokens') return item.total_tokens
  return item.request_count
}

function totalValue(): number | bigint {
  if (props.distribution.metric === 'cost') {
    return BigInt(props.summary.estimated_cost_nano_usd)
  }
  if (props.distribution.metric === 'tokens') return props.summary.total_tokens
  return props.summary.request_count
}

function shareBasisPoints(item: UsageDistributionAggregateDto): number {
  const value = metricValue(item)
  const total = totalValue()
  if (typeof value === 'bigint' && typeof total === 'bigint') {
    if (total === 0n) return 0
    return Number((value * 10_000n + total / 2n) / total)
  }
  if (typeof value === 'number' && typeof total === 'number') {
    if (total === 0) return 0
    return Math.round((value / total) * 10_000)
  }
  return 0
}

function shareLabel(item: UsageDistributionAggregateDto): string {
  return formatPercent(shareBasisPoints(item), 10_000, locale.value)
}

function barStyle(item: UsageDistributionAggregateDto): Record<string, string> {
  return { '--distribution-share': `${shareBasisPoints(item) / 100}%` }
}

function primaryValue(item: UsageDistributionAggregateDto): string {
  if (props.distribution.metric === 'cost') {
    return formatEstimatedCost(item.estimated_cost_nano_usd, locale.value)
  }
  if (props.distribution.metric === 'tokens') {
    return formatTokens(item.total_tokens, locale.value)
  }
  return formatInteger(item.request_count, locale.value)
}

function secondaryValue(item: UsageDistributionAggregateDto): string {
  if (props.distribution.metric === 'requests') {
    return formatEstimatedCost(item.estimated_cost_nano_usd, locale.value)
  }
  return t('monitor.usage.distribution.requestsValue', {
    value: formatInteger(item.request_count, locale.value),
  })
}
</script>

<template>
  <ol class="usage-distribution" :aria-label="t('monitor.usage.distribution.caption')">
    <li v-for="row in rows" :key="row.key" class="usage-distribution__item">
      <div
        class="usage-distribution__row"
        :class="{ 'usage-distribution__row--other': row.identity === undefined }"
        :title="identityLabel(row)"
      >
        <span class="usage-distribution__rank" aria-hidden="true">
          {{ row.rank === undefined ? '∑' : String(row.rank).padStart(2, '0') }}
        </span>

        <span class="usage-distribution__identity" :title="identityLabel(row)">
          <span class="usage-distribution__icon" aria-hidden="true">
            <template v-if="distribution.dimension === 'group'">
              <ChannelIcon
                v-if="row.identity && channel(row.identity)"
                :icon="channel(row.identity)!.icon"
                :mark="channel(row.identity)!.mark"
              />
              <Boxes v-else :size="17" />
            </template>
            <KeyRound v-else-if="distribution.dimension === 'access_key'" :size="17" />
            <Layers3 v-else :size="17" />
          </span>
          <span class="usage-distribution__identity-copy">
            <OverflowTooltip as="strong" :content="identityLabel(row)" :focusable="false">
              {{ identityLabel(row) }}
            </OverflowTooltip>
            <small>{{ identityMeta(row) }}</small>
          </span>
        </span>

        <span class="usage-distribution__visual">
          <span class="usage-distribution__track" aria-hidden="true">
            <span class="usage-distribution__fill" :style="barStyle(row.item)" />
          </span>
        </span>

        <span class="usage-distribution__values">
          <strong>{{ primaryValue(row.item) }}</strong>
          <small>{{ secondaryValue(row.item) }}</small>
        </span>

        <span class="usage-distribution__share">{{ shareLabel(row.item) }}</span>
      </div>
    </li>
  </ol>
</template>

<style scoped>
.usage-distribution {
  display: grid;
  min-width: 0;
  overflow: hidden;
  margin: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: 0;
}

.usage-distribution__item {
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  list-style: none;
}

.usage-distribution__row {
  display: grid;
  min-width: 0;
  min-height: 62px;
  grid-template-columns: 34px minmax(180px, 0.85fr) minmax(160px, 1.25fr) 116px 64px;
  align-items: center;
  gap: 14px;
  border: 0;
  background: transparent;
  color: inherit;
  padding: 10px 15px;
  text-align: left;
  font: inherit;
}

.usage-distribution__item:last-child {
  border-bottom: 0;
}

.usage-distribution__row--other {
  background: var(--color-surface-sunken);
}

.usage-distribution__rank,
.usage-distribution__share,
.usage-distribution__values {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.usage-distribution__rank {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  letter-spacing: 0.04em;
}

.usage-distribution__identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.usage-distribution__icon {
  display: grid;
  width: 30px;
  height: 30px;
  flex: none;
  place-items: center;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  font-size: 17px;
}

.usage-distribution__identity-copy,
.usage-distribution__values {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.usage-distribution__identity-copy strong {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--text-sm);
  font-weight: 620;
}

.usage-distribution__identity-copy small,
.usage-distribution__values small {
  overflow: hidden;
  color: var(--color-text-faint);
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--text-label-xs);
}

.usage-distribution__visual {
  min-width: 0;
}

.usage-distribution__track {
  display: block;
  overflow: hidden;
  height: 8px;
  border-radius: 999px;
  background: var(--color-surface-sunken);
}

.usage-distribution__fill {
  display: block;
  width: var(--distribution-share);
  min-width: min(2px, var(--distribution-share));
  height: 100%;
  border-radius: inherit;
  background: var(--color-action);
  transition: width var(--duration-normal) var(--easing-standard);
}

.usage-distribution__row--other .usage-distribution__fill {
  background: var(--color-text-faint);
}

.usage-distribution__values {
  align-items: flex-end;
  text-align: right;
}

.usage-distribution__values strong {
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 620;
}

.usage-distribution__share {
  color: var(--color-text-muted);
  text-align: right;
  font-size: var(--text-sm);
}

@media (max-width: 820px) {
  .usage-distribution__row {
    grid-template-columns: 30px minmax(0, 1fr) 104px 58px;
    gap: 10px;
    padding-inline: 12px;
  }

  .usage-distribution__visual {
    display: none;
  }
}

@media (max-width: 560px) {
  .usage-distribution__row {
    min-height: 68px;
    grid-template-columns: 26px minmax(0, 1fr) 86px;
  }

  .usage-distribution__share {
    display: none;
  }

  .usage-distribution__icon {
    width: 28px;
    height: 28px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .usage-distribution__fill {
    transition: none;
  }
}
</style>
