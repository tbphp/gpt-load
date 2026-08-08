<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import { homeBaseQueryOptions, type HomeRange } from '@/app/resources/home'
import { homeLocation } from '@/app/route-locations'
import TrendChart from '@/components/charts/TrendChart.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import PageSection from '@/components/layout/PageSection.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonBlock from '@/components/ui/SkeletonBlock.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'

import ConsumptionRanking from './ConsumptionRanking.vue'
import GatewayConnection from './GatewayConnection.vue'
import HomeSummary from './HomeSummary.vue'
import HomeWelcome from './HomeWelcome.vue'
import { homeRangeLabelKey } from './home-range'
import { useHomeStatisticsPresenter } from './home-presenter'
import {
  isCanonicalHomeRouteQuery,
  parseHomeRouteQuery,
  serializeHomeRouteQuery,
  type HomeRankingDimension,
  type HomeRouteState,
} from './home-route'
import type { GatewayClientID } from './gateway-clients'

const client = useApiClient()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const baseQuery = useQuery(homeBaseQueryOptions(client))
const routeState = computed(() => parseHomeRouteQuery(route.query))
const statistics = useHomeStatisticsPresenter(client, { initialRange: routeState.value.range })
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
  return state.kind === 'initial' ? null : state.snapshot
})
const statisticsLoading = computed(() => {
  return statistics.state.value.kind === 'initial'
})
const statisticsSwitching = computed(() => statistics.state.value.kind === 'switching')
const baseLoading = useStableLoading(() => baseQuery.isPending.value)
const statisticsInitialLoading = useStableLoading(statisticsLoading)
const statisticsTransition = useStableLoading(statisticsSwitching)
const baseRefreshing = computed(
  () => baseQuery.data.value !== undefined && baseQuery.isFetching.value,
)
const homeRefreshing = computed(() => baseRefreshing.value || statistics.refreshing.value)
const displayedRange = computed(
  () => statistics.targetRange.value ?? snapshot.value?.range ?? statistics.selectedRange.value,
)
const isEmpty = computed(() => {
  const inventory = baseQuery.data.value?.inventory
  return (
    inventory !== undefined && inventory.group_count === 0 && inventory.upstream_key_count === 0
  )
})
const selectedAccessKeyID = computed(() => {
  const accessKeys = baseQuery.data.value?.access_keys ?? []
  const requested = routeState.value.accessKeyID
  return accessKeys.find(({ id }) => id === requested)?.id ?? accessKeys[0]?.id ?? null
})

watch(
  () => route.query,
  (query) => {
    const state = parseHomeRouteQuery(query)
    if (!isCanonicalHomeRouteQuery(query, state)) {
      void router.replace(homeLocation(serializeHomeRouteQuery(state)))
    }
  },
  { deep: true, immediate: true },
)

watch(
  () => baseQuery.data.value?.access_keys,
  (accessKeys) => {
    if (!accessKeys || routeState.value.accessKeyID === undefined) return
    if (accessKeys.some(({ id }) => id === routeState.value.accessKeyID)) return
    void navigate({ accessKeyID: undefined }, true)
  },
  { immediate: true },
)

watch(
  () => routeState.value.range,
  (range) => statistics.selectRange(range),
)

function navigate(patch: Partial<HomeRouteState>, replace = false): void {
  const next = { ...routeState.value, ...patch }
  const location = homeLocation(serializeHomeRouteQuery(next))
  void (replace ? router.replace(location) : router.push(location))
}

function selectRange(range: HomeRange): void {
  navigate({ range })
}

function selectRanking(dimension: HomeRankingDimension): void {
  navigate({ ranking: dimension })
}

function selectAccessKey(id: number): void {
  navigate({ accessKeyID: id })
}

function selectClient(clientID: GatewayClientID): void {
  navigate({ client: clientID })
}
function rangeLabel(range: HomeRange): string {
  return t(homeRangeLabelKey(range))
}

