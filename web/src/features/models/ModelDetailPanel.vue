<script setup lang="ts">
import { Pencil } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { ModelPriceBranchDto, ModelUpstreamDto } from '@/app/resources/models'
import { groupDetailLocation } from '@/app/route-locations'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import ModelPriceResetDialog from '@/features/model-prices/ModelPriceResetDialog.vue'
import { presentModelPrice } from '@/features/model-prices/model-price-presenter'

import ModelCatalogFacts from './ModelCatalogFacts.vue'
import { hasWiderPriceImpact, type ClientModelPresentation } from './model-presenter'

const props = defineProps<{
  presentation: ClientModelPresentation
}>()
const emit = defineEmits<{
  edit: [branch: ModelPriceBranchDto, trigger: HTMLElement]
}>()
const { t } = useI18n()

function slots(branch: ModelPriceBranchDto) {
  return presentModelPrice(branch.price, {
    unavailable: t('modelPrices.values.unavailable'),
    free: t('modelPrices.values.free'),
    configured: (value) => t('modelPrices.values.configured', { value }),
  }).slots
}

function methodLabel(branch: ModelPriceBranchDto): string {
  const { method, matched_provider_id: provider } = branch.price
  if (method === null) return t('modelPrices.method.pending')
  if (method === 'auto_matched') return t('modelPrices.method.auto_matched', { provider })
  return t(`modelPrices.method.${method}`)
}

function showUpstreamHeader(upstream: ModelUpstreamDto): boolean {
  return props.presentation.upstream.shape !== 'direct' || upstream.alias_applied
}

function edit(branch: ModelPriceBranchDto, trigger: HTMLElement): void {
  emit('edit', branch, trigger)
}
</script>

<template>
  <div class="model-detail">
    <section
      v-for="upstream in presentation.model.upstream_models"
      :key="upstream.model_id"
      class="model-detail__upstream"
    >
      <header v-if="showUpstreamHeader(upstream)" class="model-detail__upstream-head">
        <span class="model-detail__eyebrow">{{ t('models.detail.upstreamModel') }}</span>
        <strong>{{ upstream.model_id }}</strong>
        <span class="model-detail__chip">
          {{ upstream.alias_applied ? t('models.detail.aliasApplied') : t('models.detail.direct') }}
        </span>
      </header>

      <ModelCatalogFacts v-if="upstream.catalog_summary" :reference="upstream.catalog_summary" />

      <article v-for="branch in upstream.prices" :key="branch.price.id" class="model-detail__scope">
        <div class="model-detail__scope-head">
          <div class="model-detail__scope-identity">
            <span class="model-detail__eyebrow">
              {{ t(`models.detail.scope.${branch.price.scope.kind}`) }}
            </span>
            <strong>{{ branch.price.scope.label }}</strong>
          </div>
          <StatusBadge
            size="compact"
            :tone="branch.price.pricing_status === 'configured' ? 'success' : 'warning'"
          >
            {{ t(`modelPrices.status.${branch.price.pricing_status}`) }}
          </StatusBadge>
          <div class="model-detail__scope-actions">
            <IconButton
              variant="ghost"
              tone="action"
              size="compact"
              :label="t('modelPrices.edit.open', { model: branch.price.model_id })"
              @click="edit(branch, $event.currentTarget as HTMLElement)"
            >
              <Pencil :size="15" aria-hidden="true" />
            </IconButton>
            <ModelPriceResetDialog
              v-if="branch.price.can_reset"
              :row="branch.price"
              action="reset"
            />
          </div>
        </div>

        <ModelCatalogFacts
          v-if="!upstream.catalog_summary && branch.catalog_reference"
          :reference="branch.catalog_reference"
        />

        <dl class="model-detail__slots">
          <div v-for="slot in slots(branch)" :key="slot.field" :data-state="slot.state">
            <dt>{{ t(`modelPrices.fields.${slot.field}`) }}</dt>
            <dd>{{ slot.value }}</dd>
          </div>
        </dl>

        <p class="model-detail__facts">
          <span>{{ methodLabel(branch) }}</span>
          <span>
            {{
              t('modelPrices.references', {
                entries: branch.price.reference_count,
                groups: branch.price.reference_group_count,
              })
            }}
          </span>
          <span v-if="branch.price.partial" class="model-detail__facts-warning">
            {{ t('modelPrices.facts.partial') }}
          </span>
          <span v-if="branch.price.has_context_tiers">{{ t('modelPrices.facts.tiered') }}</span>
        </p>

        <div class="model-detail__groups">
          <span class="model-detail__eyebrow">{{ t('models.detail.routeGroups') }}</span>
          <ul>
            <li v-for="group in branch.route_groups" :key="group.id">
              <RouterLink :to="groupDetailLocation(group.id)">{{ group.name }}</RouterLink>
              <span v-if="!group.enabled">{{ t('models.detail.groupDisabled') }}</span>
            </li>
          </ul>
          <span v-if="hasWiderPriceImpact(branch)" class="model-detail__impact">
            {{ t('models.detail.globalImpact', { count: branch.affected_groups.length }) }}
          </span>
        </div>
      </article>
    </section>
  </div>
