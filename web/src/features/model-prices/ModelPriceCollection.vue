<script setup lang="ts">
import { ExternalLink, Pencil, TriangleAlert } from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ModelPriceRuleDto, ModelPriceSource } from '@/app/resources/model-prices'
import AppButton from '@/components/ui/AppButton.vue'
import AppDateTime from '@/components/ui/AppDateTime.vue'
import DataTable from '@/components/ui/DataTable.vue'
import MobileRecordCard from '@/components/ui/MobileRecordCard.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import ModelPriceResetDialog from './ModelPriceResetDialog.vue'
import { modelPricePatternKind } from './model-price-form'
import { presentModelPriceRule } from './model-price-presenter'

const props = defineProps<{
  rules: ModelPriceRuleDto[]
  source: ModelPriceSource
}>()
const emit = defineEmits<{
  edit: [rule: ModelPriceRuleDto, trigger: HTMLElement]
}>()
const { locale, n, t } = useI18n()
const mobile = ref(false)
let mediaQuery: MediaQueryList | undefined

try {
  mediaQuery = window.matchMedia('(max-width: 767px)')
  mobile.value = mediaQuery.matches
} catch {
  mediaQuery = undefined
}

const presentations = computed(() =>
  props.rules.map((rule) =>
    presentModelPriceRule(rule, {
      fieldLabels: {
        uncached_input: t('modelPrices.fields.uncached_input'),
        cache_read: t('modelPrices.fields.cache_read'),
        cache_write_5m: t('modelPrices.fields.cache_write_5m'),
        cache_write_1h: t('modelPrices.fields.cache_write_1h'),
        output: t('modelPrices.fields.output'),
      },
      notConfigured: t('modelPrices.notConfigured'),
      explicitlyFree: t('modelPrices.explicitlyFree'),
      configuredPrice: (price) => t('modelPrices.configuredPrice', { price }),
      kindLabel: (pattern) => t(`modelPrices.kind.${modelPricePatternKind(pattern)}`),
      sourceLabel: (source) => t(`modelPrices.source.${source}`),
      policySummary: (policy) =>
        t('modelPrices.builtin.longContext.summary', {
          threshold: n(policy.input_threshold_tokens),
          inputMultiplier: n(policy.input_multiplier),
          outputMultiplier: n(policy.output_multiplier),
        }),
    }),
  ),
)
const testPrefix = computed(() => (props.source === 'user' ? 'override' : 'builtin'))

function sourceRule(index: number): ModelPriceRuleDto {
  const rule = props.rules[index]
  if (!rule) throw new Error(`MODEL_PRICE_SOURCE_MISSING:${index}`)
  return rule
}

function edit(index: number, trigger: HTMLElement): void {
  emit('edit', sourceRule(index), trigger)
}

function updateMedia(event: MediaQueryListEvent): void {
  mobile.value = event.matches
}

onMounted(() => mediaQuery?.addEventListener('change', updateMedia))
onBeforeUnmount(() => mediaQuery?.removeEventListener('change', updateMedia))
</script>

