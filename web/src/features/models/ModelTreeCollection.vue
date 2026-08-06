<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ClientModelDto, ModelPriceBranchDto, ModelUpstreamDto } from '@/app/resources/models'
import CopyChip from '@/components/ui/CopyChip.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import ModelTreePriceRow from './ModelTreePriceRow.vue'

const props = defineProps<{
  items: ClientModelDto[]
}>()
const emit = defineEmits<{
  edit: [scope: ModelPriceBranchDto, trigger: HTMLElement]
}>()
const { t } = useI18n()

interface ClientModelPresentation {
  model: ClientModelDto
  scopes: ModelPriceBranchDto[]
  pending: number
  simpleScope: ModelPriceBranchDto | null
  showUpstreamLayer: boolean
}

const presentations = computed<ClientModelPresentation[]>(() =>
  props.items.map((model) => {
    const scopes = model.upstream_models.flatMap(({ prices }) => prices)
    const pending = scopes.filter(({ price }) => price.pricing_status === 'pending').length
    const upstream = model.upstream_models[0]
    const simpleScope =
      model.upstream_models.length === 1 &&
      upstream !== undefined &&
      !upstream.alias_applied &&
      upstream.prices.length === 1
        ? (upstream.prices[0] ?? null)
        : null
    return {
      model,
      scopes,
      pending,
      simpleScope,
      showUpstreamLayer:
        model.upstream_models.length > 1 ||
        model.upstream_models.some(({ alias_applied }) => alias_applied),
    }
  }),
)

function priceSummary(presentation: ClientModelPresentation): string {
  return presentation.pending > 0
    ? t('models.tree.pricesWithPending', {
        count: presentation.scopes.length,
        pending: presentation.pending,
      })
    : t('models.tree.prices', { count: presentation.scopes.length })
}

function upstreamSummary(upstream: ModelUpstreamDto): string {
  const pending = upstream.prices.filter(({ price }) => price.pricing_status === 'pending').length
  return pending > 0
    ? t('models.tree.pricesWithPending', {
        count: upstream.prices.length,
        pending,
      })
    : t('models.tree.prices', { count: upstream.prices.length })
}

function forwardEdit(scope: ModelPriceBranchDto, trigger: HTMLElement): void {
  emit('edit', scope, trigger)
}
</script>

<template>
  <section class="model-tree-collection" :aria-label="t('models.tree.label')">
    <div class="model-tree-collection__header" aria-hidden="true">
      <span>{{ t('models.tree.columns.structure') }}</span>
      <span>{{ t('models.tree.columns.status') }}</span>
      <span>{{ t('models.tree.columns.prices') }}</span>
      <span>{{ t('models.tree.columns.facts') }}</span>
    </div>

    <article
      v-for="presentation in presentations"
      :key="presentation.model.client_model"
      class="model-tree-client"
    >
      <ModelTreePriceRow
        v-if="presentation.simpleScope"
        :scope="presentation.simpleScope"
        :client-model="presentation.model.client_model"
        @edit="forwardEdit"
      />

      <template v-else>
        <header class="model-tree-client__header">
          <div class="model-tree-client__identity">
            <span>{{ t('models.tree.clientModel') }}</span>
            <CopyChip
              :value="presentation.model.client_model"
              :label="t('models.tree.copy', { model: presentation.model.client_model })"
              :success-label="t('models.tree.copySucceeded')"
              :failure-label="t('models.tree.copyFailed')"
            />
          </div>
          <div class="model-tree-client__protocols">
            <span v-for="protocol in presentation.model.protocols" :key="protocol">
              {{ t(`common.protocols.${protocol}`) }}
            </span>
          </div>
          <StatusBadge :tone="presentation.pending > 0 ? 'warning' : 'success'" size="compact">
            {{ priceSummary(presentation) }}
          </StatusBadge>
        </header>

        <div class="model-tree-client__body">
          <section
            v-for="upstream in presentation.model.upstream_models"
            :key="upstream.model_id"
            class="model-tree-upstream"
            :class="{
              'model-tree-upstream--visible': presentation.showUpstreamLayer,
              'model-tree-upstream--direct': !presentation.showUpstreamLayer,
            }"
          >
            <header v-if="presentation.showUpstreamLayer" class="model-tree-upstream__header">
              <div>
                <span>{{ t('models.tree.upstreamModel') }}</span>
                <strong>{{ upstream.model_id }}</strong>
                <small v-if="!upstream.alias_applied">{{ t('models.tree.directModel') }}</small>
              </div>
              <span>{{ upstreamSummary(upstream) }}</span>
            </header>

            <div class="model-tree-upstream__prices">
              <ModelTreePriceRow
                v-for="scope in upstream.prices"
                :key="scope.price.id"
                :scope="scope"
                @edit="forwardEdit"
              />
            </div>
          </section>
        </div>
      </template>
    </article>
  </section>
