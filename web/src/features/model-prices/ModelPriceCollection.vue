<script setup lang="ts">
import { Pencil } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ModelPriceDto, ModelPriceStatus } from '@/app/resources/model-prices'
import LedgerRecordList from '@/components/collection/LedgerRecordList.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import ModelPriceResetDialog from './ModelPriceResetDialog.vue'
import { presentModelPrice } from './model-price-presenter'

const props = defineProps<{
  rows: ModelPriceDto[]
  status: ModelPriceStatus
}>()
const emit = defineEmits<{
  edit: [row: ModelPriceDto, trigger: HTMLElement]
}>()
const { locale, t } = useI18n()

const presentations = computed(() =>
  props.rows.map((row) =>
    presentModelPrice(row, {
      unavailable: t('modelPrices.values.unavailable'),
      free: t('modelPrices.values.free'),
      configured: (value) => t('modelPrices.values.configured', { value }),
    }),
  ),
)

function edit(row: ModelPriceDto, trigger: HTMLElement): void {
  emit('edit', row, trigger)
}
</script>

<template>
  <section class="model-price-section" :aria-labelledby="`model-price-${status}-title`">
    <header class="model-price-section__heading">
      <div>
        <h2 :id="`model-price-${status}-title`">{{ t(`modelPrices.sections.${status}.title`) }}</h2>
        <p>{{ t(`modelPrices.sections.${status}.description`) }}</p>
      </div>
      <span>{{ t('modelPrices.sections.count', { count: rows.length }) }}</span>
    </header>

    <LedgerRecordList
      :label="t(`modelPrices.sections.${status}.tableLabel`)"
      :row-count="rows.length + 1"
      grid-class="model-price-record-grid"
    >
      <template #header>
        <span role="columnheader">{{ t('modelPrices.columns.identity') }}</span>
        <span role="columnheader">{{ t('modelPrices.columns.status') }}</span>
        <span role="columnheader">{{ t('modelPrices.columns.prices') }}</span>
        <span role="columnheader">{{ t('modelPrices.columns.facts') }}</span>
        <span role="columnheader">{{ t('modelPrices.columns.updatedAt') }}</span>
        <span role="columnheader">{{ t('modelPrices.columns.actions') }}</span>
      </template>

      <article
        v-for="(record, index) in presentations"
        :key="record.row.id"
        class="ledger-record-list__record model-price-record"
        role="row"
        :aria-rowindex="index + 2"
      >
        <div class="ledger-record-list__cell model-price-identity" role="cell">
          <strong>{{ record.row.model_id }}</strong>
          <span>
            {{ t(`modelPrices.scope.${record.row.scope.kind}`) }} · {{ record.row.scope.label }}
          </span>
        </div>

        <div class="ledger-record-list__cell model-price-status" role="cell">
          <StatusBadge
            size="compact"
            :tone="record.row.pricing_status === 'configured' ? 'success' : 'warning'"
          >
            {{ t(`modelPrices.status.${record.row.pricing_status}`) }}
          </StatusBadge>
        </div>

        <div class="ledger-record-list__cell model-price-slots" role="cell">
          <div
            v-for="slot in record.slots"
            :key="slot.field"
            class="model-price-slot"
            :data-state="slot.state"
          >
            <span>{{ t(`modelPrices.fields.${slot.field}`) }}</span>
            <strong>{{ slot.value }}</strong>
          </div>
          <p v-if="record.row.has_context_tiers">
            <span v-if="record.row.has_context_tiers">{{ t('modelPrices.facts.tiered') }}</span>
          </p>
        </div>

        <div class="ledger-record-list__cell model-price-facts" role="cell">
          <span class="mobile-label">{{ t('modelPrices.columns.facts') }}</span>
          <strong>
            {{
              record.method === null
                ? t('modelPrices.method.pending')
                : record.method === 'auto_matched'
                  ? t('modelPrices.method.auto_matched', {
                      provider: record.row.matched_provider_id,
                    })
                  : t(`modelPrices.method.${record.method}`)
            }}
          </strong>
          <small v-if="record.method === 'auto_sync'">
            {{ t('modelPrices.reference', { provider: record.row.matched_provider_id }) }}
          </small>
          <small>
            {{
              t('modelPrices.references', {
                entries: record.row.reference_count,
                groups: record.row.reference_group_count,
              })
            }}
          </small>
        </div>

        <div class="ledger-record-list__cell model-price-updated" role="cell">
          <span class="mobile-label">{{ t('modelPrices.columns.updatedAt') }}</span>
          <AppDateTime :instant="record.row.updated_at_ms" :locale="locale" />
        </div>

        <div class="ledger-record-list__cell model-price-actions" role="cell">
          <IconButton
            variant="ghost"
            tone="action"
            size="compact"
            :label="t('modelPrices.edit.open', { model: record.row.model_id })"
            @click="edit(record.row, $event.currentTarget as HTMLElement)"
          >
            <Pencil :size="15" aria-hidden="true" />
          </IconButton>
          <ModelPriceResetDialog v-if="record.row.can_reset" :row="record.row" action="reset" />
          <ModelPriceResetDialog v-if="record.row.can_delete" :row="record.row" action="delete" />
        </div>
      </article>
    </LedgerRecordList>
  </section>
