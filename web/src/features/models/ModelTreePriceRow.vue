<script setup lang="ts">
import { Pencil } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ModelCatalogMetadataDto, ModelPriceBranchDto } from '@/app/resources/models'
import { groupDetailLocation } from '@/app/route-locations'
import CopyChip from '@/components/ui/CopyChip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import ModelPriceResetDialog from '@/features/model-prices/ModelPriceResetDialog.vue'
import { presentModelPrice } from '@/features/model-prices/model-price-presenter'

const props = withDefaults(
  defineProps<{
    scope: ModelPriceBranchDto
    clientModel?: string
  }>(),
  { clientModel: undefined },
)
const emit = defineEmits<{
  edit: [scope: ModelPriceBranchDto, trigger: HTMLElement]
}>()
const { n, t } = useI18n()

const presentation = computed(() =>
  presentModelPrice(props.scope.price, {
    unavailable: t('modelPrices.values.unavailable'),
    free: t('modelPrices.values.free'),
    configured: (value) => t('modelPrices.values.configured', { value }),
  }),
)
const catalogMetadata = computed(() => props.scope.catalog_reference?.model ?? null)
const hasDistinctGlobalImpact = computed(() => {
  if (props.scope.route_groups.length !== props.scope.affected_groups.length) return true
  const routeGroupIDs = new Set(props.scope.route_groups.map(({ id }) => id))
  return props.scope.affected_groups.some(({ id }) => !routeGroupIDs.has(id))
})

function metadataFacts(metadata: ModelCatalogMetadataDto): string[] {
  const result: string[] = []
  if (metadata.family) result.push(t('models.tree.metadata.family', { value: metadata.family }))
  if (metadata.status) result.push(t('models.tree.metadata.status', { value: metadata.status }))
  if (metadata.limits.context !== null) {
    result.push(t('models.tree.metadata.context', { value: n(metadata.limits.context) }))
  }
  if (metadata.limits.input !== null) {
    result.push(t('models.tree.metadata.inputLimit', { value: n(metadata.limits.input) }))
  }
  if (metadata.limits.output !== null) {
    result.push(t('models.tree.metadata.outputLimit', { value: n(metadata.limits.output) }))
  }
  if (metadata.modalities.input.length > 0) {
    result.push(
      t('models.tree.metadata.inputModalities', { value: metadata.modalities.input.join(', ') }),
    )
  }
  if (metadata.modalities.output.length > 0) {
    result.push(
      t('models.tree.metadata.outputModalities', { value: metadata.modalities.output.join(', ') }),
    )
  }
  if (metadata.release_date) {
    result.push(t('models.tree.metadata.releaseDate', { value: metadata.release_date }))
  }
  if (metadata.last_updated) {
    result.push(t('models.tree.metadata.lastUpdated', { value: metadata.last_updated }))
  }
  if (metadata.knowledge) {
    result.push(t('models.tree.metadata.knowledge', { value: metadata.knowledge }))
  }
  if (metadata.open_weights === true) result.push(t('models.tree.metadata.openWeights'))
  for (const capability of [
    'attachment',
    'reasoning',
    'tool_call',
    'structured_output',
    'temperature',
  ] as const) {
    if (metadata.capabilities[capability] === true) {
      result.push(t(`models.tree.metadata.capabilities.${capability}`))
    }
  }
  return result
}

function methodLabel(): string {
  const row = props.scope.price
  if (row.method === null) return t('modelPrices.method.pending')
  if (row.method === 'auto_matched') {
    return t('modelPrices.method.auto_matched', { provider: row.matched_provider_id })
  }
  return t(`modelPrices.method.${row.method}`)
}

function edit(trigger: HTMLElement): void {
  emit('edit', props.scope, trigger)
}
</script>

