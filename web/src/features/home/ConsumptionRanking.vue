<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HomeRankings, HomeStatisticsRef } from '@/app/resources/home'
import DataTable from '@/components/ui/DataTable.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'
import { formatEstimatedCost, formatInteger, formatTokens } from '@/lib/format'

type RankingDimension = 'models' | 'groups' | 'accessKeys'

const props = withDefaults(
  defineProps<{
    rankings: HomeRankings
    range: string
    loading?: boolean
  }>(),
  {
    loading: false,
  },
)

const { locale, t } = useI18n()
const dimension = ref<RankingDimension>('models')
const options = computed(() => [
  { value: 'models', label: t('home.ledger.ranking.tabs.models') },
  { value: 'groups', label: t('home.ledger.ranking.tabs.groups') },
  { value: 'accessKeys', label: t('home.ledger.ranking.tabs.accessKeys') },
])
const rows = computed(() => {
  if (dimension.value === 'models') return props.rankings.models.slice(0, 5)
  if (dimension.value === 'groups') return props.rankings.groups.slice(0, 5)
  return props.rankings.access_keys.slice(0, 5)
})

function referenceName(reference: HomeStatisticsRef, kind: 'group' | 'accessKey'): string {
  if (!reference.deleted && reference.name) return reference.name
  return t(
    kind === 'group' ? 'home.ledger.ranking.deletedGroup' : 'home.ledger.ranking.deletedAccessKey',
    { id: reference.id },
  )
}

function modelName(model: string): string {
  return model || t('home.ledger.ranking.unknownModel')
}

function tokenCellAttributes(totalTokens: number): { title: string; 'aria-label': string } {
  const exactTokenCount = t('home.ledger.tokens', {
    count: formatInteger(totalTokens, locale.value),
  })
  return {
    title: exactTokenCount,
    'aria-label': exactTokenCount,
  }
}
</script>

<template>
  <section class="consumption-ranking" :aria-labelledby="'consumption-ranking-title'">
    <div class="consumption-ranking__header">
      <h2 id="consumption-ranking-title">
        {{ t('home.ledger.ranking.title', { range }) }}
      </h2>
      <SegmentedControl
        v-model="dimension"
        :label="t('home.ledger.ranking.tabs.label')"
        :options="options"
      />
    </div>

    <DataTable
      appearance="editorial"
      column-collapse="narrow"
      dense
      :aria-busy="loading ? 'true' : undefined"
      :caption="t('home.ledger.ranking.caption')"
      :scroll-hint="t('home.ledger.ranking.scrollHint')"
    >
      <thead>
        <tr v-if="dimension === 'models'">
          <th scope="col">{{ t('home.ledger.ranking.columns.model') }}</th>
          <th scope="col">{{ t('home.ledger.ranking.columns.group') }}</th>
          <th scope="col" class="consumption-ranking__number">
            {{ t('home.ledger.ranking.columns.requests') }}
          </th>
          <th scope="col" class="consumption-ranking__number" data-column-priority="low">
            {{ t('home.ledger.ranking.columns.tokens') }}
          </th>
          <th scope="col" class="consumption-ranking__number">
            {{ t('home.ledger.ranking.columns.cost') }}
          </th>
        </tr>
        <tr v-else>
          <th scope="col">
            {{
              dimension === 'groups'
                ? t('home.ledger.ranking.columns.group')
                : t('home.ledger.ranking.columns.accessKey')
            }}
          </th>
          <th scope="col" class="consumption-ranking__number">
            {{ t('home.ledger.ranking.columns.requests') }}
          </th>
          <th scope="col" class="consumption-ranking__number" data-column-priority="low">
            {{ t('home.ledger.ranking.columns.tokens') }}
          </th>
          <th scope="col" class="consumption-ranking__number">
            {{ t('home.ledger.ranking.columns.cost') }}
          </th>
        </tr>
      </thead>
      <tbody>
        <template v-if="loading">
          <tr v-for="row in 5" :key="`loading-${row}`">
            <td v-for="column in dimension === 'models' ? 5 : 4" :key="column">
              <SkeletonBlock
                height="0.72rem"
                :width="column === 1 ? '72%' : column === 2 ? '58%' : '44%'"
              />
            </td>
          </tr>
        </template>
        <tr v-else-if="rows.length === 0">
          <td class="consumption-ranking__empty" :colspan="dimension === 'models' ? 5 : 4">
            {{ t('home.ledger.ranking.empty') }}
          </td>
        </tr>
        <template v-else-if="dimension === 'models'">
          <tr v-for="row in rankings.models.slice(0, 5)" :key="`${row.group.id}:${row.model}`">
            <td class="consumption-ranking__model">{{ modelName(row.model) }}</td>
            <td>{{ referenceName(row.group, 'group') }}</td>
            <td class="consumption-ranking__number consumption-ranking__mono">
              {{ formatInteger(row.request_count, locale) }}
            </td>
            <td
              class="consumption-ranking__number consumption-ranking__mono"
              data-column-priority="low"
              v-bind="tokenCellAttributes(row.total_tokens)"
            >
              {{ formatTokens(row.total_tokens, locale) }}
            </td>
            <td class="consumption-ranking__number consumption-ranking__mono">
              {{ formatEstimatedCost(row.estimated_cost_nano_usd, locale) }}
            </td>
          </tr>
        </template>
        <template v-else-if="dimension === 'groups'">
          <tr v-for="row in rankings.groups.slice(0, 5)" :key="row.group.id">
            <td>
              {{ referenceName(row.group, 'group') }}
            </td>
            <td class="consumption-ranking__number consumption-ranking__mono">
              {{ formatInteger(row.request_count, locale) }}
            </td>
            <td
              class="consumption-ranking__number consumption-ranking__mono"
              data-column-priority="low"
              v-bind="tokenCellAttributes(row.total_tokens)"
            >
              {{ formatTokens(row.total_tokens, locale) }}
            </td>
            <td class="consumption-ranking__number consumption-ranking__mono">
              {{ formatEstimatedCost(row.estimated_cost_nano_usd, locale) }}
            </td>
          </tr>
        </template>
        <template v-else>
          <tr v-for="row in rankings.access_keys.slice(0, 5)" :key="row.access_key.id">
            <td>
              {{ referenceName(row.access_key, 'accessKey') }}
            </td>
            <td class="consumption-ranking__number consumption-ranking__mono">
              {{ formatInteger(row.request_count, locale) }}
            </td>
            <td
              class="consumption-ranking__number consumption-ranking__mono"
              data-column-priority="low"
              v-bind="tokenCellAttributes(row.total_tokens)"
            >
              {{ formatTokens(row.total_tokens, locale) }}
            </td>
            <td class="consumption-ranking__number consumption-ranking__mono">
              {{ formatEstimatedCost(row.estimated_cost_nano_usd, locale) }}
            </td>
          </tr>
        </template>
      </tbody>
    </DataTable>
  </section>
</template>

<style scoped>
.consumption-ranking {
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 20px;
}
.consumption-ranking__header {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 14px;
  margin-bottom: 12px;
}
.consumption-ranking__header h2 {
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--text-lg);
  font-weight: 500;
}
.consumption-ranking__number {
  text-align: right !important;
}
.consumption-ranking__mono {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}
.consumption-ranking__model {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.consumption-ranking__empty {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
  padding-block: var(--space-3) !important;
}

</style>