<template>
  <div class="model-price-collection">
    <div v-if="mobile" class="model-price-collection__cards">
      <MobileRecordCard
        v-for="(record, index) in presentations"
        :key="record.pattern"
        :label="record.pattern"
        :data-test="`${testPrefix}-price-card-${index}`"
      >
        <template #header>
          <div class="model-price-card__identity">
            <h3>{{ record.pattern }}</h3>
            <StatusBadge>{{ record.kind }}</StatusBadge>
          </div>
          <StatusBadge>{{ record.source }}</StatusBadge>
        </template>

        <p
          v-if="record.globalOverride"
          class="model-price-card__warning"
          data-test="model-price-global-row-warning"
        >
          <TriangleAlert :size="14" aria-hidden="true" />
          {{ t('modelPrices.globalUserOverride') }}
        </p>
        <details>
          <summary>{{ t('modelPrices.details') }}</summary>
          <dl>
            <template v-for="price in record.priceRows" :key="price.field">
              <dt>{{ price.label }}</dt>
              <dd :data-state="price.state">{{ price.value }}</dd>
            </template>
            <dt>{{ t('modelPrices.table.source') }}</dt>
            <dd>
              <a
                v-if="record.sourceUrl"
                :data-test="`${testPrefix}-source-${index}`"
                class="model-price-collection__source"
                :href="record.sourceUrl"
                target="_blank"
                rel="noopener noreferrer"
              >
                {{ t('modelPrices.builtin.source') }}
                <ExternalLink :size="14" aria-hidden="true" />
              </a>
              <span v-else :data-test="`${testPrefix}-source-${index}`">
                {{
                  source === 'builtin'
                    ? t('modelPrices.sourceUnavailable')
                    : t('modelPrices.source.user')
                }}
              </span>
            </dd>
            <dt>{{ t('modelPrices.table.updatedAt') }}</dt>
            <dd>
              <AppDateTime :instant="record.updatedAt" :locale="locale" />
            </dd>
          </dl>
          <p
            v-if="record.policySummary"
            :data-test="`builtin-pricing-policy-${index}`"
            class="model-price-collection__policy"
          >
            <StatusBadge>{{ t('modelPrices.builtin.longContext.label') }}</StatusBadge>
            <span>{{ record.policySummary }}</span>
          </p>
        </details>

        <template #actions>
          <AppButton
            :data-test="`${testPrefix}-price-edit-${index}`"
            variant="ghost"
            @click="edit(index, $event.currentTarget as HTMLElement)"
          >
            <Pencil :size="15" aria-hidden="true" />{{
              source === 'user'
                ? t('modelPrices.overrides.edit')
                : t('modelPrices.builtin.createOverride')
            }}
          </AppButton>
          <ModelPriceResetDialog v-if="source === 'user'" :rule="sourceRule(index)" />
        </template>
      </MobileRecordCard>
    </div>

    <DataTable
      v-else
      :caption="
        source === 'user' ? t('modelPrices.overrides.caption') : t('modelPrices.builtin.caption')
      "
      :scroll-hint="t('modelPrices.scrollHint')"
      dense
    >
      <thead>
        <tr>
          <th scope="col" data-column-priority="high">{{ t('modelPrices.table.pattern') }}</th>
          <th scope="col" data-column-priority="high">{{ t('modelPrices.table.kind') }}</th>
          <th v-for="field in presentations[0]?.priceRows ?? []" :key="field.field" scope="col">
            {{ field.label }}
          </th>
          <th scope="col">{{ t('modelPrices.table.source') }}</th>
          <th scope="col">{{ t('modelPrices.table.updatedAt') }}</th>
          <th scope="col" data-column-priority="high">{{ t('modelPrices.table.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="(record, index) in presentations"
          :key="record.pattern"
          :data-test="`${testPrefix}-price-row-${index}`"
        >
          <td>
            <code>{{ record.pattern }}</code>
            <span
              v-if="record.globalOverride"
              class="model-price-collection__global-warning"
              data-test="model-price-global-row-warning"
            >
              <TriangleAlert :size="14" aria-hidden="true" />
              {{ t('modelPrices.globalUserOverride') }}
            </span>
            <div
              v-if="record.policySummary"
              class="model-price-collection__policy"
              :data-test="`builtin-pricing-policy-${index}`"
            >
              <StatusBadge>{{ t('modelPrices.builtin.longContext.label') }}</StatusBadge>
              <span>{{ record.policySummary }}</span>
            </div>
          </td>
          <td>
            <StatusBadge>{{ record.kind }}</StatusBadge>
            <span class="model-price-collection__source-label">{{ record.source }}</span>
          </td>
          <td
            v-for="price in record.priceRows"
            :key="price.field"
            :data-test="`${testPrefix}-${index}-${price.field}`"
            :data-state="price.state"
          >
            {{ price.value }}
          </td>
          <td>
            <a
              v-if="record.sourceUrl"
              :data-test="`${testPrefix}-source-${index}`"
              class="model-price-collection__source"
              :href="record.sourceUrl"
              target="_blank"
              rel="noopener noreferrer"
            >
              {{ t('modelPrices.builtin.source') }}
              <ExternalLink :size="14" aria-hidden="true" />
            </a>
            <span v-else :data-test="`${testPrefix}-source-${index}`">
              {{
                source === 'builtin'
                  ? t('modelPrices.sourceUnavailable')
                  : t('modelPrices.source.user')
              }}
            </span>
          </td>
          <td>
            <AppDateTime :instant="record.updatedAt" :locale="locale" />
          </td>
          <td>
            <div class="model-price-collection__row-actions">
              <AppButton
                :data-test="`${testPrefix}-price-edit-${index}`"
                variant="ghost"
                @click="edit(index, $event.currentTarget as HTMLElement)"
              >
                <Pencil :size="15" aria-hidden="true" />{{
                  source === 'user'
                    ? t('modelPrices.overrides.edit')
                    : t('modelPrices.builtin.createOverride')
                }}
              </AppButton>
              <ModelPriceResetDialog v-if="source === 'user'" :rule="sourceRule(index)" />
            </div>
          </td>
        </tr>
      </tbody>
    </DataTable>
  </div>
</template>

<style scoped>
.model-price-collection {
  min-width: 0;
}

.model-price-collection__cards {
  display: grid;
  gap: var(--space-3);
}

.model-price-card__identity {
  min-width: 0;
}

.model-price-card__identity h3 {
  margin: 0 0 var(--space-2);
  font-family: var(--font-mono);
  font-size: var(--text-lg);
  overflow-wrap: anywhere;
}

.model-price-card__warning,
.model-price-collection__global-warning {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-warning);
  font-size: var(--text-xs);
  font-weight: 650;
}

.model-price-card__warning {
  margin: 0;
}

.model-price-collection details {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}

.model-price-collection summary {
  min-height: var(--touch-target);
  color: var(--color-action);
  cursor: pointer;
  font-weight: 650;
}

.model-price-collection details dl {
  display: grid;
  grid-template-columns: minmax(8rem, auto) minmax(0, 1fr);
  gap: var(--space-2);
  margin: var(--space-3) 0 0;
}

.model-price-collection details dt {
  color: var(--color-text-muted);
}

.model-price-collection details dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.model-price-collection code {
  color: var(--color-code);
  font-family: var(--font-mono);
  white-space: nowrap;
}

.model-price-collection td:not(:first-child, :last-child) {
  font-variant-numeric: tabular-nums;
}

.model-price-collection__source,
.model-price-collection__row-actions {
  display: inline-flex;
  align-items: center;
}

.model-price-collection__source {
  min-height: var(--touch-target);
  gap: var(--space-1);
  color: var(--color-action);
  font-weight: 650;
  white-space: nowrap;
}

.model-price-collection__source-label {
  display: block;
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.model-price-collection__policy {
  display: grid;
  max-width: 22rem;
  gap: var(--space-1);
  margin: var(--space-2) 0 0;
  color: var(--color-text-muted);
  font-size: var(--text-xs);
  line-height: var(--line-normal);
  white-space: normal;
}

.model-price-collection__policy > :first-child {
  width: fit-content;
}

.model-price-collection__global-warning {
  margin-top: var(--space-1);
  white-space: normal;
}

.model-price-collection__row-actions {
  gap: var(--space-2);
  white-space: nowrap;
}
</style>