<template>
  <article class="model-tree-price-row" :class="{ 'model-tree-price-row--root': clientModel }">
    <div class="model-tree-price-row__structure">
      <div class="model-tree-price-row__identity">
        <span class="model-tree-price-row__kind">
          {{
            clientModel
              ? t('models.tree.clientModel')
              : t(`models.tree.scope.${scope.price.scope.kind}`)
          }}
        </span>
        <CopyChip
          v-if="clientModel"
          :value="clientModel"
          :label="t('models.tree.copy', { model: clientModel })"
          :success-label="t('models.tree.copySucceeded')"
          :failure-label="t('models.tree.copyFailed')"
        />
        <strong v-else>{{ scope.price.scope.label }}</strong>
        <small v-if="clientModel">
          {{ t(`models.tree.scope.${scope.price.scope.kind}`) }} ·
          {{ scope.price.scope.label }}
        </small>
      </div>

      <div v-if="scope.catalog_reference && catalogMetadata" class="model-tree-price-row__catalog">
        <span class="model-tree-price-row__catalog-source">
          {{
            t(`models.tree.catalogReference.${scope.catalog_reference.source}`, {
              provider: scope.catalog_reference.provider_name,
            })
          }}
          · {{ catalogMetadata.name }}
        </span>
        <p v-if="catalogMetadata.description">{{ catalogMetadata.description }}</p>
        <div v-if="metadataFacts(catalogMetadata).length" class="model-tree-price-row__metadata">
          <span v-for="fact in metadataFacts(catalogMetadata)" :key="fact">{{ fact }}</span>
        </div>
      </div>

      <div class="model-tree-price-row__groups">
        <span>
          {{
            hasDistinctGlobalImpact
              ? t('models.tree.routeGroups')
              : scope.route_groups.length > 1
                ? t('models.tree.groupCount', { count: scope.route_groups.length })
                : t('models.tree.groups')
          }}
        </span>
        <ul
          :class="{
            'model-tree-price-row__group-list--shared': scope.route_groups.length > 1,
          }"
        >
          <li v-for="group in scope.route_groups" :key="group.id">
            <RouterLink :to="groupDetailLocation(group.id)">{{ group.name }}</RouterLink>
            <small>{{ group.provider_id ?? t('models.tree.customProvider') }}</small>
            <small>{{
              group.protocols.map((value) => t(`common.protocols.${value}`)).join(' · ')
            }}</small>
            <span :data-enabled="group.enabled">
              {{ group.enabled ? t('models.tree.groupEnabled') : t('models.tree.groupDisabled') }}
            </span>
          </li>
        </ul>
        <template v-if="hasDistinctGlobalImpact">
          <span class="model-tree-price-row__global-impact">
            {{ t('models.tree.globalImpact', { count: scope.affected_groups.length }) }}
          </span>
          <ul class="model-tree-price-row__group-list--shared">
            <li v-for="group in scope.affected_groups" :key="group.id">
              <RouterLink :to="groupDetailLocation(group.id)">{{ group.name }}</RouterLink>
              <span :data-enabled="group.enabled">
                {{ group.enabled ? t('models.tree.groupEnabled') : t('models.tree.groupDisabled') }}
              </span>
            </li>
          </ul>
        </template>
      </div>
    </div>

    <div class="model-tree-price-row__status">
      <span class="model-tree-price-row__mobile-label">{{ t('models.tree.columns.status') }}</span>
      <StatusBadge
        size="compact"
        :tone="scope.price.pricing_status === 'configured' ? 'success' : 'warning'"
      >
        {{ t(`modelPrices.status.${scope.price.pricing_status}`) }}
      </StatusBadge>
      <small v-if="scope.price.partial">{{ t('modelPrices.facts.partial') }}</small>
      <small v-if="scope.price.has_context_tiers">{{ t('modelPrices.facts.tiered') }}</small>
    </div>

    <div class="model-tree-price-row__prices">
      <span class="model-tree-price-row__mobile-label">{{ t('models.tree.columns.prices') }}</span>
      <div
        v-for="slot in presentation.slots"
        :key="slot.field"
        class="model-tree-price-row__price-slot"
        :data-state="slot.state"
      >
        <span>{{ t(`modelPrices.fields.${slot.field}`) }}</span>
        <strong>{{ slot.value }}</strong>
      </div>
    </div>

    <div class="model-tree-price-row__facts">
      <span class="model-tree-price-row__mobile-label">{{ t('models.tree.columns.facts') }}</span>
      <strong>{{ methodLabel() }}</strong>
      <small v-if="scope.price.method === 'auto_sync'">
        {{ t('modelPrices.reference', { provider: scope.price.matched_provider_id }) }}
      </small>
      <small>
        {{
          t('modelPrices.references', {
            entries: scope.price.reference_count,
            groups: scope.price.reference_group_count,
          })
        }}
      </small>
      <div class="model-tree-price-row__actions">
        <IconButton
          variant="ghost"
          tone="action"
          size="compact"
          :label="t('modelPrices.edit.open', { model: scope.price.model_id })"
          @click="edit($event.currentTarget as HTMLElement)"
        >
          <Pencil :size="15" aria-hidden="true" />
        </IconButton>
        <ModelPriceResetDialog v-if="scope.price.can_reset" :row="scope.price" action="reset" />
      </div>
    </div>
  </article>
</template>

<style scoped>
.model-tree-price-row {
  display: grid;
  min-width: 0;
  grid-template-columns:
    minmax(300px, 1.45fr) minmax(92px, 0.38fr) minmax(300px, 1.2fr)
    minmax(170px, 0.62fr);
  align-items: stretch;
  gap: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
  padding: var(--space-3-5) var(--space-2);
}

