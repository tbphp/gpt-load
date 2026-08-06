<script setup lang="ts">
import { ChevronDown, Pencil } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ClientModelDto, ModelPriceBranchDto } from '@/app/resources/models'
import { groupDetailLocation } from '@/app/route-locations'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import ModelDetailPanel from './ModelDetailPanel.vue'
import {
  presentClientModel,
  type ClientModelPresentation,
  type ModelPriceFieldSummary,
} from './model-presenter'

const visibleGroupCount = 3

const props = defineProps<{
  items: ClientModelDto[]
}>()
const emit = defineEmits<{
  edit: [branch: ModelPriceBranchDto, trigger: HTMLElement]
}>()
const { t } = useI18n()

const expanded = ref(new Set<string>())
const presentations = computed<ClientModelPresentation[]>(() =>
  props.items.map((model) => presentClientModel(model)),
)

watch(
  () => props.items,
  (items) => {
    const available = new Set(items.map(({ client_model: model }) => model))
    expanded.value = new Set([...expanded.value].filter((model) => available.has(model)))
  },
)

function toggle(clientModel: string): void {
  const next = new Set(expanded.value)
  if (!next.delete(clientModel)) next.add(clientModel)
  expanded.value = next
}

function upstreamCaption(presentation: ClientModelPresentation): string {
  const { shape, modelID, count } = presentation.upstream
  if (shape === 'multi') return t('models.collection.multiUpstream', { count })
  if (shape === 'alias') return t('models.collection.alias', { model: modelID })
  return t('models.collection.direct')
}

function priceText(summary: ModelPriceFieldSummary): string {
  if (summary.state === 'unavailable') return t('modelPrices.values.unavailable')
  if (summary.state === 'free') return t('modelPrices.values.free')
  const value = t('modelPrices.values.configured', { value: summary.value })
  if (summary.state === 'single') return value
  return t('models.collection.priceRange', {
    min: value,
    max: t('modelPrices.values.configured', { value: summary.upper }),
  })
}

function scopeCaption(presentation: ClientModelPresentation): string | null {
  if (presentation.scopes.length < 2) return null
  return presentation.pendingCount > 0
    ? t('models.collection.pendingScopes', {
        pending: presentation.pendingCount,
        count: presentation.scopes.length,
      })
    : t('models.collection.scopeCount', { count: presentation.scopes.length })
}

function forwardEdit(branch: ModelPriceBranchDto, trigger: HTMLElement): void {
  emit('edit', branch, trigger)
}
</script>

<template>
  <LedgerRecordList
    :label="t('models.collection.label')"
    grid-class="models-record-grid"
    :scroll-hint="t('models.collection.scrollHint')"
  >
    <template #header>
      <span role="columnheader">{{ t('models.collection.columns.model') }}</span>
      <span role="columnheader">{{ t('models.collection.columns.routing') }}</span>
      <span role="columnheader">{{ t('models.collection.columns.prices') }}</span>
      <span role="columnheader">{{ t('models.collection.columns.status') }}</span>
      <span role="columnheader">
        <span class="sr-only">{{ t('models.collection.columns.actions') }}</span>
      </span>
    </template>

    <template v-for="presentation in presentations" :key="presentation.model.client_model">
      <article
        class="ledger-record-list__record model-record"
        :class="{ 'model-record--expanded': expanded.has(presentation.model.client_model) }"
        role="row"
      >
        <div class="ledger-record-list__cell model-record__identity" role="cell">
          <div class="model-record__name">
            <strong>{{ presentation.model.client_model }}</strong>
            <CopyButton
              class="model-record__copy"
              :value="presentation.model.client_model"
              :label="t('models.collection.copy', { model: presentation.model.client_model })"
              :success-label="t('models.collection.copySucceeded')"
              :failure-label="t('models.collection.copyFailed')"
            />
          </div>
          <span class="model-record__caption">{{ upstreamCaption(presentation) }}</span>
        </div>

        <div class="ledger-record-list__cell model-record__routing" role="cell">
          <ul class="model-record__groups">
            <li
              v-for="group in presentation.routeGroups.slice(0, visibleGroupCount)"
              :key="group.id"
            >
              <RouterLink :to="groupDetailLocation(group.id)">{{ group.name }}</RouterLink>
            </li>
            <li
              v-if="presentation.routeGroups.length > visibleGroupCount"
              class="model-record__groups-more"
            >
              {{
                t('models.collection.moreGroups', {
                  count: presentation.routeGroups.length - visibleGroupCount,
                })
              }}
            </li>
          </ul>
          <ul class="model-record__protocols">
            <li v-for="protocol in presentation.model.protocols" :key="protocol">
              {{ t(`common.protocols.${protocol}`) }}
            </li>
          </ul>
        </div>

        <div class="ledger-record-list__cell model-record__prices" role="cell">
          <div v-for="summary in [presentation.input, presentation.output]" :key="summary.field">
            <span>{{ t(`modelPrices.fields.${summary.field}`) }}</span>
            <strong :data-state="summary.state" :title="priceText(summary)">
              {{ priceText(summary) }}
            </strong>
          </div>
        </div>

        <div class="ledger-record-list__cell model-record__status" role="cell">
          <StatusBadge
            size="compact"
            :tone="presentation.status === 'configured' ? 'success' : 'warning'"
          >
            {{ t(`modelPrices.status.${presentation.status}`) }}
          </StatusBadge>
          <small v-if="scopeCaption(presentation)">{{ scopeCaption(presentation) }}</small>
        </div>

        <div class="ledger-record-list__cell model-record__actions" role="cell">
          <IconButton
            v-if="presentation.soleBranch"
            variant="ghost"
            tone="action"
            size="compact"
            :label="t('modelPrices.edit.open', { model: presentation.soleBranch.price.model_id })"
            @click="forwardEdit(presentation.soleBranch, $event.currentTarget as HTMLElement)"
          >
            <Pencil :size="15" aria-hidden="true" />
          </IconButton>
          <IconButton
            class="model-record__toggle"
            variant="ghost"
            size="compact"
            :pressed="expanded.has(presentation.model.client_model)"
            :label="
              expanded.has(presentation.model.client_model)
                ? t('models.collection.collapse', { model: presentation.model.client_model })
                : t('models.collection.expand', { model: presentation.model.client_model })
            "
            @click="toggle(presentation.model.client_model)"
          >
            <ChevronDown :size="16" aria-hidden="true" />
          </IconButton>
        </div>
      </article>

      <div
        v-if="expanded.has(presentation.model.client_model)"
        class="model-record__detail"
        role="row"
      >
        <div class="model-record__detail-cell" role="cell">
          <ModelDetailPanel :presentation="presentation" @edit="forwardEdit" />
        </div>
      </div>
    </template>
  </LedgerRecordList>
