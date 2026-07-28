<script setup lang="ts">
import { ArrowLeft, ExternalLink, Pencil, Plus, Tags, TriangleAlert } from 'lucide-vue-next'
import { useQuery } from '@tanstack/vue-query'
import { nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { getModelPrices, type ModelPriceRuleDto } from '@/api/control/model-prices'
import { controlQueryKeys } from '@/app/query-keys'
import AppButton from '@/components/ui/AppButton.vue'
import DataTable from '@/components/ui/DataTable.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import SurfaceCard from '@/components/ui/SurfaceCard.vue'

import ModelPriceDrawer from './ModelPriceDrawer.vue'
import ModelPriceResetDialog from './ModelPriceResetDialog.vue'
import { modelPricePatternKind, type ModelPriceField } from './model-price-form'

const client = useApiClient()
const { n, t } = useI18n()
const drawerOpen = ref(false)
const selected = ref<ModelPriceRuleDto | null>(null)
let restoreFocus: HTMLElement | null = null

const pricesQuery = useQuery({
  queryKey: controlQueryKeys.modelPrices(),
  queryFn: ({ signal }) => getModelPrices(client, signal),
})

function addOverride(): void {
  selected.value = null
  restoreFocus = null
  drawerOpen.value = true
}

function editRule(rule: ModelPriceRuleDto, event: Event): void {
  selected.value = rule
  restoreFocus = event.currentTarget as HTMLElement
  drawerOpen.value = true
}

async function setDrawerOpen(open: boolean): Promise<void> {
  drawerOpen.value = open
  if (!open) {
    selected.value = null
    const target = restoreFocus
    restoreFocus = null
    await nextTick()
    target?.focus()
  }
}

function formatPrice(value: number | null): string {
  if (value === null) return t('modelPrices.notConfigured')
  if (value === 0) return t('modelPrices.explicitlyFree')
  return t('modelPrices.configuredPrice', { price: value })
}

function kindLabel(pattern: string): string {
  return t(`modelPrices.kind.${modelPricePatternKind(pattern)}`)
}

function value(rule: ModelPriceRuleDto, field: ModelPriceField): string {
  return formatPrice(rule.prices[field])
}

function pricingPolicySummary(rule: ModelPriceRuleDto): string {
  const policy = rule.pricing_policy
  if (policy === null) return ''
  return t('modelPrices.builtin.longContext.summary', {
    threshold: n(policy.input_threshold_tokens),
    inputMultiplier: n(policy.input_multiplier),
    outputMultiplier: n(policy.output_multiplier),
  })
}
</script>

<template>
  <section class="model-prices" aria-labelledby="model-prices-title">
    <RouterLink class="model-prices__back" to="/settings">
      <ArrowLeft :size="16" aria-hidden="true" />{{ t('modelPrices.back') }}
    </RouterLink>

    <PageHeader
      id="model-prices-title"
      :title="t('modelPrices.title')"
      :description="t('modelPrices.description')"
    >
      <template #actions>
        <ModelPriceDrawer :open="drawerOpen" :rule="selected" @update:open="setDrawerOpen">
          <template #trigger>
            <AppButton data-test="model-price-add" @click="addOverride">
              <Plus :size="16" aria-hidden="true" />{{ t('modelPrices.add') }}
            </AppButton>
          </template>
        </ModelPriceDrawer>
      </template>
    </PageHeader>

    <SurfaceCard class="model-prices__notice">
      <Tags :size="18" aria-hidden="true" />
      <div>
        <strong>{{ t('modelPrices.priceUnit') }}</strong>
        <p>{{ t('modelPrices.historyNote') }}</p>
        <p>{{ t('modelPrices.modelIdentityNote') }}</p>
        <p>{{ t('modelPrices.precedenceNote') }}</p>
        <p>{{ t('modelPrices.wholeRuleNote') }}</p>
      </div>
    </SurfaceCard>

    <QueryFeedback
      v-if="pricesQuery.isPending.value"
      state="loading"
      :message="t('modelPrices.loading')"
    />
    <QueryFeedback
      v-else-if="pricesQuery.isError.value && !pricesQuery.data.value"
      state="error"
      :message="t('modelPrices.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="pricesQuery.refetch()"
    />
    <template v-else-if="pricesQuery.data.value">
      <QueryFeedback
        v-if="pricesQuery.isError.value"
        state="stale"
        :message="t('modelPrices.stale')"
        :retry-label="t('common.retry')"
        @retry="pricesQuery.refetch()"
      />

      <section
        class="model-prices__section"
        data-test="model-price-rule-section"
        data-source="user"
        aria-labelledby="override-prices-title"
      >
        <div class="model-prices__section-heading">
          <div>
            <h2 id="override-prices-title">{{ t('modelPrices.overrides.title') }}</h2>
            <p>{{ t('modelPrices.overrides.description') }}</p>
          </div>
          <StatusBadge>{{ t('modelPrices.source.user') }}</StatusBadge>
        </div>
        <EmptyState
          v-if="pricesQuery.data.value.overrides.length === 0"
          :title="t('modelPrices.overrides.empty')"
          :description="t('modelPrices.overrides.emptyDescription')"
        />
        <DataTable v-else :caption="t('modelPrices.overrides.caption')" dense>
          <thead>
            <tr>
              <th scope="col">{{ t('modelPrices.table.pattern') }}</th>
              <th scope="col">{{ t('modelPrices.table.kind') }}</th>
              <th scope="col">{{ t('modelPrices.fields.uncached_input') }}</th>
              <th scope="col">{{ t('modelPrices.fields.cache_read') }}</th>
              <th scope="col">{{ t('modelPrices.fields.cache_write_5m') }}</th>
              <th scope="col">{{ t('modelPrices.fields.cache_write_1h') }}</th>
              <th scope="col">{{ t('modelPrices.fields.output') }}</th>
              <th scope="col">{{ t('modelPrices.table.source') }}</th>
              <th scope="col">{{ t('modelPrices.table.updatedAt') }}</th>
              <th scope="col">{{ t('modelPrices.table.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(rule, index) in pricesQuery.data.value.overrides"
              :key="rule.pattern"
              :data-test="`override-price-row-${index}`"
            >
              <td>
                <code>{{ rule.pattern }}</code>
                <span
                  v-if="rule.pattern === '*'"
                  class="model-prices__global-warning"
                  data-test="model-price-global-row-warning"
                >
                  <TriangleAlert :size="14" aria-hidden="true" />
                  {{ t('modelPrices.globalUserOverride') }}
                </span>
              </td>
              <td>
                <StatusBadge>{{ kindLabel(rule.pattern) }}</StatusBadge>
                <span class="model-prices__source-label">{{ t('modelPrices.source.user') }}</span>
              </td>
              <td :data-test="`override-${index}-uncached_input`">
                {{ value(rule, 'uncached_input') }}
              </td>
              <td :data-test="`override-${index}-cache_read`">
                {{ value(rule, 'cache_read') }}
              </td>
              <td :data-test="`override-${index}-cache_write_5m`">
                {{ value(rule, 'cache_write_5m') }}
              </td>
              <td :data-test="`override-${index}-cache_write_1h`">
                {{ value(rule, 'cache_write_1h') }}
              </td>
              <td :data-test="`override-${index}-output`">{{ value(rule, 'output') }}</td>
              <td :data-test="`override-source-${index}`">
                {{ t('modelPrices.source.user') }}
              </td>
              <td>
                <time :datetime="rule.updated_at">{{ rule.updated_at }}</time>
              </td>
              <td>
                <div class="model-prices__row-actions">
                  <AppButton
                    :data-test="`override-price-edit-${index}`"
                    variant="ghost"
                    @click="editRule(rule, $event)"
                  >
                    <Pencil :size="15" aria-hidden="true" />{{ t('modelPrices.overrides.edit') }}
                  </AppButton>
                  <ModelPriceResetDialog :rule="rule" />
                </div>
              </td>
            </tr>
          </tbody>
        </DataTable>
      </section>

      <section
        class="model-prices__section"
        data-test="model-price-rule-section"
        data-source="builtin"
        aria-labelledby="builtin-prices-title"
      >
        <div class="model-prices__section-heading">
          <div>
            <h2 id="builtin-prices-title">{{ t('modelPrices.builtin.title') }}</h2>
            <p>{{ t('modelPrices.builtin.description') }}</p>
          </div>
          <StatusBadge>{{ t('modelPrices.source.builtin') }}</StatusBadge>
        </div>
        <EmptyState
          v-if="pricesQuery.data.value.builtin.length === 0"
          :title="t('modelPrices.builtin.empty')"
          :description="t('modelPrices.builtin.emptyDescription')"
        />
        <DataTable v-else :caption="t('modelPrices.builtin.caption')" dense>
          <thead>
            <tr>
              <th scope="col">{{ t('modelPrices.table.pattern') }}</th>
              <th scope="col">{{ t('modelPrices.table.kind') }}</th>
              <th scope="col">{{ t('modelPrices.fields.uncached_input') }}</th>
              <th scope="col">{{ t('modelPrices.fields.cache_read') }}</th>
              <th scope="col">{{ t('modelPrices.fields.cache_write_5m') }}</th>
              <th scope="col">{{ t('modelPrices.fields.cache_write_1h') }}</th>
              <th scope="col">{{ t('modelPrices.fields.output') }}</th>
              <th scope="col">{{ t('modelPrices.table.source') }}</th>
              <th scope="col">{{ t('modelPrices.table.updatedAt') }}</th>
              <th scope="col">{{ t('modelPrices.table.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(rule, index) in pricesQuery.data.value.builtin"
              :key="rule.pattern"
              :data-test="`builtin-price-row-${index}`"
            >
              <td>
                <code>{{ rule.pattern }}</code>
                <div
                  v-if="rule.pricing_policy"
                  class="model-prices__pricing-policy"
                  :data-test="`builtin-pricing-policy-${index}`"
                >
                  <StatusBadge>{{ t('modelPrices.builtin.longContext.label') }}</StatusBadge>
                  <span>{{ pricingPolicySummary(rule) }}</span>
                </div>
              </td>
              <td>
                <StatusBadge>{{ kindLabel(rule.pattern) }}</StatusBadge>
                <span class="model-prices__source-label">{{
                  t('modelPrices.source.builtin')
                }}</span>
              </td>
              <td :data-test="`builtin-${index}-uncached_input`">
                {{ value(rule, 'uncached_input') }}
              </td>
              <td :data-test="`builtin-${index}-cache_read`">
                {{ value(rule, 'cache_read') }}
              </td>
              <td :data-test="`builtin-${index}-cache_write_5m`">
                {{ value(rule, 'cache_write_5m') }}
              </td>
              <td :data-test="`builtin-${index}-cache_write_1h`">
                {{ value(rule, 'cache_write_1h') }}
              </td>
              <td :data-test="`builtin-${index}-output`">{{ value(rule, 'output') }}</td>
              <td>
                <a
                  :data-test="`builtin-source-${index}`"
                  class="model-prices__source"
                  :href="rule.source_url ?? undefined"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {{ t('modelPrices.builtin.source') }}
                  <ExternalLink :size="14" aria-hidden="true" />
                </a>
              </td>
              <td>
                <time :datetime="rule.updated_at">{{ rule.updated_at }}</time>
              </td>
              <td>
                <AppButton
                  :data-test="`builtin-price-edit-${index}`"
                  variant="ghost"
                  @click="editRule(rule, $event)"
                >
                  <Pencil :size="15" aria-hidden="true" />{{
                    t('modelPrices.builtin.createOverride')
                  }}
                </AppButton>
              </td>
            </tr>
          </tbody>
        </DataTable>
      </section>
    </template>
  </section>
</template>

<style scoped>
.model-prices {
  display: grid;
  min-width: 0;
  gap: var(--space-5);
}
.model-prices__back,
.model-prices__source,
.model-prices__row-actions {
  display: inline-flex;
  align-items: center;
}
.model-prices__back {
  width: fit-content;
  min-height: 44px;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-weight: 650;
}
.model-prices__back:hover,
.model-prices__source:hover {
  color: var(--color-primary);
}
.model-prices__notice {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-4);
}
.model-prices__notice p,
.model-prices__section-heading p {
  margin: 0;
  color: var(--color-text-muted);
}
.model-prices__notice p + p {
  margin-top: var(--space-1);
}
.model-prices__section {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}
.model-prices__section-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}
.model-prices__section-heading h2 {
  margin: 0;
  font-size: 1.125rem;
}
.model-prices code {
  color: var(--color-code);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  white-space: nowrap;
}
.model-prices td:not(:first-child, :last-child) {
  font-variant-numeric: tabular-nums;
}
.model-prices__source {
  min-height: 44px;
  gap: var(--space-1);
  color: var(--color-primary);
  font-weight: 650;
  white-space: nowrap;
}
.model-prices__source-label {
  display: block;
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: 0.75rem;
}
.model-prices__pricing-policy {
  display: grid;
  max-width: 22rem;
  gap: var(--space-1);
  margin-top: var(--space-2);
  color: var(--color-text-muted);
  font-size: 0.75rem;
  line-height: 1.5;
  white-space: normal;
}
.model-prices__pricing-policy > :first-child {
  width: fit-content;
}
.model-prices__global-warning {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-top: var(--space-1);
  color: var(--color-warning);
  font-size: 0.75rem;
  font-weight: 650;
  white-space: normal;
}
.model-prices__row-actions {
  gap: var(--space-2);
  white-space: nowrap;
}
@media (max-width: 640px) {
  .model-prices__section-heading {
    align-items: stretch;
    flex-direction: column;
  }
  .model-prices__section-heading > :last-child {
    width: fit-content;
  }
}
</style>