.model-tree-price-row--root {
  border-top: 0;
  padding-block: var(--space-4);
}

.model-tree-price-row__structure,
.model-tree-price-row__identity,
.model-tree-price-row__catalog,
.model-tree-price-row__groups,
.model-tree-price-row__status,
.model-tree-price-row__facts {
  display: grid;
  min-width: 0;
  align-content: start;
}

.model-tree-price-row__structure {
  gap: var(--space-2-5);
}

.model-tree-price-row__identity {
  gap: 2px;
}

.model-tree-price-row__kind,
.model-tree-price-row__catalog-source,
.model-tree-price-row__groups > span,
.model-tree-price-row__identity small,
.model-tree-price-row__status small,
.model-tree-price-row__facts small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-tree-price-row__identity > strong {
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-tree-price-row__catalog {
  gap: var(--space-1);
}

.model-tree-price-row__catalog p {
  display: -webkit-box;
  overflow: hidden;
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.model-tree-price-row__metadata {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 6px;
}

.model-tree-price-row__metadata span,
.model-tree-price-row__groups li > span {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  color: var(--color-text-muted);
  padding: 2px 6px;
  font-size: var(--text-label-xs);
}

.model-tree-price-row__groups {
  gap: var(--space-1);
}

.model-tree-price-row__groups > .model-tree-price-row__global-impact {
  margin-top: var(--space-1);
  color: var(--color-warning);
  font-weight: 600;
}

.model-tree-price-row__groups ul {
  display: grid;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.model-tree-price-row__groups li {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 7px;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.model-tree-price-row__groups a {
  color: var(--color-action);
  font-weight: 600;
}

.model-tree-price-row__groups small {
  color: var(--color-text-faint);
}

.model-tree-price-row__groups li > span[data-enabled='false'] {
  color: var(--color-text-faint);
}

.model-tree-price-row__status,
.model-tree-price-row__facts {
  justify-items: start;
  gap: var(--space-1);
}

.model-tree-price-row__prices {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  align-content: start;
  gap: var(--space-2);
}

.model-tree-price-row__price-slot {
  min-width: 0;
  border-left: 1px solid var(--color-border-subtle);
  padding-left: var(--space-2);
}

.model-tree-price-row__price-slot:first-of-type {
  border-left: 0;
  padding-left: 0;
}

.model-tree-price-row__price-slot > span,
.model-tree-price-row__mobile-label {
  display: block;
  overflow: hidden;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-tree-price-row__price-slot strong {
  display: block;
  overflow: hidden;
  margin-top: var(--space-0-5);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-tree-price-row__price-slot[data-state='unavailable'] strong {
  color: var(--color-text-faint);
  font-family: var(--font-sans);
  font-weight: 500;
}

.model-tree-price-row__price-slot[data-state='free'] strong {
  color: var(--color-success);
}

.model-tree-price-row__facts > strong {
  font-size: var(--text-sm);
}

.model-tree-price-row__actions {
  display: flex;
  align-items: center;
  gap: var(--space-0-5);
  margin-top: var(--space-1);
}

.model-tree-price-row__mobile-label {
  display: none;
}

@media (max-width: 1120px) {
  .model-tree-price-row {
    grid-template-columns:
      minmax(260px, 1.3fr) minmax(88px, 0.35fr) minmax(270px, 1.1fr)
      minmax(150px, 0.55fr);
    gap: var(--space-2-5);
  }
}

@media (max-width: 860px) {
  .model-tree-price-row,
  .model-tree-price-row--root {
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--space-3) var(--space-4);
    border-top: 1px solid var(--color-border-subtle);
    padding: var(--space-4) 0 0;
  }

  .model-tree-price-row--root {
    border-top: 0;
    padding-top: 0;
  }

  .model-tree-price-row__structure {
    grid-column: 1;
    grid-row: 1;
  }

  .model-tree-price-row__status {
    grid-column: 2;
    grid-row: 1;
    justify-self: end;
  }

  .model-tree-price-row__prices {
    grid-column: 1 / -1;
    grid-row: 2;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .model-tree-price-row__facts {
    grid-column: 1 / -1;
    grid-row: 3;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: center;
    border-top: 1px solid var(--color-border-subtle);
    padding-top: var(--space-2-5);
  }

  .model-tree-price-row__facts > strong,
  .model-tree-price-row__facts > small {
    grid-column: 1;
  }

  .model-tree-price-row__actions {
    grid-column: 2;
    grid-row: 1 / span 4;
    margin-top: 0;
  }

  .model-tree-price-row__mobile-label {
    display: block;
  }

  .model-tree-price-row__prices > .model-tree-price-row__mobile-label {
    grid-column: 1 / -1;
  }
}
</style>