</template>

<style scoped>
.models-record-grid {
  --ledger-record-list-grid: minmax(210px, 1.5fr) minmax(180px, 1.35fr) minmax(190px, 1.15fr)
    minmax(112px, 132px) 84px;
  --ledger-record-list-column-gap: var(--space-4);
  --ledger-record-list-record-min-height: 64px;
  --ledger-record-list-record-padding: var(--space-2-5) 0;
}

.model-record--expanded {
  background: var(--color-surface-sunken);
}

.model-record__identity {
  display: grid;
  align-content: center;
  gap: 2px;
}

.model-record__name {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-1);
}

.model-record__name strong {
  min-width: 0;
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-record__copy {
  flex: none;
  opacity: 0;
  transition: opacity var(--duration-fast) var(--easing-standard);
}

.model-record:hover .model-record__copy,
.model-record__copy:focus-within {
  opacity: 1;
}

.model-record__caption {
  overflow: hidden;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-record__routing {
  display: grid;
  align-content: center;
  gap: var(--space-1);
}

.model-record__groups,
.model-record__protocols {
  display: flex;
  min-width: 0;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 2px var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.model-record__groups {
  font-size: var(--text-sm);
}

.model-record__groups a {
  color: var(--color-action);
  font-weight: 600;
}

.model-record__groups-more {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-record__protocols {
  gap: 2px var(--space-1);
}

.model-record__protocols li {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  color: var(--color-text-faint);
  padding: 1px 6px;
  font-size: var(--text-label-xs);
  white-space: nowrap;
}

.model-record__prices {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-content: center;
  gap: var(--space-2);
}

.model-record__prices > div {
  min-width: 0;
  border-left: 1px solid var(--color-border-subtle);
  padding-left: var(--space-2-5);
}

.model-record__prices > div:first-child {
  border-left: 0;
  padding-left: 0;
}

.model-record__prices span {
  display: block;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-record__prices strong {
  display: block;
  overflow: hidden;
  margin-top: 1px;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-record__prices strong[data-state='unavailable'] {
  color: var(--color-text-faint);
  font-family: var(--font-sans);
  font-weight: 500;
}

.model-record__prices strong[data-state='free'] {
  color: var(--color-success);
}

.model-record__status {
  display: grid;
  justify-items: start;
  align-content: center;
  gap: 3px;
}

.model-record__status small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-record__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-0-5);
}

.model-record__toggle :deep(svg) {
  transition: transform var(--duration-fast) var(--easing-standard);
}

.model-record__toggle[aria-pressed='true'] :deep(svg) {
  transform: rotate(180deg);
}

.model-record__detail {
  grid-column: 1 / -1;
  background: var(--color-surface-sunken);
}

.model-record__detail-cell {
  min-width: 0;
  border-top: 1px dashed var(--color-border-control);
  padding: var(--space-4) 0 var(--space-5);
  animation: model-record-detail var(--duration-normal) var(--easing-data);
}

@keyframes model-record-detail {
  from {
    opacity: 0;
    transform: translateY(-3px);
  }
}

@media (prefers-reduced-motion: reduce) {
  .model-record__detail-cell {
    animation: none;
  }
}

@media (max-width: 1120px) {
  .models-record-grid {
    --ledger-record-list-grid: minmax(180px, 1.4fr) minmax(150px, 1.2fr) minmax(170px, 1.1fr)
      minmax(104px, 120px) 76px;
    --ledger-record-list-column-gap: var(--space-3);
  }
}

@media (max-width: 860px) {
  .models-record-grid {
    --ledger-record-list-card-grid: minmax(0, 1fr) auto;
  }

  .model-record__identity {
    grid-column: 1;
    grid-row: 1;
  }

  .model-record__actions {
    grid-column: 2;
    grid-row: 1;
  }

  .model-record__copy {
    opacity: 1;
  }

  .model-record__routing {
    grid-column: 1 / -1;
    grid-row: 2;
  }

  .model-record__prices {
    grid-column: 1 / -1;
    grid-row: 3;
    border-top: 1px solid var(--color-border-subtle);
    padding-top: var(--space-3);
  }

  .model-record__status {
    grid-column: 1 / -1;
    grid-row: 4;
  }

  .model-record__detail {
    border: 1px solid var(--color-border-subtle);
    border-radius: var(--radius-control);
    padding: 0 var(--space-4);
  }

  .model-record__detail-cell {
    border-top: 0;
    padding-block: var(--space-4);
  }
}
</style>
