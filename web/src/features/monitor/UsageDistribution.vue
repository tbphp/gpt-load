<script setup lang="ts">
import { Boxes, ChevronRight, Layers3 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupOptionDto } from '@/api/control/types'
import type { ChannelDto } from '@/app/resources/channels'
import type {
  UsageAggregateDto,
  UsageDistributionAggregateDto,
  UsageReportDto,
} from '@/app/resources/usage'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import { formatEstimatedCost, formatInteger, formatPercent } from '@/lib/format'

type DistributionItem = UsageReportDto['distribution']['items'][number]
type DistributionRow = {
  key: string
  item: UsageDistributionAggregateDto
  identity?: DistributionItem
  rank?: number
  selectable: boolean
}

const props = defineProps<{
  distribution: UsageReportDto['distribution']
  summary: UsageAggregateDto
  groups: GroupOptionDto[]
  channels: ChannelDto[]
}>()

const emit = defineEmits<{ select: [item: DistributionItem] }>()
const { locale, t } = useI18n()

const rows = computed<DistributionRow[]>(() => {
  const items: DistributionRow[] = props.distribution.items.map((item, index) => ({
    key:
      props.distribution.dimension === 'group'
        ? `group:${item.group_id ?? 0}`
        : `model:${item.model ?? ''}`,
    item,
    identity: item,
    rank: index + 1,
    selectable: props.distribution.dimension === 'group' || item.model !== '',
  }))
  if (props.distribution.other !== null) {
    items.push({ key: 'other', item: props.distribution.other, selectable: false })
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

function identityLabel(row: DistributionRow): string {
  if (row.identity === undefined) return t('monitor.usage.distribution.other')
  if (props.distribution.dimension === 'model') {
    return row.identity.model || t('monitor.usage.distribution.unknownModel')
  }
  const groupID = row.identity.group_id ?? 0
  return group(row.identity)?.name ?? t('monitor.usage.filters.deletedOrUnknown', { id: groupID })
}

function identityMeta(row: DistributionRow): string {
  if (row.identity === undefined) return t('monitor.usage.distribution.otherHint')
  if (props.distribution.dimension === 'model') {
    return t('monitor.usage.distribution.modelHint')
  }
  const groupID = row.identity.group_id ?? 0
  const groupChannel = channel(row.identity)
  return groupChannel ? `${groupChannel.name} · Group #${groupID}` : `Group #${groupID}`
}

function metricValue(item: UsageDistributionAggregateDto): number | bigint {
  return props.distribution.metric === 'cost'
    ? BigInt(item.estimated_cost_nano_usd)
    : item.request_count
}

function totalValue(): number | bigint {
  return props.distribution.metric === 'cost'
    ? BigInt(props.summary.estimated_cost_nano_usd)
    : props.summary.request_count
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
  return props.distribution.metric === 'cost'
    ? formatEstimatedCost(item.estimated_cost_nano_usd, locale.value)
    : formatInteger(item.request_count, locale.value)
}

function secondaryValue(item: UsageDistributionAggregateDto): string {
  return props.distribution.metric === 'cost'
    ? t('monitor.usage.distribution.requestsValue', {
        value: formatInteger(item.request_count, locale.value),
      })
    : formatEstimatedCost(item.estimated_cost_nano_usd, locale.value)
}

function select(row: DistributionRow): void {
  if (row.selectable && row.identity !== undefined) emit('select', row.identity)
}
</script>

<template>
  <ol class="usage-distribution" :aria-label="t('monitor.usage.distribution.caption')">
    <li v-for="row in rows" :key="row.key" class="usage-distribution__item">
      <component
        :is="row.selectable ? 'button' : 'div'"
        class="usage-distribution__row"
        :class="{ 'usage-distribution__row--other': row.identity === undefined }"
        :type="row.selectable ? 'button' : undefined"
        :title="row.selectable ? t('monitor.usage.distribution.filterHint') : identityLabel(row)"
        @click="select(row)"
      >
        <span class="usage-distribution__rank" aria-hidden="true">
          {{ row.rank === undefined ? '∑' : String(row.rank).padStart(2, '0') }}
        </span>

        <span class="usage-distribution__identity" :title="identityLabel(row)">
          <span class="usage-distribution__icon" aria-hidden="true">
            <template v-if="row.identity && distribution.dimension === 'group'">
              <ChannelIcon
                v-if="channel(row.identity)"
                :icon="channel(row.identity)!.icon"
                :mark="channel(row.identity)!.mark"
              />
              <Boxes v-else :size="17" />
            </template>
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
        <ChevronRight
          v-if="row.selectable"
          class="usage-distribution__arrow"
          :size="16"
          aria-hidden="true"
        />
      </component>
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
  grid-template-columns: 34px minmax(180px, 0.85fr) minmax(160px, 1.25fr) 116px 64px 22px;
  align-items: center;
  gap: 14px;
  border: 0;
  background: transparent;
  color: inherit;
  padding: 10px 15px;
  text-align: left;
  font: inherit;
}

button.usage-distribution__row {
  cursor: pointer;
  transition: background-color var(--duration-fast) var(--easing-standard);
}

button.usage-distribution__row:hover {
  background: var(--color-surface-sunken);
}

button.usage-distribution__row:focus-visible {
  position: relative;
  z-index: 1;
  outline: 2px solid var(--color-focus);
  outline-offset: -2px;
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

.usage-distribution__arrow {
  color: var(--color-text-faint);
  transition: transform var(--duration-fast) var(--easing-standard);
}

button.usage-distribution__row:hover .usage-distribution__arrow {
  transform: translateX(2px);
  color: var(--color-text-muted);
}

@media (max-width: 820px) {
  .usage-distribution__row {
    grid-template-columns: 30px minmax(0, 1fr) 104px 58px 18px;
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
    grid-template-columns: 26px minmax(0, 1fr) 86px 18px;
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
  .usage-distribution__fill,
  .usage-distribution__arrow {
    transition: none;
  }
}
</style>