</template>

<style scoped>
.model-detail {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}

.model-detail__upstream {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}

.model-detail__upstream-head {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
}

.model-detail__upstream-head strong {
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  overflow-wrap: anywhere;
}

.model-detail__eyebrow {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  letter-spacing: 0.04em;
}

.model-detail__chip {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  color: var(--color-text-muted);
  padding: 2px 7px;
  font-size: var(--text-label-xs);
}

.model-detail__scope {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: var(--space-3-5) var(--space-4);
}

.model-detail__scope-head {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--space-3);
}

.model-detail__scope-identity {
  display: grid;
  min-width: 0;
  gap: 1px;
}

.model-detail__scope-identity strong {
  overflow: hidden;
  font-size: var(--text-meta);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-detail__scope-actions {
  display: flex;
  align-items: center;
  gap: var(--space-0-5);
}

.model-detail__slots {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-2);
  margin: 0;
  border-block: 1px solid var(--color-border-subtle);
  padding-block: var(--space-2-5);
}

.model-detail__slots div {
  min-width: 0;
  border-left: 1px solid var(--color-border-subtle);
  padding-left: var(--space-2-5);
}

.model-detail__slots div:first-child {
  border-left: 0;
  padding-left: 0;
}

.model-detail__slots dt {
  overflow: hidden;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-detail__slots dd {
  margin: var(--space-0-5) 0 0;
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-detail__slots div[data-state='unavailable'] dd {
  color: var(--color-text-faint);
  font-family: var(--font-sans);
  font-weight: 500;
}

.model-detail__slots div[data-state='free'] dd {
  color: var(--color-success);
}

.model-detail__facts {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2-5);
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.model-detail__facts span + span::before {
  margin-right: var(--space-2-5);
  color: var(--color-text-faint);
  content: '·';
}

.model-detail__facts-warning {
  color: var(--color-warning);
}

.model-detail__groups {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
  min-width: 0;
}

.model-detail__groups ul {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.model-detail__groups li {
  display: inline-flex;
  align-items: baseline;
  gap: var(--space-1);
  font-size: var(--text-sm);
}

.model-detail__groups a {
  color: var(--color-action);
  font-weight: 600;
}

.model-detail__groups li span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-detail__impact {
  color: var(--color-warning);
  font-size: var(--text-label-xs);
  font-weight: 600;
}

@media (max-width: 720px) {
  .model-detail__scope {
    padding: var(--space-3) var(--space-3);
  }

  .model-detail__scope-head {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .model-detail__scope-actions {
    grid-column: 2;
    grid-row: 1;
  }

  .model-detail__scope-head :deep(.status-badge) {
    grid-column: 1 / -1;
    grid-row: 2;
    justify-self: start;
  }

  .model-detail__slots {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-2-5) var(--space-2);
  }

  .model-detail__slots div:nth-child(3) {
    border-left: 0;
    padding-left: 0;
  }
}
</style>
