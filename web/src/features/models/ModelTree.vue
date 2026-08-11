<script setup lang="ts">
import { ChevronRight, CircleHelp } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { enabledDataProtocols } from '@/api/control/protocols'
import type { GroupProtocol } from '@/api/control/types'
import type { ClientModelDto, ModelUpstreamDto } from '@/app/resources/models'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import CopyChip from '@/components/ui/CopyChip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { modelPriceFields } from '@/features/model-prices/model-price-form'

import ModelPriceStatusBadge from './ModelPriceStatusBadge.vue'
import { presentClientModel, type ClientModelRow } from './model-presenter'

const props = defineProps<{
  items: ClientModelDto[]
  readOnly?: boolean
}>()
const emit = defineEmits<{ open: [upstream: ModelUpstreamDto] }>()
const { t } = useI18n()

const rows = computed<ClientModelRow[]>(() => props.items.map(presentClientModel))

function hasProtocolRestriction(protocols: GroupProtocol[]): boolean {
  return props.readOnly === true && protocols.length < enabledDataProtocols.length
}

function protocolRestrictionTooltip(protocols: GroupProtocol[]): string {
  return t('models.tree.protocolRestrictedHelp', {
    protocols: protocols.join('\n'),
  })
}

function pricingIdentityTooltip(upstream: ModelUpstreamDto): string {
  return t('models.tree.pricingIdentityHelp', {
    channel: upstream.price.channel_name,
    channelId: upstream.price.channel_id,
    model: upstream.model_id,
  })
}
</script>

<template>
  <div class="model-tree">
    <div class="model-tree__scroll">
      <div
        class="model-tree__grid"
        :class="{ 'model-tree__grid--read-only': readOnly }"
        role="table"
        :aria-label="t('models.tree.label')"
      >
        <div class="model-tree__row model-tree__row--head" role="row">
          <span class="model-tree__cell" role="columnheader">
            {{ t('models.tree.modelColumn') }}
          </span>
          <span
            v-for="field in modelPriceFields"
            :key="field"
            class="model-tree__cell model-tree__cell--price"
            role="columnheader"
          >
            {{ t(`modelPrices.fields.${field}`) }}
          </span>
          <span class="model-tree__cell model-tree__cell--status" role="columnheader">
            {{ t('models.tree.statusColumn') }}
          </span>
          <span v-if="!readOnly" class="model-tree__cell" role="columnheader">
            <span class="sr-only">{{ t('models.tree.actionColumn') }}</span>
          </span>
        </div>

        <template v-for="row in rows" :key="row.model.client_model">
          <div class="model-tree__row model-tree__row--client" role="row">
            <div class="model-tree__cell model-tree__client" role="cell">
              <span class="model-tree__ident">
                <span class="model-tree__client-name">{{ row.model.client_model }}</span>
                <CopyChip
                  class="model-tree__copy"
                  layout="icon"
                  :value="row.model.client_model"
                  :label="t('models.tree.copy', { model: row.model.client_model })"
                  :success-label="t('models.tree.copySucceeded')"
                  :failure-label="t('models.tree.copyFailed')"
                />
              </span>
              <span class="model-tree__tag">
                {{ t('models.tree.upstreamCount', { count: row.upstreams.length }) }}
              </span>
              <AppTooltip
                v-if="hasProtocolRestriction(row.model.protocols)"
                :content="protocolRestrictionTooltip(row.model.protocols)"
                align="start"
              >
                <button
                  type="button"
                  class="model-tree__protocol-restriction"
                  :aria-label="protocolRestrictionTooltip(row.model.protocols)"
                >
                  <CircleHelp :size="13" aria-hidden="true" />
                  {{ t('models.tree.protocolRestricted') }}
                </button>
              </AppTooltip>
            </div>
          </div>

          <div
            v-for="(entry, index) in row.upstreams"
            :key="entry.upstream.price.id"
            class="model-tree__row model-tree__row--upstream"
            :class="{ 'model-tree__row--last': index === row.upstreams.length - 1 }"
            role="row"
          >
            <div class="model-tree__cell model-tree__upstream" role="cell">
              <span class="model-tree__ident">
                <button
                  v-if="!readOnly"
                  type="button"
                  class="model-tree__open"
                  :aria-label="t('models.tree.open', { model: entry.upstream.model_id })"
                  @click="emit('open', entry.upstream)"
                >
                  {{ entry.upstream.model_id }}
                </button>
                <span v-else class="model-tree__upstream-name">
                  {{ entry.upstream.model_id }}
                </span>
                <CopyChip
                  class="model-tree__copy"
                  layout="icon"
                  :value="entry.upstream.model_id"
                  :label="t('models.tree.copyUpstream', { model: entry.upstream.model_id })"
                  :success-label="t('models.tree.copySucceeded')"
                  :failure-label="t('models.tree.copyFailed')"
                />
              </span>
              <AppTooltip :content="pricingIdentityTooltip(entry.upstream)" align="start">
                <span
                  class="model-tree__channel"
                  tabindex="0"
                  :aria-label="pricingIdentityTooltip(entry.upstream)"
                >
                  {{ entry.upstream.price.channel_name }}
                </span>
              </AppTooltip>
              <span v-if="entry.tierCount > 0" class="model-tree__tag">
                {{ t('models.tree.tierCount', { count: entry.tierCount }) }}
              </span>
            </div>

            <div
              v-for="field in modelPriceFields"
              :key="field"
              class="model-tree__cell model-tree__cell--price"
              role="cell"
            >
              <span class="model-tree__price-label" aria-hidden="true">
                {{ t(`modelPrices.fields.${field}`) }}
              </span>
              <span
                class="model-tree__price"
                :class="{ 'model-tree__price--empty': entry.prices[field] === null }"
              >
                {{ entry.prices[field] ?? t('models.tree.noPrice') }}
              </span>
            </div>

            <div class="model-tree__cell model-tree__cell--status" role="cell">
              <ModelPriceStatusBadge
                :price="entry.upstream.price"
                :provider-name="entry.upstream.catalog_reference?.provider_name"
              />
            </div>

            <div v-if="!readOnly" class="model-tree__cell model-tree__cell--action" role="cell">
              <IconButton
                variant="ghost"
                size="xs"
                :label="t('models.tree.open', { model: entry.upstream.model_id })"
                @click="emit('open', entry.upstream)"
              >
                <ChevronRight :size="17" aria-hidden="true" />
              </IconButton>
            </div>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.model-tree {
  /* 树线锚点：客户端模型行的主干与上游行的转角共用同一条竖线位置。 */
  --model-tree-rail: 20px;
  min-width: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}

