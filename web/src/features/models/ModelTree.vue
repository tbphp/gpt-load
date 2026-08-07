<script setup lang="ts">
import { ChevronRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ClientModelDto, ModelUpstreamDto } from '@/app/resources/models'
import CopyChip from '@/components/ui/CopyChip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import { modelPriceFields } from '@/features/model-prices/model-price-form'

import {
  presentClientModel,
  type ClientModelRow,
  type ModelPriceRowStatus,
} from './model-presenter'

const props = defineProps<{
  items: ClientModelDto[]
}>()
const emit = defineEmits<{ open: [upstream: ModelUpstreamDto] }>()
const { t } = useI18n()

const rows = computed<ClientModelRow[]>(() => props.items.map(presentClientModel))

const statusTone = {
  configured: 'success',
  pending: 'warning',
  unpriced: 'neutral',
} as const satisfies Record<ModelPriceRowStatus, 'success' | 'warning' | 'neutral'>
</script>

<template>
  <div class="model-tree">
    <div class="model-tree__scroll">
      <div class="model-tree__grid" role="table" :aria-label="t('models.tree.label')">
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
          <span class="model-tree__cell" role="columnheader">
            <span class="sr-only">{{ t('models.tree.actionColumn') }}</span>
          </span>
        </div>

        <template v-for="row in rows" :key="row.model.client_model">
          <div class="model-tree__row model-tree__row--client" role="row">
            <div class="model-tree__cell model-tree__client" role="cell">
              <span class="model-tree__client-name">{{ row.model.client_model }}</span>
              <CopyChip
                class="model-tree__copy"
                layout="icon"
                :value="row.model.client_model"
                :label="t('models.tree.copy', { model: row.model.client_model })"
                :success-label="t('models.tree.copySucceeded')"
                :failure-label="t('models.tree.copyFailed')"
              />
              <span v-for="protocol in row.model.protocols" :key="protocol" class="model-tree__tag">
                {{ t(`common.protocols.${protocol}`) }}
              </span>
              <span class="model-tree__muted">
                {{ t('models.tree.upstreamCount', { count: row.upstreams.length }) }}
              </span>
              <span v-if="row.pendingCount > 0" class="model-tree__pending">
                {{ t('models.tree.pendingCount', { count: row.pendingCount }) }}
              </span>
            </div>
          </div>

          <div
            v-for="entry in row.upstreams"
            :key="entry.upstream.model_id"
            class="model-tree__row model-tree__row--upstream"
            role="row"
          >
            <div class="model-tree__cell model-tree__upstream" role="cell">
              <button
                type="button"
                class="model-tree__open"
                :aria-label="t('models.tree.open', { model: entry.upstream.model_id })"
                @click="emit('open', entry.upstream)"
              >
                {{ entry.upstream.model_id }}
              </button>
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
              <StatusBadge size="compact" :tone="statusTone[entry.status]">
                {{ t(`modelPrices.status.${entry.status}`) }}
              </StatusBadge>
            </div>

            <div class="model-tree__cell model-tree__cell--action" role="cell">
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

/* 客户端模型行是分组标题：底色区分层级，价格列留空。 */
.model-tree__row--client {
  border-top: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
}

.model-tree__row--client:first-of-type {
  border-top: 0;
}

.model-tree__client {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  grid-column: 1 / -1;
  gap: var(--space-1) var(--space-2-5);
  padding-right: var(--space-3-5);
}

.model-tree__client-name {
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 650;
}

/* 复制紧跟模型名，不参与后面元信息的间距节奏。 */
.model-tree__copy {
  margin-left: calc(var(--space-2-5) * -1 + var(--space-0-5));
}

.model-tree__tag {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding: 1px 6px;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  white-space: nowrap;
}

.model-tree__muted {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  white-space: nowrap;
}

.model-tree__pending {
  color: var(--color-warning);
  font-size: var(--text-label-xs);
  white-space: nowrap;
}

.model-tree__row--upstream {
  border-top: 1px solid var(--color-border-subtle);
  transition: background-color var(--duration-fast) var(--easing-standard);
}

.model-tree__row--upstream:hover {
  background: var(--color-interactive-hover);
}

/* 上游行缩进一级，用竖线把它归到上方的客户端模型下。 */
.model-tree__upstream {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
  margin-left: var(--space-4);
  border-left: 1px solid var(--color-border-subtle);
  padding-left: var(--space-3);
}

.model-tree__open {
  border: 0;
  background: none;
  cursor: pointer;
  padding: 0;
  color: inherit;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  overflow-wrap: anywhere;
  text-align: left;
}

.model-tree__open:hover {
  color: var(--color-action);
  text-decoration: underline;
}

.model-tree__cell--action :deep(.icon-button) {
  color: var(--color-text-faint);
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

  .model-tree__upstream {
    grid-column: 1 / -1;
    margin-left: 0;
    border-left: 0;
    padding-right: var(--control-xs);
    padding-left: 0;
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
