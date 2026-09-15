<script setup lang="ts">
import {
  ArrowRight,
  Boxes,
  KeyRound,
  Layers2,
  Plus,
  Route,
  ScrollText,
  Settings2,
} from '@lucide/vue'
import { keepPreviousData, useQuery } from '@tanstack/vue-query'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRouter } from 'vue-router'
import { getHome, getHomeAccounts } from '@modern/api/home'
import { getUsage, type UsageItem } from '@modern/api/usage'
import { getHealth } from '@modern/api/health'
import { getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import { useApiClient } from '@shared/http/client-context'
import { useAuthSession } from '@modern/features/auth/auth-session'
import { usePageRefresh } from '@modern/app/page-refresh'
import { useURLState } from '@modern/app/url-state'
import { resolveTimeRange } from '@modern/app/time-range'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppIcon,
  AppOverflowText,
  AppPanel,
  AppSegmentedControl,
  AppTooltip,
} from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import { healthIssues, healthManageLocation } from '@modern/features/health/health-display'
import { trendMetrics, type TrendMetric } from '@modern/features/usage/usage-state'
import UsageMetrics from '@modern/features/usage/UsageMetrics.vue'
import UsageTrend from '@modern/features/usage/UsageTrend.vue'
import UsageRank from '@modern/features/usage/UsageRank.vue'
import HomeAccessKey from './HomeAccessKey.vue'
import HomeAccounts from './HomeAccounts.vue'
import GatewayConnection from './GatewayConnection.vue'

const { t, n, locale } = useI18n()
const client = useApiClient()
const session = useAuthSession()
const router = useRouter()
const admin = computed(() => session.state.principalType === 'admin')
const state = useURLState(
  ['preset'],
  (query) => ({ preset: query.preset === '30d' ? ('30d' as const) : ('24h' as const) }),
  (value) => ({ preset: value.preset === '24h' ? undefined : value.preset }),
)
const baseQuery = useQuery({
  queryKey: ['modern', 'home', 'base'],
  queryFn: ({ signal }) => getHome(client, signal),
})
const base = computed(() => baseQuery.data.value)
const usage = useQuery(
  computed(() => {
    const range = { preset: state.value.preset }
    return {
      queryKey: ['modern', 'usage', admin.value, {}, range],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        getUsage(client, resolveTimeRange(range), signal),
      placeholderData: keepPreviousData,
    }
  }),
)
const health = useQuery({
  queryKey: ['modern', 'health'],
  queryFn: ({ signal }) => getHealth(client, signal),
  enabled: admin,
})
const groups = useQuery({
  queryKey: groupQueryKey,
  queryFn: ({ signal }) => getGroupWorkspace(client, signal),
  enabled: admin,
})
const accounts = useQuery({
  queryKey: ['modern', 'home', 'accounts'],
  queryFn: ({ signal }) => getHomeAccounts(client, signal),
  enabled: admin,
})
const issues = computed(() =>
  health.data.value && groups.data.value
    ? healthIssues(health.data.value, groups.data.value.items, (key, values) =>
        t(key, values ?? {}),
      ).sort((a, b) => a.priority - b.priority || a.name.localeCompare(b.name))
    : [],
)
const healthReady = computed(
  () => !health.isError.value && !groups.isError.value && health.data.value && groups.data.value,
)
const healthFailed = computed(() => health.isError.value || groups.isError.value)
const trend = ref<TrendMetric>('requests')
const trendOptions = computed(() =>
  trendMetrics.map((value) => ({ value, label: t('usage.' + value) })),
)
const periods = computed(() =>
  ['24h', '30d'].map((value) => ({ value, label: t('ui.date.ranges.' + value) })),
)
const period = computed(() => {
  const report = usage.data.value
  if (!report) return ''
  const format = dateFormatter(locale.value, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  })
  return `${format.format(report.from_ms)} – ${format.format(report.to_ms)}`
})
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
const shortcuts = computed(() => [
  { route: 'modern-models', label: t('home.viewModels'), icon: Boxes },
  { route: 'modern-logs', label: t('home.viewLogs'), icon: ScrollText },
  ...(admin.value
    ? [
        { route: 'modern-inspector', label: t('home.inspect'), icon: Route },
        { route: 'modern-settings', label: t('home.settings'), icon: Settings2 },
      ]
    : []),
])
async function refresh(): Promise<void> {
  await Promise.all([
    baseQuery.refetch(),
    usage.refetch(),
    ...(admin.value ? [health.refetch(), groups.refetch(), accounts.refetch()] : []),
  ])
}
usePageRefresh({
  refresh,
  pending: () =>
    baseQuery.isFetching.value ||
    usage.isFetching.value ||
    health.isFetching.value ||
    groups.isFetching.value ||
    accounts.isFetching.value,
  updatedAt: () =>
    Math.max(base.value?.observedAt ?? 0, usage.data.value?.observed_at_ms ?? 0) || undefined,
})
function drill(row: UsageItem, logs = false): void {
  if (!row.model || usage.isPlaceholderData.value) return
  void router.push({
    name: logs ? 'modern-logs' : 'modern-usage',
    query: { preset: state.value.preset, upstream_model: row.model },
  })
}
</script>