</template>

<style scoped>
.model-price-section {
  display: grid;
  min-width: 0;
  gap: var(--space-2-5);
}

.model-price-section + .model-price-section {
  margin-top: var(--space-6);
}

.model-price-section__heading {
  display: flex;
  align-items: end;
  justify-content: space-between;
  gap: var(--space-4);
}

.model-price-section__heading h2,
.model-price-section__heading p {
  margin: 0;
}

.model-price-section__heading h2 {
  font-size: var(--text-body);
}

.model-price-section__heading p,
.model-price-section__heading > span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-price-section__heading p {
  margin-top: var(--space-0-75);
}

.model-price-section__heading > span {
  font-family: var(--font-mono);
}

.model-price-record-grid {
  --ledger-record-list-grid: minmax(150px, 1.15fr) 104px minmax(300px, 2.25fr) minmax(138px, 0.9fr)
    126px 116px;
  --ledger-record-list-column-gap: var(--space-3-5);
  --ledger-record-list-record-min-height: 88px;
  --ledger-record-list-record-padding: var(--space-3) 0;
}

.model-price-identity,
.model-price-facts,
.model-price-updated {
  display: grid;
  align-content: center;
  gap: var(--space-1);
}

.model-price-identity strong {
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-meta);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-price-identity span,
.model-price-facts small,
.model-price-updated {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-price-slots {
  display: grid;
  min-width: 0;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-1-75);
}

.model-price-slot {
  min-width: 0;
  border-left: 1px solid var(--color-border-subtle);
  padding-left: var(--space-1-75);
}

.model-price-slot:first-child {
  border-left: 0;
  padding-left: 0;
}

.model-price-slot span {
  display: block;
  overflow: hidden;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-price-slot strong {
  display: block;
  overflow: hidden;
  margin-top: var(--space-0-5);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-price-slot[data-state='unavailable'] strong {
  color: var(--color-text-faint);
  font-family: var(--font-sans);
  font-weight: 500;
}

.model-price-slot[data-state='free'] strong {
  color: var(--color-success);
}

.model-price-slots > p {
  display: flex;
  grid-column: 1 / -1;
  flex-wrap: wrap;
  gap: var(--space-1-75);
  margin: var(--space-0-5) 0 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.model-price-slots > p span + span::before {
  content: '·';
  margin-right: var(--space-1-75);
}

.model-price-facts strong {
  font-size: var(--text-sm);
  font-weight: 600;
}

.model-price-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-0-5);
}

.mobile-label {
  display: none;
}

@media (max-width: 1160px) {
  .model-price-record-grid {
    --ledger-record-list-grid: minmax(140px, 1.1fr) 96px minmax(280px, 2fr) minmax(120px, 0.8fr)
      108px 108px;
    --ledger-record-list-column-gap: var(--space-2-5);
  }
}

@media (max-width: 860px) {
  .model-price-record-grid {
    --ledger-record-list-card-grid: minmax(0, 1fr) auto;
  }

  .model-price-identity {
    grid-column: 1;
    grid-row: 1;
  }

  .model-price-status {
    grid-column: 2;
    grid-row: 1;
    justify-self: end;
  }

  .model-price-slots {
    grid-column: 1 / -1;
    grid-row: 2;
  }

  .model-price-facts {
    grid-column: 1;
    grid-row: 3;
  }

  .model-price-updated {
    grid-column: 2;
    grid-row: 3;
    text-align: right;
  }

  .model-price-actions {
    grid-column: 1 / -1;
    grid-row: 4;
    justify-content: flex-end;
    border-top: 1px solid var(--color-border-subtle);
    padding-top: var(--space-2-5);
  }

  .mobile-label {
    display: block;
    color: var(--color-text-faint);
    font-size: var(--text-label-xs);
  }
}

@media (max-width: 560px) {
  .model-price-section__heading {
    align-items: flex-start;
  }

  .model-price-slots {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
