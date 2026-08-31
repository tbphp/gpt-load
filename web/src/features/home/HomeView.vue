<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import { healthQueryOptions } from '@/app/resources/health'
import { homeBaseQueryOptions, homeSubscriptionAccountsQueryOptions } from '@/app/resources/home'
import { systemUpdateQueryOptions } from '@/app/resources/system-update'
import { homeLocation } from '@/app/route-locations'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import { useAuthSession } from '@/features/auth/auth-session'

import CurrentAccessKeyCard from './CurrentAccessKeyCard.vue'
import GatewayConnection from './GatewayConnection.vue'
import HomeAttention from './HomeAttention.vue'
import HomeSpend from './HomeSpend.vue'
import HomeSubscriptionAccounts from './HomeSubscriptionAccounts.vue'
import HomeSummary from './HomeSummary.vue'
import HomeWelcome from './HomeWelcome.vue'
import { useHomeStatisticsPresenter } from './home-presenter'
import {
  isCanonicalHomeRouteQuery,
  parseHomeRouteQuery,
  serializeHomeRouteQuery,
  type HomeRouteState,
} from './home-route'
import type { GatewayClientID } from './gateway-clients'

const client = useApiClient()
const session = useAuthSession()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const isAccessKey = computed(() => session.state.principalType === 'access_key')
const isAdmin = computed(() => session.state.principalType === 'admin')
const baseQuery = useQuery(homeBaseQueryOptions(client))
// 订阅账号包含完整管理身份和额度，只允许管理员发起查询；模板仍二次 gate，
// 防止会话切换时短暂复用旧 Query 缓存。
const subscriptionAccountsQuery = useQuery(homeSubscriptionAccountsQueryOptions(client, isAdmin))
// 更新检查与首页数据解耦，仅由管理员进入首页时按需触发一次。
const updateQuery = useQuery(
  systemUpdateQueryOptions(client, () => session.state.principalType === 'admin'),
)
// /api/health 不在 AccessKey 白名单里，必须前端主动 gate，
// 否则 AccessKey 用户首页会挂一个永远 403 的区块。首页不轮询，进页面拉一次即可。
const healthQuery = useQuery(healthQueryOptions(client, undefined, () => !isAccessKey.value))
const routeState = computed<HomeRouteState>(() => {
  const state = parseHomeRouteQuery(route.query)
  return isAccessKey.value ? { ...state, accessKeyID: undefined } : state
})
// 花费固定看近 30 天：首页不再提供时间旋钮，那个控件本身就是「我是仪表盘」的宣言。
const statistics = useHomeStatisticsPresenter(client, { initialRange: '30d' })
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
const releaseUpdate = computed(() =>
  updateQuery.isSuccess.value && !updateQuery.isFetching.value
    ? (updateQuery.data.value?.update ?? null)
    : null,
)
const snapshot = computed(() => {
  const state = statistics.state.value
  return state.kind === 'initial' ? null : state.snapshot
})
const statisticsLoading = computed(() => {
  return statistics.state.value.kind === 'initial'
})
const baseLoading = useStableLoading(() => baseQuery.isPending.value)
const statisticsInitialLoading = useStableLoading(statisticsLoading)
const baseRefreshing = computed(
  () => baseQuery.data.value !== undefined && baseQuery.isFetching.value,
)
const homeRefreshing = computed(() => baseRefreshing.value || statistics.refreshing.value)
const isEmpty = computed(() => {
  const inventory = baseQuery.data.value?.inventory
  return inventory !== undefined && inventory.group_count === 0 && inventory.credential_count === 0
})
const selectedAccessKeyID = computed(() => {
  const accessKeys = baseQuery.data.value?.access_keys ?? []
  const requested = routeState.value.accessKeyID
  return accessKeys.find(({ id }) => id === requested)?.id ?? accessKeys[0]?.id ?? null
})

watch(
  () => route.query,
  (query) => {
    const parsed = parseHomeRouteQuery(query)
    const state = isAccessKey.value ? { ...parsed, accessKeyID: undefined } : parsed
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

function navigate(patch: Partial<HomeRouteState>, replace = false): void {
  const next = { ...routeState.value, ...patch }
  const location = homeLocation(serializeHomeRouteQuery(next))
  void (replace ? router.replace(location) : router.push(location))
}

function selectAccessKey(id: number): void {
  if (isAccessKey.value) return
  navigate({ accessKeyID: id })
}

function selectClient(clientID: GatewayClientID): void {
  navigate({ client: clientID })
}

const uptimeTimer = window.setInterval(() => {
  nowMS.value = Date.now()
}, 60_000)

onBeforeUnmount(() => window.clearInterval(uptimeTimer))
</script>

<template>
  <PageFrame aria-labelledby="home-title">
    <LedgerSheet
      class="home-view__sheet"
      :class="{ 'home-view__sheet--welcome': isEmpty && !isAccessKey }"
    >
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

      <HomeWelcome
        v-else-if="isEmpty && !isAccessKey && baseQuery.data.value"
        :base="baseQuery.data.value"
        :update="releaseUpdate"
      />

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
          :update="releaseUpdate"
          :observed-at-ms="statistics.lastSuccessfulObservedAtMS.value"
          :uptime-now-ms="uptimeNowMS"
        />

        <!-- 紧贴事实行：它是那句「X/Y 个凭据可用」的注解，隔开就变成孤立的红条。 -->
        <HomeAttention v-if="!isAccessKey" :health="healthQuery.data.value ?? null" />

        <HomeSubscriptionAccounts
          v-if="isAdmin && subscriptionAccountsQuery.data.value?.items.length"
          :accounts="subscriptionAccountsQuery.data.value"
        />

        <CurrentAccessKeyCard
          v-if="baseQuery.data.value.current_access_key"
          :access-key="baseQuery.data.value.current_access_key"
        />

        <GatewayConnection
          :access-keys="baseQuery.data.value.access_keys"
          :selected-access-key-id="selectedAccessKeyID"
          :client-id="routeState.client"
          :credential="isAccessKey ? session.getAuthKey() : undefined"
          :self-scoped="isAccessKey"
          @update:selected-access-key-id="selectAccessKey"
          @update:client-id="selectClient"
        />

        <HomeSpend :snapshot="snapshot" :loading="statisticsLoading || statisticsInitialLoading" />
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