const uptimeTimer = window.setInterval(() => {
  nowMS.value = Date.now()
}, 60_000)

onBeforeUnmount(() => window.clearInterval(uptimeTimer))
</script>

<template>
  <PageFrame aria-labelledby="home-title">
    <LedgerSheet class="home-view__sheet" :class="{ 'home-view__sheet--welcome': isEmpty }">
      <AsyncRefreshIndicator :active="homeRefreshing" :label="t('home.ledger.loading')" />

      <SkeletonSurface
        v-if="baseQuery.isPending.value || baseLoading"
        variant="page"
        :concealed="!baseLoading"
        :label="t('home.ledger.loading')"
      />

      <section
        v-else-if="baseQuery.isError.value && !baseQuery.data.value"
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
        <QueryFeedback
          v-if="baseQuery.isError.value"
          state="stale"
          :message="t('home.ledger.baseError')"
          :retry-label="t('common.retry')"
          @retry="baseQuery.refetch()"
        />
        <HomeSummary
          :base="baseQuery.data.value"
          :statistics-state="statistics.state.value"
          :selected-range="statistics.selectedRange.value"
          :observed-at-ms="statistics.lastSuccessfulObservedAtMS.value"
          :uptime-now-ms="uptimeNowMS"
          :loading="statisticsTransition"
          @select-range="selectRange"
        />

        <section class="home-view__statistics-region home-view__statistics-region--trend">
          <PageSection
            v-if="snapshot"
            :title="t('home.ledger.trendTitle', { range: rangeLabel(displayedRange) })"
          >
            <SkeletonBlock
              v-if="statisticsTransition"
              class="home-view__trend-skeleton"
              height="auto"
              :aria-label="t('home.ledger.statisticsLoading')"
            />
            <TrendChart
              v-else
              :series="snapshot.series"
              :title="t('home.ledger.trendTitle', { range: rangeLabel(snapshot.range) })"
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
            v-else-if="statisticsLoading || statisticsInitialLoading"
            :title="t('home.ledger.statisticsLoading')"
            class="home-view__statistics-section"
          >
            <SkeletonBlock
              height="12rem"
              :concealed="!statisticsInitialLoading"
              :aria-label="t('home.ledger.statisticsLoading')"
            />
          </PageSection>
        </section>

        <section class="home-view__statistics-region home-view__statistics-region--ranking">
          <ConsumptionRanking
            v-if="snapshot"
            :rankings="snapshot.rankings"
            :range="rangeLabel(displayedRange)"
            :dimension="routeState.ranking"
            :loading="statisticsTransition"
            @update:dimension="selectRanking"
          />
          <PageSection
            v-else-if="statisticsLoading || statisticsInitialLoading"
            :title="t('home.ledger.statisticsLoading')"
            class="home-view__statistics-section"
          >
            <SkeletonBlock
              height="17rem"
              :concealed="!statisticsInitialLoading"
              :aria-label="t('home.ledger.statisticsLoading')"
            />
          </PageSection>
        </section>

        <GatewayConnection
          :access-keys="baseQuery.data.value.access_keys"
          :selected-access-key-id="selectedAccessKeyID"
          :client-id="routeState.client"
          @update:selected-access-key-id="selectAccessKey"
          @update:client-id="selectClient"
        />
      </template>
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.home-view__error {
  display: grid;
  gap: var(--space-5);
  min-height: 420px;
}

.home-view__title {
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--title-panel);
  font-weight: 500;
}

.home-view__statistics-section :deep(.page-section__content) {
  min-height: 12rem;
}

.home-view__statistics-region--trend :deep(.page-section__header) {
  margin-bottom: 10px;
}

.home-view__trend-skeleton {
  aspect-ratio: var(--chart-aspect-ratio);
}

.home-view__sheet--welcome {
  min-height: 560px;
}

@media (max-width: 860px) {
  .home-view__sheet--welcome {
    min-height: 0;
  }

  .home-view__sheet {
    border-radius: 9px;
  }
}
</style>
