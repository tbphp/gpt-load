<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import type { HomeStatisticsDto } from '@/app/resources/home'
import { monitorLocation } from '@/app/route-locations'
import DataTable from '@/components/ui/DataTable.vue'
import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'

import HomeSectionHeading from './HomeSectionHeading.vue'
import { formatEstimatedCost, formatInteger, formatTokens } from '@/lib/format'

const props = withDefaults(
  defineProps<{
    snapshot: HomeStatisticsDto | null
    loading?: boolean
  }>(),
  { loading: false },
)

const { locale, t } = useI18n()

const rows = computed(() => props.snapshot?.rankings.models.slice(0, 5) ?? [])
const totalCost = computed(() =>
  props.snapshot === null
    ? '—'
    : formatEstimatedCost(props.snapshot.summary.estimated_cost_nano_usd, locale.value),
)

function modelName(model: string): string {
  return model || t('home.ledger.spend.unknownModel')
}

function modelUsageLocation(model: string) {
  return monitorLocation(
    model === ''
      ? { tab: 'usage', range: '30d' }
      : { tab: 'usage', range: '30d', upstream_model: model },
  )
}

function tokenCellAttributes(totalTokens: number): { title: string; 'aria-label': string } {
  const exactTokenCount = t('home.ledger.tokens', {
    count: formatInteger(totalTokens, locale.value),
  })
  return { title: exactTokenCount, 'aria-label': exactTokenCount }
}
</script>

<template>
  <section class="home-spend" aria-labelledby="home-spend-title">
    <HomeSectionHeading id="home-spend-title" :title="t('home.ledger.spend.title')">
      <template #actions>
        <RouterLink class="home-spend__link" :to="monitorLocation({ tab: 'usage', range: '30d' })">
          {{ t('home.ledger.spend.viewDetail') }}
        </RouterLink>
      </template>
    </HomeSectionHeading>

    <SkeletonBlock
      v-if="loading"
      class="home-spend__value-skeleton"
      width="140px"
      height="2.25rem"
    />
    <p v-else class="home-spend__value">{{ totalCost }}</p>

    <DataTable
      appearance="editorial"
      column-collapse="narrow"
      dense
      :aria-busy="loading ? 'true' : undefined"
      :caption="t('home.ledger.spend.caption')"
      :scroll-hint="t('home.ledger.spend.scrollHint')"
    >
      <thead>
        <tr>
          <th scope="col">{{ t('home.ledger.spend.columns.model') }}</th>
          <th scope="col" class="home-spend__number">
            {{ t('home.ledger.spend.columns.requests') }}
          </th>
          <th scope="col" class="home-spend__number" data-column-priority="low">
            {{ t('home.ledger.spend.columns.tokens') }}
          </th>
          <th scope="col" class="home-spend__number">
            {{ t('home.ledger.spend.columns.cost') }}
          </th>
        </tr>
      </thead>
      <tbody>
        <template v-if="loading">
          <tr v-for="row in 5" :key="`loading-${row}`">
            <td v-for="column in 4" :key="column">
              <SkeletonBlock
                height="0.72rem"
                :width="column === 1 ? '72%' : column === 2 ? '58%' : '44%'"
              />
            </td>
          </tr>
        </template>
        <tr v-else-if="rows.length === 0">
          <td class="home-spend__empty" colspan="4">{{ t('home.ledger.spend.empty') }}</td>
        </tr>
        <tr v-for="row in rows" v-else :key="row.model">
          <td class="home-spend__model">
            <RouterLink
              class="home-spend__model-link"
              :to="modelUsageLocation(row.model)"
              :aria-label="t('home.ledger.spend.viewModel', { model: modelName(row.model) })"
            >
              {{ modelName(row.model) }}
            </RouterLink>
          </td>
          <td class="home-spend__number home-spend__mono">
            {{ formatInteger(row.request_count, locale) }}
          </td>
          <td
            class="home-spend__number home-spend__mono"
            data-column-priority="low"
            v-bind="tokenCellAttributes(row.total_tokens)"
          >
            {{ formatTokens(row.total_tokens, locale) }}
          </td>
          <td class="home-spend__number home-spend__mono">
            {{ formatEstimatedCost(row.estimated_cost_nano_usd, locale) }}
          </td>
        </tr>
      </tbody>
    </DataTable>
  </section>
</template>

<style scoped>
.home-spend {
  margin-top: 36px;
  border-top: 1px solid var(--color-border-subtle);
  padding: 20px 0 4px;
}

.home-spend__link {
  color: var(--color-action);
  font-size: var(--text-meta);
  font-weight: 600;
  white-space: nowrap;
}

.home-spend__link:hover {
  text-decoration: underline;
}

.home-spend__value {
  margin: 10px 0 16px;
  font-family: var(--font-mono);
  font-size: var(--stat-value);
  font-weight: 550;
  line-height: 1.05;
  letter-spacing: -0.03em;
  font-variant-numeric: tabular-nums;
}

.home-spend__value-skeleton {
  margin-bottom: 18px;
}

.home-spend__number {
  text-align: right !important;
}

.home-spend__mono {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.home-spend__model {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}

.home-spend__model-link {
  color: inherit;
}

.home-spend__model-link:hover {
  color: var(--color-action);
  text-decoration: underline;
}

.home-spend__empty {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  padding-block: var(--space-3) !important;
}
</style>