<template>
  <div class="modern-home-workspace">
    <div class="modern-home-toolbar">
      <AppSegmentedControl
        :model-value="state.preset"
        :label="t('home.period')"
        :options="periods"
        appearance="field"
        size="sm"
        @update:model-value="state.preset = $event === '30d' ? '30d' : '24h'"
      />
      <div class="modern-home-shortcuts">
        <AppButton v-for="item in shortcuts" :key="item.route" as-child size="sm" variant="text"
          ><RouterLink :to="{ name: item.route }"
            ><AppIcon :icon="item.icon" size="sm" />{{ item.label }}</RouterLink
          ></AppButton
        >
      </div>
    </div>
    <AppCollectionState
      v-if="!base"
      :loading="baseQuery.isPending.value"
      :error="baseQuery.isError.value"
      :title="t(baseQuery.isError.value ? 'home.baseFailed' : 'ui.loading')"
      ><AppButton v-if="baseQuery.isError.value" @click="refresh">{{
        t('ui.retry')
      }}</AppButton></AppCollectionState
    >
    <div v-else class="modern-home-body">
      <div v-if="baseQuery.isError.value" class="modern-home-error" role="status">
        <span>{{ t('home.baseFailed') }}</span
        ><AppButton size="xs" @click="baseQuery.refetch()">{{ t('ui.retry') }}</AppButton>
      </div>
      <section class="modern-home-overview" :aria-label="t('home.overview')">
        <div class="modern-home-overview-heading">
          <div>
            <span class="modern-home-eyebrow"
              >GPT-Load <span>{{ base.version }}</span></span
            >
            <p>{{ admin && !base.groups ? t('home.welcome') : t('home.overview') }}</p>
          </div>
          <div class="modern-home-overview-meta">
            <span>{{ uptime }}</span>
            <AppBadge
              v-if="admin && healthReady"
              :tone="issues.length ? 'warning' : 'success'"
              dot
              size="xs"
              >{{
                issues.length ? t('home.attention', { count: n(issues.length) }) : t('home.healthy')
              }}</AppBadge
            >
            <AppBadge v-else-if="admin" :tone="healthFailed ? 'warning' : 'neutral'" size="xs">{{
              t(healthFailed ? 'home.healthFailed' : 'ui.loading')
            }}</AppBadge>
          </div>
        </div>
        <dl class="modern-home-inventory">
          <div v-for="item in inventory" :key="item.key">
            <dt><AppIcon :icon="item.icon" size="sm" />{{ t('home.' + item.key) }}</dt>
            <dd>
              <AppButton v-if="item.route" as-child variant="text"
                ><RouterLink :to="{ name: item.route }">{{
                  formatCompactNumber(item.value, locale)
                }}</RouterLink></AppButton
              ><span v-else>{{ formatCompactNumber(item.value, locale) }}</span
              ><small v-if="'suffix' in item">/ {{ item.suffix }}</small>
            </dd>
          </div>
        </dl>
        <div v-if="admin && !base.groups" class="modern-home-onboarding">
          <p>{{ t('home.welcomeHelp') }}</p>
          <AppButton as-child variant="primary" size="sm"
            ><RouterLink :to="{ name: 'modern-groups', query: { panel: 'create' } }"
              ><AppIcon :icon="Plus" size="sm" />{{ t('home.addGroup') }}</RouterLink
            ></AppButton
          >
        </div>
      </section>
      <HomeAccessKey v-if="!admin && base.currentKey" :row="base.currentKey" />
      <div v-if="usage.isError.value" class="modern-home-error" role="status">
        <span>{{ t('usage.failed') }}</span
        ><AppButton size="xs" @click="usage.refetch()">{{ t('ui.retry') }}</AppButton>
      </div>
      <AppCollectionState
        v-if="!usage.data.value"
        :loading="usage.isPending.value"
        :error="usage.isError.value"
        :title="t(usage.isError.value ? 'usage.failed' : 'usage.loading')"
      />
      <template v-else>
        <div
          :class="{ 'modern-home-updating': usage.isPlaceholderData.value }"
          :aria-busy="usage.isFetching.value"
        >
          <UsageMetrics :report="usage.data.value" />
        </div>
        <div class="modern-home-charts" :aria-busy="usage.isFetching.value">
          <AppPanel :title="t('usage.trend')" compact>
            <template #actions
              ><AppSegmentedControl
                :model-value="trend"
                :label="t('usage.trend')"
                :options="trendOptions"
                size="sm"
                appearance="field"
                @update:model-value="
                  trend = trendMetrics.find((value) => value === $event) ?? 'requests'
                "
            /></template>
            <div class="modern-home-chart-context">
              <span>{{ period }}</span>
              <AppTooltip
                v-if="
                  usage.data.value.collectionIncomplete ||
                  usage.data.value.summary.usage_missing_count ||
                  usage.data.value.summary.partial_count
                "
                :label="
                  t('usage.incompleteHint', {
                    missing: formatCompactNumber(
                      usage.data.value.summary.usage_missing_count,
                      locale,
                    ),
                    partial: formatCompactNumber(usage.data.value.summary.partial_count, locale),
                  })
                "
                ><AppBadge tone="warning" size="xs" tabindex="0">{{
                  t('usage.incomplete')
                }}</AppBadge></AppTooltip
              >
              <AppButton as-child variant="text" size="xs"
                ><RouterLink :to="{ name: 'modern-usage', query: { preset: state.preset } }"
                  >{{ t('home.viewUsage') }}<AppIcon :icon="ArrowRight" size="xs" /></RouterLink
              ></AppButton>
            </div>
            <UsageTrend :report="usage.data.value" :metric="trend" />
          </AppPanel>
          <div class="modern-home-ranking">
            <UsageRank
              dimension="model"
              :distribution="usage.data.value.distributions.model!.requests"
              metric="requests"
              :groups="[]"
              :access-keys="[]"
              :cost-unavailable="
                usage.data.value.summary.estimated_cost_nano_usd === '0' &&
                !!(
                  usage.data.value.summary.unpriced_request_count ||
                  usage.data.value.summary.pricing_partial_count
                )
              "
              :disabled="usage.isPlaceholderData.value"
              @select="drill"
              @logs="drill($event, true)"
            />
          </div>
        </div>
      </template>
      <AppPanel
        v-if="admin && (issues.length || healthFailed)"
        :title="t('pages.health.title')"
        compact
      >
        <template #actions
          ><AppButton as-child variant="text" size="sm"
            ><RouterLink :to="{ name: 'modern-health' }"
              >{{ t('home.viewHealth')
              }}<AppIcon :icon="ArrowRight" size="sm" /></RouterLink></AppButton
        ></template>
        <div v-if="healthFailed" class="modern-home-error" role="status">
          <span>{{ t('home.healthFailed') }}</span
          ><AppButton size="xs" @click="refresh">{{ t('ui.retry') }}</AppButton>
        </div>
        <div class="modern-home-issues">
          <AppButton v-for="issue in issues.slice(0, 4)" :key="issue.key" as-child variant="text"
            ><RouterLink :to="healthManageLocation(issue)" class="modern-home-issue"
              ><AppBadge :tone="issue.severity" size="xs" dot>{{
                t('health.kinds.' + issue.kind)
              }}</AppBadge
              ><AppOverflowText :text="issue.name" /><span>{{ issue.reason }}</span
              ><AppIcon :icon="ArrowRight" size="sm" /></RouterLink
          ></AppButton>
        </div>
      </AppPanel>
      <div v-if="admin && accounts.isError.value" class="modern-home-error" role="status">
        <span>{{ t('home.accountsFailed') }}</span
        ><AppButton size="xs" @click="accounts.refetch()">{{ t('ui.retry') }}</AppButton>
      </div>
      <HomeAccounts
        v-if="admin && accounts.data.value?.items.length"
        :accounts="accounts.data.value.items"
      />
      <GatewayConnection :keys="base.keys" :admin="admin" />
    </div>
  </div>
