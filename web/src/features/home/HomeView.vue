<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import { homeBaseQueryOptions } from '@/app/resources/home'
import TrendChart from '@/components/charts/TrendChart.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import PageSection from '@/components/layout/PageSection.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'

import ConsumptionRanking from './ConsumptionRanking.vue'
import GatewayConnection from './GatewayConnection.vue'
import HomeSummary from './HomeSummary.vue'
import HomeWelcome from './HomeWelcome.vue'
import { useHomeStatisticsPresenter } from './home-presenter'

const client = useApiClient()
const { locale, t } = useI18n()
const baseQuery = useQuery(homeBaseQueryOptions(client))
const statistics = useHomeStatisticsPresenter(client)
const serverClockOffsetMS = ref(0)
const nowMS = ref(Date.now())

watch(
  () => baseQuery.data.value,
  (base) => {
    if (base) serverClockOffsetMS.value = base.server_now_ms - Date.now()
  },
  { immediate: true },
)

const uptimeNowMS = computed(() => nowMS.value + serverClockOffsetMS.value)
const snapshot = computed(() => {
  const state = statistics.state.value
  return state.kind === 'initial' || state.kind === 'switching' ? null : state.snapshot
})
const statisticsLoading = computed(() => {
  const kind = statistics.state.value.kind
  return kind === 'initial' || kind === 'switching'
})
const isEmpty = computed(() => {
  const inventory = baseQuery.data.value?.inventory
  return (
    inventory !== undefined && inventory.group_count === 0 && inventory.upstream_key_count === 0
  )
})

const uptimeTimer = window.setInterval(() => {
  nowMS.value = Date.now()
}, 60_000)

onBeforeUnmount(() => window.clearInterval(uptimeTimer))
</script>

<template>
  <PageFrame aria-labelledby="home-title">
    <LedgerSheet>
      <div
        v-if="baseQuery.isPending.value"
        class="home-view__loading"
        :aria-label="t('home.ledger.loading')"
      >
        <SkeletonBlock height="2rem" />
        <SkeletonBlock height="7.5rem" />
        <SkeletonBlock height="12rem" />
      </div>

      <section
        v-else-if="baseQuery.isError.value"
        class="home-view__error"
        aria-labelledby="home-title"
      >
        <h1 id="home-title" class="home-view__title">{{ t('home.ledger.title') }}</h1>
        <QueryFeedback
          state="error"
          :message="t('home.ledger.baseError')"
          :retry-label="t('common.retry')"
          @retry="baseQuery.refetch()"
        />
      </section>

      <HomeWelcome v-else-if="isEmpty" />

      <template v-else-if="baseQuery.data.value">
        <HomeSummary
          :base="baseQuery.data.value"
          :statistics-state="statistics.state.value"
          :selected-range="statistics.selectedRange.value"
          :target-range="statistics.targetRange.value"
          :observed-at-ms="statistics.lastSuccessfulObservedAtMS.value"
          :uptime-now-ms="uptimeNowMS"
          @select-range="statistics.selectRange"
        />

        <div v-if="statistics.state.value.kind === 'stale'" class="home-view__statistics-feedback">
          <QueryFeedback
            state="stale"
            :message="t('home.ledger.statisticsError')"
            :retry-label="t('common.retry')"
            @retry="statistics.retry()"
          />
        </div>

        <PageSection
          v-if="snapshot"
          :title="t('home.ledger.trendTitle', { range: snapshot.range })"
        >
          <TrendChart
            :series="snapshot.series"
            :title="t('home.ledger.trendTitle', { range: snapshot.range })"
            :description="t('home.ledger.trendDescription')"
            :empty-label="t('home.ledger.trendEmpty')"
            :request-label="t('home.ledger.requestsLabel')"
            :failure-label="t('home.ledger.failuresLabel')"
            :range-start="snapshot.from_ms"
            :range-end="snapshot.to_ms"
            :locale="locale"
          />
        </PageSection>
        <PageSection
          v-else-if="statisticsLoading"
          :title="t('home.ledger.statisticsLoading')"
          class="home-view__statistics-section"
        >
          <SkeletonBlock height="12rem" :aria-label="t('home.ledger.statisticsLoading')" />
        </PageSection>

        <ConsumptionRanking v-if="snapshot" :rankings="snapshot.rankings" :range="snapshot.range" />
        <PageSection
          v-else-if="statisticsLoading"
          :title="t('home.ledger.statisticsLoading')"
          class="home-view__statistics-section"
        >
          <SkeletonBlock height="12rem" :aria-label="t('home.ledger.statisticsLoading')" />
        </PageSection>

        <GatewayConnection :access-keys="baseQuery.data.value.access_keys" />
      </template>
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.home-view__loading,
.home-view__error {
  display: grid;
  gap: var(--space-5);
  min-height: 420px;
}

.home-view__loading {
  align-content: start;
}

.home-view__title {
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--title-panel);
  font-weight: 500;
}

.home-view__statistics-feedback {
  padding-top: var(--space-4);
}

.home-view__statistics-section :deep(.page-section__content) {
  min-height: 12rem;
}
</style>