</template>

<style scoped>
.model-tree-collection {
  min-width: 0;
  border-bottom: 1px solid var(--color-border-control);
}

.model-tree-collection__header {
  display: grid;
  min-height: 38px;
  grid-template-columns:
    minmax(300px, 1.45fr) minmax(92px, 0.38fr) minmax(300px, 1.2fr)
    minmax(170px, 0.62fr);
  align-items: center;
  gap: var(--space-4);
  border-bottom: 1px solid var(--color-border-control);
  color: var(--color-text-faint);
  padding-inline: var(--space-2);
  font-size: var(--text-sm);
  font-weight: 500;
  letter-spacing: 0.04em;
}

.model-tree-client {
  min-width: 0;
  border-bottom: 1px solid var(--color-border-control);
}

.model-tree-client:last-child {
  border-bottom: 0;
}

.model-tree-client__header {
  display: grid;
  min-height: 58px;
  grid-template-columns: minmax(220px, 1fr) minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--space-4);
  background: var(--color-surface-sunken);
  padding: var(--space-2-5) var(--space-2);
}

.model-tree-client__identity {
  display: grid;
  min-width: 0;
  gap: 1px;
}

.model-tree-client__identity > span,
.model-tree-upstream__header span,
.model-tree-upstream__header small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-tree-client__protocols {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.model-tree-client__protocols span {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  color: var(--color-text-muted);
  padding: 3px 7px;
  font-size: var(--text-label-xs);
}

.model-tree-client__body {
  padding-left: var(--space-4);
}

.model-tree-upstream {
  position: relative;
  min-width: 0;
  border-left: 1px solid var(--color-border-control);
}

.model-tree-upstream + .model-tree-upstream {
  border-top: 1px solid var(--color-border-control);
}

.model-tree-upstream--direct {
  border-left: 0;
}

.model-tree-upstream__header {
  position: relative;
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-2) var(--space-2) var(--space-2) var(--space-5);
}

.model-tree-upstream__header::before {
  position: absolute;
  top: 50%;
  left: 0;
  width: var(--space-3);
  border-top: 1px solid var(--color-border-control);
  content: '';
}

.model-tree-upstream__header > div {
  display: grid;
  min-width: 0;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: baseline;
  gap: var(--space-2);
}

.model-tree-upstream__header strong {
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-tree-upstream__header > span {
  flex: none;
}

.model-tree-upstream__prices {
  padding-left: var(--space-4);
}

.model-tree-upstream--direct .model-tree-upstream__prices {
  padding-left: 0;
}

@media (max-width: 1120px) {
  .model-tree-collection__header {
    grid-template-columns:
      minmax(260px, 1.3fr) minmax(88px, 0.35fr) minmax(270px, 1.1fr)
      minmax(150px, 0.55fr);
    gap: var(--space-2-5);
  }
}

@media (max-width: 860px) {
  .model-tree-collection {
    display: grid;
    gap: var(--space-3);
    border-bottom: 0;
  }

  .model-tree-collection__header {
    display: none;
  }

  .model-tree-client {
    overflow: hidden;
    border: 1px solid var(--color-border-subtle);
    border-radius: var(--radius-control);
    background: var(--color-surface);
    padding: var(--space-4);
  }

  .model-tree-client:last-child {
    border-bottom: 1px solid var(--color-border-subtle);
  }

  .model-tree-client__header {
    min-height: 0;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--space-2) var(--space-3);
    background: transparent;
    padding: 0 0 var(--space-3);
  }

  .model-tree-client__protocols {
    grid-column: 1 / -1;
    grid-row: 2;
  }

  .model-tree-client__body {
    padding-left: 0;
  }

  .model-tree-upstream,
  .model-tree-upstream--direct {
    border-left: 0;
  }

  .model-tree-upstream + .model-tree-upstream {
    margin-top: var(--space-3);
    padding-top: var(--space-3);
  }

  .model-tree-upstream__header {
    min-height: 0;
    align-items: flex-start;
    flex-direction: column;
    gap: var(--space-1);
    padding: var(--space-2-5) 0;
  }

  .model-tree-upstream__header::before {
    display: none;
  }

  .model-tree-upstream__header > div {
    grid-template-columns: auto minmax(0, 1fr);
  }

  .model-tree-upstream__header small {
    grid-column: 2;
  }

  .model-tree-upstream__prices,
  .model-tree-upstream--direct .model-tree-upstream__prices {
    padding-left: var(--space-3);
    border-left: 1px solid var(--color-border-control);
  }
}
</style>
