<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { UsageAggregateDto, UsageReportDto } from '@/app/resources/usage'
import DataTable from '@/components/ui/DataTable.vue'
import { formatEstimatedUSD } from '@/features/usage/estimated-cost'

import { usageBreakdownLocation } from './home-presenter'

const props = defineProps<{
  report: UsageReportDto
  groupNames: ReadonlyMap<number, string>
}>()
const { locale, t } = useI18n()
const rows = computed(() => props.report.breakdown.slice(0, 5))

function formatCount(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value)
}

function formatCost(row: UsageAggregateDto): string {
  const cost = formatEstimatedUSD(row.estimated_cost_usd, locale.value)
  return row.unpriced_request_count > 0 ? t('home.ranking.knownPlusUnknown', { cost }) : cost
}

function groupName(groupID: number): string {
  return (
    props.groupNames.get(groupID) ??
    t('home.ranking.unknownGroup', {
      id: groupID,
    })
  )
}
</script>

<template>
  <section class="home-ranking" data-test="home-cost-ranking" aria-labelledby="home-ranking-title">
    <header class="home-ranking__header">
      <div>
        <h2 id="home-ranking-title">{{ t('home.ranking.title', { range: report.range }) }}</h2>
        <p>{{ t('home.ranking.description') }}</p>
      </div>
    </header>

    <DataTable
      v-if="rows.length > 0"
      :caption="t('home.ranking.caption')"
      :scroll-hint="t('home.ranking.scrollHint')"
    >
      <thead>
        <tr>
          <th scope="col">{{ t('home.ranking.group') }}</th>
          <th scope="col">{{ t('home.ranking.model') }}</th>
          <th scope="col">{{ t('home.ranking.requests') }}</th>
          <th scope="col" data-column-priority="low">{{ t('home.ranking.tokens') }}</th>
          <th scope="col">{{ t('home.ranking.cost') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in rows" :key="`${row.group_id}:${row.model}`" data-ranking-row>
          <td>{{ groupName(row.group_id) }}</td>
          <td data-ranking-model>
            <RouterLink :to="usageBreakdownLocation(report.range, row.group_id, row.model)">
              {{ row.model }}
            </RouterLink>
          </td>
          <td>{{ formatCount(row.request_count) }}</td>
          <td data-column-priority="low">{{ formatCount(row.total_tokens) }}</td>
          <td>{{ formatCost(row) }}</td>
        </tr>
      </tbody>
    </DataTable>
    <p v-else class="home-ranking__empty">{{ t('home.ranking.empty') }}</p>

    <footer class="home-ranking__footer" data-test="home-ranking-footer">
      <span>{{ t('home.ranking.footer', { count: report.breakdown_group_count }) }}</span>
      <RouterLink :to="{ name: 'monitor', query: { tab: 'usage', range: report.range } }">
        {{ t('home.ranking.viewAll') }}
        <ArrowRight :size="16" aria-hidden="true" />
      </RouterLink>
    </footer>
  </section>
</template>

<style scoped>
.home-ranking {
  display: grid;
  gap: var(--space-4);
}
.home-ranking__header h2,
.home-ranking__header p,
.home-ranking__empty {
  margin: 0;
}
.home-ranking__header h2 {
  font-family: var(--font-serif);
  font-size: 1.45rem;
  font-weight: 500;
}
.home-ranking__header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.home-ranking td,
.home-ranking th {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}
.home-ranking td:first-child,
.home-ranking th:first-child {
  font-family: var(--font-sans);
}
.home-ranking td a {
  color: var(--color-action);
  font-weight: 650;
}
.home-ranking__empty {
  border-top: 1px solid var(--color-border-subtle);
  border-bottom: 1px solid var(--color-border-subtle);
  color: var(--color-text-muted);
  padding: var(--space-5) 0;
}
.home-ranking__footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-4);
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.home-ranking__footer a {
  display: inline-flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-1);
  color: var(--color-action);
  font-family: var(--font-sans);
  font-weight: 650;
}
</style>
