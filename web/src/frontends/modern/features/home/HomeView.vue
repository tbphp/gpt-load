<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { getHome, getHomeAccounts, getHomeStatistics, homeStatisticsKey } from '@modern/api/home'
import { getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import { getHealth } from '@modern/api/health'
import { getLogAccessKeys } from '@modern/api/logs'
import { useApiClient } from '@shared/http/client-context'
import { useAuthSession } from '@modern/features/auth/auth-session'
import { usePageRefresh } from '@modern/app/page-refresh'
import { AppButton, AppCollectionState, AppNotice } from '@modern/components/ui'
import RouteInspector from '@modern/features/inspector/RouteInspector.vue'
import HomeAccessKey from './HomeAccessKey.vue'
import HomeAccounts from './HomeAccounts.vue'
import HomeAttention from './HomeAttention.vue'
import HomeRouteTool from './HomeRouteTool.vue'
import HomeSetupGuide from './HomeSetupGuide.vue'
import HomeStatusBar from './HomeStatusBar.vue'
import HomeTrend from './HomeTrend.vue'
import { collectAttention } from './home-attention'
import GatewayConnection from './GatewayConnection.vue'
import type { GatewaySelection } from './gateway-config'

const { t } = useI18n()
const client = useApiClient()
const session = useAuthSession()
const route = useRoute()
const router = useRouter()
const inspector = ref<InstanceType<typeof RouteInspector>>()
const inspectorSection = ref<HTMLElement>()
const attentionSection = ref<HTMLElement>()
const connectionSelection = ref<GatewaySelection>()
const admin = computed(() => session.state.principalType === 'admin')
const baseQuery = useQuery({
  queryKey: ['modern', 'home', 'base'],
  queryFn: ({ signal }) => getHome(client, signal),
})
const base = computed(() => baseQuery.data.value)
const groups = useQuery({
  queryKey: groupQueryKey,
  queryFn: ({ signal }) => getGroupWorkspace(client, signal),
  enabled: admin,
})
const keys = useQuery({
  queryKey: ['modern', 'log-access-key-options'],
  queryFn: ({ signal }) => getLogAccessKeys(client, signal),
  enabled: admin,
})
const accounts = useQuery({
  queryKey: ['modern', 'home', 'accounts'],
  queryFn: ({ signal }) => getHomeAccounts(client, signal),
  enabled: admin,
})
const statistics = useQuery({
  queryKey: homeStatisticsKey,
  queryFn: ({ signal }) => getHomeStatistics(client, signal),
})
/* 需要处理读运行健康快照：分组列表只有凭据计数，推不出额度将尽、
   充值卡临期、访问密钥被费用额度挡住这几类。 */
const health = useQuery({
  queryKey: ['modern', 'health'],
  queryFn: ({ signal }) => getHealth(client, signal),
  enabled: admin,
})
// 初始化只看配置是否齐全，不把停用、冷却等运行状态当成尚未配置。
const showSetup = computed(() => {
  if (!admin.value || !groups.data.value || !keys.data.value) return false
  return (
    !groups.data.value.items.some((group) => group.credentials.total > 0 && group.modelCount > 0) ||
    !keys.data.value.length
  )
})
const emptyProject = computed(
  () => admin.value && groups.data.value?.items.length === 0 && keys.data.value?.length === 0,
)
const attention = computed(() => (admin.value ? collectAttention(health.data.value).length : 0))
// 深链接 /monitor/inspector 会重定向到首页并带上 inspect_* 参数，那种情况直接展开。
const inspectorOpen = ref(
  route.hash === '#route-inspector' ||
    Object.keys(route.query).some((key) => key.startsWith('inspect_')),
)
async function toggleInspector(open: boolean): Promise<void> {
  const selection = connectionSelection.value
  if (open && selection && !Object.keys(route.query).some((key) => key.startsWith('inspect_'))) {
    await router.replace({
      query: {
        ...route.query,
        inspect_protocol: selection.protocol || undefined,
        inspect_external_model: selection.model || undefined,
        inspect_access_key_id: selection.accessKeyID ? String(selection.accessKeyID) : undefined,
      },
    })
  }
  inspectorOpen.value = open
}
async function refreshOptions(): Promise<void> {
  await Promise.all([groups.refetch(), keys.refetch()])
}
async function refresh(): Promise<void> {
  await Promise.all([
    baseQuery.refetch(),
    statistics.refetch(),
    ...(admin.value
      ? [refreshOptions(), accounts.refetch(), health.refetch(), inspector.value?.refresh()]
      : []),
  ])
}
usePageRefresh({
  refresh,
  pending: () =>
    baseQuery.isFetching.value ||
    statistics.isFetching.value ||
    groups.isFetching.value ||
    keys.isFetching.value ||
    accounts.isFetching.value ||
    health.isFetching.value ||
    Boolean(inspector.value?.pending),
  updatedAt: () =>
    Math.max(
      base.value?.observedAt ?? 0,
      statistics.data.value?.observedAt ?? 0,
      accounts.data.value?.observedAt ?? 0,
      health.data.value?.observedAt ?? 0,
      inspector.value?.updatedAt ?? 0,
    ) || undefined,
})
const sectionReady = computed(
  () =>
    admin.value &&
    Boolean(base.value) &&
    !groups.isPending.value &&
    !keys.isPending.value &&
    !accounts.isPending.value,
)
watch(
  [() => route.hash, sectionReady],
  async ([hash, ready]) => {
    if (hash !== '#route-inspector' || !ready) return
    inspectorOpen.value = true
    await nextTick()
    if (route.hash !== hash || !sectionReady.value) return
    inspectorSection.value?.scrollIntoView({ block: 'start' })
    inspectorSection.value?.focus({ preventScroll: true })
  },
  { immediate: true },
)
watch(
  [() => route.hash, () => Boolean(base.value)],
  async ([hash, ready]) => {
    if (hash !== '#home-attention' || !ready || !admin.value) return
    await nextTick()
    if (route.hash !== hash) return
    attentionSection.value?.scrollIntoView({ block: 'start' })
    attentionSection.value?.focus({ preventScroll: true })
  },
  { immediate: true },
)
</script>

<template>
  <div class="modern-home-workspace">
    <AppCollectionState
      v-if="!base"
      :loading="baseQuery.isPending.value"
      :error="baseQuery.isError.value"
      :title="t(baseQuery.isError.value ? 'home.baseFailed' : 'ui.loading')"
    >
      <AppButton v-if="baseQuery.isError.value" @click="refresh">{{ t('ui.retry') }}</AppButton>
    </AppCollectionState>
    <template v-else>
      <div v-if="baseQuery.isError.value" class="modern-home-error" role="status">
        <span>{{ t('home.baseFailed') }}</span>
        <AppButton size="xs" @click="baseQuery.refetch()">{{ t('ui.retry') }}</AppButton>
      </div>
      <HomeStatusBar :base="base" :admin="admin" :attention="attention" />
      <AppNotice v-if="admin && (groups.isError.value || keys.isError.value)" tone="warning">
        <div class="modern-home-options-error">
          <span>{{ t('home.setupFailed') }}</span>
          <AppButton size="xs" variant="text" @click="refreshOptions">{{
            t('ui.retry')
          }}</AppButton>
        </div>
      </AppNotice>
      <HomeSetupGuide
        v-if="showSetup && groups.data.value && keys.data.value"
        :groups="groups.data.value.items"
        :key-count="keys.data.value.length"
      />
      <div class="modern-home-columns">
        <div class="modern-home-main">
          <GatewayConnection
            :keys="base.keys"
            :admin="admin"
            @selection="connectionSelection = $event"
          />
          <div
            v-if="admin"
            id="route-inspector"
            ref="inspectorSection"
            class="modern-home-inspector"
            tabindex="-1"
          >
            <HomeRouteTool
              :open="inspectorOpen"
              :selection="connectionSelection"
              @update:open="toggleInspector"
            >
              <p v-if="emptyProject" class="modern-home-inspector-empty">
                {{ t('home.setup.inspectorLater') }}
              </p>
              <RouteInspector
                v-else
                ref="inspector"
                :groups="groups.data.value"
                :groups-loading="groups.isFetching.value"
                :groups-failed="groups.isError.value"
                :access-keys="keys.data.value"
                :keys-loading="keys.isFetching.value"
                :keys-failed="keys.isError.value"
                @retry-options="refreshOptions"
              />
            </HomeRouteTool>
          </div>
        </div>
        <div class="modern-home-side">
          <HomeAccounts
            v-if="admin"
            :accounts="accounts.data.value?.items ?? []"
            :failed="accounts.isError.value"
            :loading="accounts.isPending.value"
            @retry="accounts.refetch()"
          />
          <div
            v-if="admin"
            id="home-attention"
            ref="attentionSection"
            class="modern-home-attention-section"
            tabindex="-1"
          >
            <HomeAttention
              :report="health.data.value"
              :failed="health.isError.value"
              @retry="health.refetch()"
            />
          </div>
          <HomeAccessKey v-if="!admin && base.currentKey" :row="base.currentKey" />
          <HomeTrend
            :report="statistics.data.value"
            :failed="statistics.isError.value"
            @retry="statistics.refetch()"
          />
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.modern-home-workspace {
  display: grid;
  flex: none;
  align-content: start;
  min-width: 0;
  gap: var(--modern-page-gap);
  padding-block: var(--modern-space-5);
}
.modern-home-error {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  color: var(--modern-danger);
  font-size: var(--modern-font-size-secondary);
}
.modern-home-options-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3);
}
.modern-home-columns {
  display: grid;
  grid-template-columns: minmax(0, 1fr) var(--modern-home-side);
  align-items: start;
  gap: var(--modern-page-gap);
}
.modern-home-main,
.modern-home-side {
  display: grid;
  align-content: start;
  min-width: 0;
  gap: var(--modern-page-gap);
}
.modern-home-inspector,
.modern-home-attention-section {
  min-width: 0;
  scroll-margin-top: var(--modern-space-5);
}
.modern-home-inspector-empty {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
@media (max-width: 1150px) {
  .modern-home-columns {
    grid-template-columns: minmax(0, 1fr);
  }
  .modern-home-side {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    align-items: start;
  }
}
@media (max-width: 760px) {
  .modern-home-side {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