</template>

<style scoped>
.modern-home-workspace {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}
.modern-home-toolbar {
  display: flex;
  flex-wrap: wrap;
  flex: none;
  justify-content: space-between;
  align-items: center;
  gap: var(--modern-space-3);
  padding-block: var(--modern-space-5) var(--modern-space-3);
}
.modern-home-shortcuts {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-4);
}
.modern-home-body {
  display: grid;
  align-content: start;
  gap: var(--modern-page-gap);
  min-height: 0;
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  padding-block: var(--modern-space-3) var(--modern-space-5);
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
.modern-home-overview-heading p {
  margin-top: var(--modern-space-2);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-home-overview-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3);
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
.modern-home-onboarding {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-4);
  margin-top: var(--modern-space-5);
  padding-top: var(--modern-space-4);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-home-onboarding p {
  flex: 1;
  min-width: 220px;
  font-size: var(--modern-font-size-secondary);
  color: var(--modern-muted);
}
.modern-home-updating {
  opacity: var(--modern-opacity-quiet);
}
.modern-home-charts {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(320px, 1fr);
  gap: var(--modern-space-4);
}
.modern-home-chart-context {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  margin-bottom: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-chart-context > :last-child {
  margin-inline-start: auto;
}
.modern-home-ranking {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  padding: var(--modern-space-4);
}
.modern-home-issues {
  display: grid;
  gap: var(--modern-space-3);
}
.modern-home-issue {
  display: grid;
  grid-template-columns: auto minmax(140px, 1fr) minmax(160px, 2fr) auto;
  width: 100%;
  gap: var(--modern-space-3);
  padding-block: var(--modern-space-2);
}
.modern-home-issue > span:nth-last-child(2) {
  color: var(--modern-muted);
}
@media (max-width: 1150px) {
  .modern-home-charts {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (max-width: 760px) {
  .modern-home-shortcuts {
    gap: var(--modern-space-3);
  }
  .modern-home-issue {
    grid-template-columns: auto minmax(0, 1fr) auto;
  }
  .modern-home-issue > span:nth-last-child(2) {
    grid-column: 2;
  }
  .modern-home-issue > :last-child {
    grid-column: 3;
    grid-row: 1 / span 2;
  }
}
</style>