.model-tree__scroll {
  overflow-x: auto;
  border-radius: inherit;
}

.model-tree__grid {
  display: grid;
  /* 名称吸收剩余空间；4 个价格列等宽，状态与箭头列按内容。 */
  min-width: 760px;
  grid-template-columns:
    minmax(220px, 1fr)
    repeat(4, minmax(78px, 104px))
    auto
    var(--control-xs);
}

.model-tree__grid--read-only {
  grid-template-columns:
    minmax(220px, 1fr)
    repeat(4, minmax(78px, 104px))
    auto;
}

.model-tree__row {
  display: grid;
  grid-column: 1 / -1;
  grid-template-columns: subgrid;
  align-items: center;
  column-gap: var(--space-3);
}

.model-tree__cell {
  min-width: 0;
  padding-block: var(--space-1-75);
}

.model-tree__cell:first-child {
  padding-left: var(--space-3-5);
}

.model-tree__cell:last-child {
  padding-right: var(--space-2-5);
}

.model-tree__cell--price,
.model-tree__cell--status {
  justify-self: end;
  text-align: right;
}

.model-tree__cell--action {
  justify-self: end;
}

/* 表头：小号、faint，作为列语义的唯一来源。 */
.model-tree__row--head {
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  letter-spacing: 0.03em;
}

.model-tree__row--head .model-tree__cell {
  padding-block: var(--space-2);
}

/* 客户端模型行是分组标题：底色比表头浅一级、比数据行深一级，价格列留空。 */
.model-tree__row--client {
  border-top: 1px solid var(--color-border-control);
  background: color-mix(in srgb, var(--color-surface-sunken) 55%, var(--color-surface));
}

.model-tree__row--head + .model-tree__row--client {
  border-top: 0;
}

.model-tree__client {
  position: relative;
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  grid-column: 1 / -1;
  gap: var(--space-1) var(--space-2-5);
  padding-right: var(--space-3-5);
}

/* 树干从组标题行内长出，延伸到行底，交给下方第一个上游行接续转角。 */
.model-tree__client::after {
  position: absolute;
  top: 62%;
  bottom: 0;
  left: var(--model-tree-rail);
  width: 1px;
  background: var(--color-border-control);
  content: '';
}

.model-tree__client-name {
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: var(--text-body);
  font-weight: 600;
}

/* 名字与复制键自成一组：CopyChip 自带命中区留白，这里只需极小的视觉间距。 */
.model-tree__ident {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-0-5);
}

.model-tree__tag {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding: 1px 6px;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  white-space: nowrap;
}

.model-tree__protocol-restriction {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  border: 0;
  border-radius: var(--radius-tag);
  background: var(--color-warning-bg);
  cursor: help;
  padding: 1px 6px;
  color: var(--color-text-muted);
  font: inherit;
  font-size: var(--text-label-xs);
}

.model-tree__protocol-restriction svg {
  color: var(--color-warning);
}

.model-tree__channel {
  display: inline-block;
  max-width: 180px;
  overflow: hidden;
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding: 1px 6px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  outline: none;
  text-overflow: ellipsis;
  vertical-align: middle;
  white-space: nowrap;
}

.model-tree__channel:focus-visible,
.model-tree__protocol-restriction:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 1px;
}

