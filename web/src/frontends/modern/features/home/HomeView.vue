<script setup lang="ts">
import { Boxes, KeyRound, Layers2 } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { getHome, getHomeAccounts } from '@modern/api/home'
import { getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import { getLogAccessKeys } from '@modern/api/logs'
import { useApiClient } from '@shared/http/client-context'
import { useAuthSession } from '@modern/features/auth/auth-session'
import { usePageRefresh } from '@modern/app/page-refresh'
import { AppButton, AppCollectionState, AppIcon, AppPanel } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import RouteInspector from '@modern/features/inspector/RouteInspector.vue'
import HomeAccessKey from './HomeAccessKey.vue'
import HomeAccounts from './HomeAccounts.vue'
import HomeSetupGuide from './HomeSetupGuide.vue'
import GatewayConnection from './GatewayConnection.vue'

const { t, n, locale } = useI18n()
const client = useApiClient()
const session = useAuthSession()
const route = useRoute()
const inspector = ref<InstanceType<typeof RouteInspector>>()
const inspectorSection = ref<HTMLElement>()
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
const uptime = computed(() => {
  const hours = base.value
    ? Math.floor(Math.max(0, base.value.observedAt - base.value.startedAt) / 3600000)
    : 0
  return t('home.uptime', { days: n(Math.floor(hours / 24)), hours: n(hours % 24) })
})
const inventory = computed(() =>
  base.value
    ? [
        {
          key: 'groups',
          icon: Layers2,
          value: base.value.groups,
          route: admin.value ? 'modern-groups' : undefined,
        },
        { key: 'models', icon: Boxes, value: base.value.models, route: 'modern-models' },
        ...(admin.value
          ? [
              {
                key: 'credentials',
                icon: KeyRound,
                value: base.value.available,
                suffix: n(base.value.credentials),
                route: 'modern-health',
              },
              {
                key: 'keys',
                icon: KeyRound,
                value: base.value.keys.length,
                route: 'modern-access-keys',
              },
            ]
          : []),
      ]
    : [],
)
async function refreshOptions(): Promise<void> {
  await Promise.all([groups.refetch(), keys.refetch()])
}
async function refresh(): Promise<void> {
  await Promise.all([
    baseQuery.refetch(),
    ...(admin.value ? [refreshOptions(), accounts.refetch(), inspector.value?.refresh()] : []),
  ])
}
usePageRefresh({
  refresh,
  pending: () =>
    baseQuery.isFetching.value ||
    groups.isFetching.value ||
    keys.isFetching.value ||
    accounts.isFetching.value ||
    Boolean(inspector.value?.pending),
  updatedAt: () =>
    Math.max(
      base.value?.observedAt ?? 0,
      accounts.data.value?.observedAt ?? 0,
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
    await nextTick()
    if (route.hash !== hash || !sectionReady.value) return
    inspectorSection.value?.scrollIntoView({ block: 'start' })
    inspectorSection.value?.focus({ preventScroll: true })
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
      <section class="modern-home-overview" :aria-label="t('home.overview')">
        <div class="modern-home-overview-heading">
          <div>
            <span class="modern-home-eyebrow"
              >GPT-Load <span>{{ base.version }}</span></span
            >
            <h2>{{ t('home.overview') }}</h2>
          </div>
          <span class="modern-home-overview-meta">{{ uptime }}</span>
        </div>
        <dl class="modern-home-inventory">
          <div v-for="item in inventory" :key="item.key">
            <dt><AppIcon :icon="item.icon" size="sm" />{{ t('home.' + item.key) }}</dt>
            <dd>
              <AppButton v-if="item.route" as-child variant="text">
                <RouterLink :to="{ name: item.route }">{{
                  formatCompactNumber(item.value, locale)
                }}</RouterLink>
              </AppButton>
              <span v-else>{{ formatCompactNumber(item.value, locale) }}</span>
              <small v-if="'suffix' in item">/ {{ item.suffix }}</small>
            </dd>
          </div>
        </dl>
      </section>
      <HomeSetupGuide
        v-if="showSetup && groups.data.value && keys.data.value"
        :groups="groups.data.value.items"
        :key-count="keys.data.value.length"
      />
      <HomeAccessKey v-if="!admin && base.currentKey" :row="base.currentKey" />
      <div v-if="admin && accounts.isError.value" class="modern-home-error" role="status">
        <span>{{ t('home.accountsFailed') }}</span>
        <AppButton size="xs" @click="accounts.refetch()">{{ t('ui.retry') }}</AppButton>
      </div>
      <HomeAccounts
        v-if="admin && accounts.data.value?.items.length"
        :accounts="accounts.data.value.items"
      />
      <GatewayConnection
        v-if="!admin || base.keys.length || (groups.data.value && keys.data.value && !showSetup)"
        :keys="base.keys"
        :admin="admin"
      />
      <div
        v-if="admin"
        id="route-inspector"
        ref="inspectorSection"
        class="modern-home-inspector"
        tabindex="-1"
      >
        <AppPanel :title="t('pages.inspector.title')" compact>
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
        </AppPanel>
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
.modern-home-overview {
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-subtle);
  padding: var(--modern-space-5);
}
.modern-home-overview-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-4);
}
.modern-home-eyebrow {
  display: flex;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
.modern-home-eyebrow > span {
  font-family: var(--modern-font-mono);
}
.modern-home-overview-heading h2 {
  margin-top: var(--modern-space-2);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-home-overview-meta {
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
.modern-home-inventory {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: var(--modern-space-5);
  margin: var(--modern-space-5) 0 0;
}
.modern-home-inventory dt {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-home-inventory dd {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-2);
  margin: var(--modern-space-1) 0 0;
  font-size: var(--modern-font-size-title);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-home-inventory small {
  font-size: var(--modern-font-size-secondary);
  color: var(--modern-muted);
  font-weight: var(--modern-weight-regular);
}
.modern-home-inspector {
  min-width: 0;
  scroll-margin-top: var(--modern-space-5);
}
.modern-home-inspector-empty {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
</style>