.model-tree__row--upstream {
  transition: background-color var(--duration-fast) var(--easing-standard);
}

/* 组内子行之间保留分隔线；组标题与首个子行贴合，靠底色过渡分组。 */
.model-tree__row--upstream + .model-tree__row--upstream {
  border-top: 1px solid var(--color-border-subtle);
}

.model-tree__row--upstream:hover {
  background: var(--color-interactive-hover);
}

/* 上游行缩进一级，用连续竖线把它归到上方的客户端模型下；
   用 .model-tree__cell 叠加类名提高特异性，否则会被 :first-child 的 padding-left 盖掉。 */
.model-tree__cell.model-tree__upstream {
  position: relative;
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
  padding-left: calc(var(--model-tree-rail) + var(--space-4));
}

/* 中间子行：├ ——竖线跨过行边框保持连续，再接一段横向短线。 */
.model-tree__row--upstream:not(.model-tree__row--last)
  .model-tree__cell.model-tree__upstream::before {
  position: absolute;
  top: -1px;
  bottom: -1px;
  left: var(--model-tree-rail);
  width: 1px;
  background: var(--color-border-control);
  content: '';
}

.model-tree__row--upstream:not(.model-tree__row--last)
  .model-tree__cell.model-tree__upstream::after {
  position: absolute;
  top: 50%;
  left: var(--model-tree-rail);
  width: 9px;
  height: 1px;
  background: var(--color-border-control);
  content: '';
}

/* 末个子行：└ ——圆角收笔，一个伪元素画完竖线转横线。 */
.model-tree__row--last .model-tree__cell.model-tree__upstream::before {
  position: absolute;
  top: -1px;
  bottom: 50%;
  left: var(--model-tree-rail);
  width: 9px;
  border-bottom-left-radius: 5px;
  border-left: 1px solid var(--color-border-control);
  border-bottom: 1px solid var(--color-border-control);
  content: '';
}

.model-tree__open {
  border: 0;
  background: none;
  cursor: pointer;
  padding: 0;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  overflow-wrap: anywhere;
  text-align: left;
}

.model-tree__upstream-name {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  overflow-wrap: anywhere;
}

.model-tree__open:hover {
  color: var(--color-action);
  text-decoration: underline;
}

.model-tree__cell--action :deep(.icon-button) {
  color: var(--color-border-control);
}

.model-tree__row--upstream:hover .model-tree__cell--action :deep(.icon-button) {
  color: var(--color-action);
}

.model-tree__price {
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-variant-numeric: tabular-nums;
}

.model-tree__price--empty {
  color: var(--color-text-faint);
}

/* 列标签只在窄屏卡片布局里出现，宽屏由表头承担。 */
.model-tree__price-label {
  display: none;
}

@media (max-width: 860px) {
  .model-tree {
    border: 0;
    background: none;
  }

  .model-tree__scroll {
    overflow-x: visible;
  }

  .model-tree__grid {
    min-width: 0;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-2);
  }

  .model-tree__row--head {
    display: none;
  }

  .model-tree__row {
    grid-column: 1;
    grid-template-columns: minmax(0, 1fr);
    column-gap: 0;
  }

  .model-tree__row--client {
    border: 0;
    border-radius: var(--radius-control);
    padding: var(--space-1);
  }

  /* 卡片布局没有树形结构，树干线不出现。 */
  .model-tree__client::after {
    display: none;
  }

  .model-tree__cell:first-child,
  .model-tree__cell:last-child {
    padding-inline: var(--space-2-5);
  }

  /* 卡片布局：价格铺成 2×2，箭头脱离网格钉在右上角，避免占掉一整列。 */
  .model-tree__row--upstream {
    position: relative;
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    align-items: start;
    gap: var(--space-2) var(--space-3);
    border: 1px solid var(--color-border-subtle);
    border-radius: var(--radius-control);
    background: var(--color-surface);
    padding: var(--space-3);
  }

  /* 用 .model-tree__cell 叠加类名匹配宽屏规则的特异性，否则宽屏的缩进会盖过这里的重置。 */
  .model-tree__cell.model-tree__upstream {
    grid-column: 1 / -1;
    padding-right: var(--control-xs);
    padding-left: 0;
  }

  .model-tree__cell.model-tree__upstream::before,
  .model-tree__cell.model-tree__upstream::after {
    display: none;
  }

  .model-tree__cell--action {
    position: absolute;
    top: var(--space-2);
    right: var(--space-2);
    padding: 0;
  }

  .model-tree__cell--status {
    grid-column: 1 / -1;
    justify-self: start;
    padding: 0;
  }

  .model-tree__cell--price {
    display: grid;
    justify-self: stretch;
    gap: 1px;
    padding: 0;
    text-align: left;
  }

  .model-tree__price-label {
    display: block;
    color: var(--color-text-faint);
    font-size: var(--text-label-xs);
  }
}
</style>
