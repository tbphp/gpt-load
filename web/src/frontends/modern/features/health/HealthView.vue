<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import {
  ArrowUpRight,
  Eye,
  KeyRound,
  Layers2,
  ScrollText,
  Search,
  ShieldCheck,
  UserRound,
} from '@lucide/vue'
import { getHealth } from '@modern/api/health'
import { getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import { useApiClient } from '@shared/http/client-context'
import { useURLState } from '@modern/app/url-state'
import { usePageRefresh } from '@modern/app/page-refresh'
import { useMessageSource } from '@modern/app/messages'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppCollectionState,
  AppFilterSummary,
  AppIcon,
  AppIconButton,
  AppListFrame,
  AppOverflowText,
  AppPagination,
  AppSearchSelect,
  AppSegmentedControl,
  AppSelect,
  AppSortMenu,
  AppTextField,
} from '@modern/components/ui'
import {
  compareHealthIssues,
  healthIssues,
  healthLogsLocation,
  healthManageLocation,
  healthTime,
  type HealthIssue,
} from './health-display'
import {
  healthKinds,
  healthStateKeys,
  parseHealthState,
  serializeHealthState,
  type HealthState,
} from './health-state'
import HealthOverview from './HealthOverview.vue'
import HealthDetailPanel from './HealthDetailPanel.vue'

const { t, locale } = useI18n()
const router = useRouter()
const client = useApiClient()
const state = useURLState(healthStateKeys, parseHealthState, serializeHealthState)
const query = useQuery({
  queryKey: ['modern', 'health'],
  queryFn: ({ signal }) => getHealth(client, signal),
})
const groups = useQuery({
  queryKey: groupQueryKey,
  queryFn: ({ signal }) => getGroupWorkspace(client, signal),
})
const report = computed(() => (groups.data.value ? query.data.value : undefined))
const pending = computed(() => query.isFetching.value || groups.isFetching.value)
const failed = computed(() => query.isError.value || groups.isError.value)
const groupMap = computed(() => new Map(groups.data.value?.items.map((row) => [row.id, row])))
const issues = computed(() =>
  report.value
    ? healthIssues(report.value, groups.data.value!.items, (key, values) => t(key, values ?? {}))
    : [],
)
const unavailable = computed(() => issues.value.filter((row) => row.kind === 'group').length)
const groupOptions = computed(() => {
  const options = (report.value?.groups ?? []).map((row) => ({
    value: String(row.id),
    label: row.name,
  }))
  if (state.value.group && !options.some((row) => row.value === state.value.group))
    options.push({
      value: state.value.group,
      label:
        groupMap.value.get(Number(state.value.group))?.name ??
        (report.value ? t('logs.deleted') : '—'),
    })
  return [{ value: '', label: t('health.allGroups') }, ...options]
})
const kindOptions = computed(() => [
  { value: '', label: t('health.allKinds') },
  ...healthKinds.map((kind) => ({ value: kind, label: t('health.kinds.' + kind) })),
])
const sortOptions = computed(() => [
  { value: 'priority', label: t('health.priority') },
  { value: 'name', label: t('health.nameSort') },
])
const matching = computed(() => {
  const q = state.value.q.trim().toLocaleLowerCase(locale.value)
  return issues.value.filter(
    (row) =>
      (!state.value.group || String(row.groupID) === state.value.group) &&
      (!state.value.kind || row.kind === state.value.kind) &&
      (!q ||
        [
          row.name,
          row.groupName,
          row.reason,
          row.impact,
          t('health.kinds.' + row.kind),
          groupMap.value.get(row.groupID ?? 0)?.channelName,
        ]
          .filter(Boolean)
          .join(' ')
          .toLocaleLowerCase(locale.value)
          .includes(q)),
  )
})
const severityOptions = computed(() => [
  { value: '', label: t('health.all'), count: matching.value.length },
  {
    value: 'danger',
    label: t('health.urgent'),
    count: matching.value.filter((row) => row.severity === 'danger').length,
  },
  {
    value: 'warning',
    label: t('health.attention'),
    count: matching.value.filter((row) => row.severity === 'warning').length,
  },
])
const filtered = computed(() =>
  matching.value
    .filter((row) => !state.value.severity || row.severity === state.value.severity)
    .sort((a, b) => compareHealthIssues(a, b, locale.value, state.value.sort === 'priority')),
)
const page = computed(() =>
  Math.min(state.value.page, Math.max(1, Math.ceil(filtered.value.length / state.value.pageSize))),
)
const rows = computed(() =>
  filtered.value.slice((page.value - 1) * state.value.pageSize, page.value * state.value.pageSize),
)
const selected = computed(() => issues.value.find((row) => row.key === state.value.detail))
const frame = ref<InstanceType<typeof AppListFrame>>()
watch(page, (value) => {
  if (report.value && value !== state.value.page) state.value = { ...state.value, page: value }
})
function change(value: Partial<HealthState>): void {
  state.value = { ...state.value, ...value, page: value.page ?? 1 }
  frame.value?.scrollToTop()
}
const search = computed({ get: () => state.value.q, set: (q) => change({ q }) })
function reset(key?: string): void {
  if (!key) {
    change({ q: '', group: '', kind: '', severity: '', sort: 'priority' })
    return
  }
  change({ [key]: key === 'sort' ? 'priority' : '' })
}
const summary = computed(() => [
  ...(state.value.q ? [{ key: 'q', label: t('health.searchLabel'), value: state.value.q }] : []),
  ...(state.value.group
    ? [
        {
          key: 'group',
          label: t('health.group'),
          value:
            groupOptions.value.find((row) => row.value === state.value.group)?.label ??
            t('logs.deleted'),
        },
      ]
    : []),
  ...(state.value.kind
    ? [{ key: 'kind', label: t('health.kind'), value: t('health.kinds.' + state.value.kind) }]
    : []),
  ...(state.value.severity
    ? [
        {
          key: 'severity',
          label: t('health.severity'),
          value: t(state.value.severity === 'danger' ? 'health.urgent' : 'health.attention'),
        },
      ]
    : []),
  ...(state.value.sort !== 'priority'
    ? [{ key: 'sort', label: t('health.sort'), value: t('health.nameSort') }]
    : []),
])
function open(detail: string): void {
  state.value = { ...state.value, detail }
}
function logs(issue: HealthIssue): void {
  void router.push(healthLogsLocation(issue))
}
function manage(issue: HealthIssue): void {
  void router.push(healthManageLocation(issue))
}
async function refresh(): Promise<void> {
  await Promise.all([query.refetch(), groups.refetch()])
}
usePageRefresh({ refresh, pending, updatedAt: () => report.value?.observedAt })
useMessageSource(() =>
  failed.value && report.value
    ? { tone: 'warning', text: t('health.stale'), action: { label: t('ui.retry'), run: refresh } }
    : undefined,
)
</script>

<template>
  <div class="modern-health-workspace">
    <HealthOverview
      v-if="report"
      :report="report"
      :unavailable="unavailable"
      @select="change({ kind: $event, group: '', q: '', severity: '' })"
    />
    <form
      class="modern-health-filters"
      role="search"
      :aria-label="t('health.searchLabel')"
      @submit.prevent
    >
      <AppTextField
        v-model="search"
        type="search"
        :icon="Search"
        :label="t('health.searchLabel')"
        :placeholder="t('health.search')"
        label-hidden
        class="modern-health-search"
      />
      <AppSearchSelect
        :model-value="state.group"
        :options="groupOptions"
        :label="t('health.group')"
        label-hidden
        class="modern-health-choice"
        @update:model-value="change({ group: $event })"
      />
      <AppSelect
        :model-value="state.kind"
        :options="kindOptions"
        :label="t('health.kind')"
        label-hidden
        class="modern-health-choice"
        @update:model-value="change({ kind: healthKinds.find((kind) => kind === $event) ?? '' })"
      />
      <AppSortMenu
        :model-value="state.sort"
        :options="sortOptions"
        :label="t('health.sort')"
        @update:model-value="change({ sort: $event === 'name' ? 'name' : 'priority' })"
      />
    </form>
    <div class="modern-health-statusbar">
      <AppSegmentedControl
        :model-value="state.severity"
        :options="severityOptions"
        :label="t('health.severity')"
        @update:model-value="
          change({ severity: $event === 'danger' || $event === 'warning' ? $event : '' })
        "
      />
      <div class="modern-health-summary">
        <AppFilterSummary :items="summary" @remove="reset" @reset="reset()" />
      </div>
    </div>
    <AppListFrame
      ref="frame"
      class="modern-health-list"
      :label="t('health.issues')"
      :loading="pending && Boolean(report)"
      :scroll-key="
        JSON.stringify([
          state.q,
          state.group,
          state.kind,
          state.severity,
          state.sort,
          page,
          state.pageSize,
        ])
      "
    >
      <template #header>
        <div class="modern-health-row modern-health-heading" aria-hidden="true">
          <span>{{ t('health.object') }}</span
          ><span>{{ t('health.group') }}</span
          ><span>{{ t('health.problem') }}</span
          ><span>{{ t('health.impact') }}</span
          ><span>{{ t('health.recoveryColumn') }}</span
          ><span>{{ t('health.actions') }}</span>
        </div>
      </template>
      <AppCollectionState v-if="!report && !failed" :title="t('health.loading')" loading />
      <AppCollectionState v-else-if="!report" :title="t('health.failed')" error
        ><AppButton @click="refresh">{{ t('ui.retry') }}</AppButton></AppCollectionState
      >
      <AppCollectionState
        v-else-if="!rows.length"
        :icon="summary.length ? Search : ShieldCheck"
        :title="t(summary.length ? 'health.noMatches' : 'health.healthy')"
        :description="t(summary.length ? 'health.noMatchesHelp' : 'health.healthyHelp')"
      >
        <AppButton v-if="summary.length" @click="reset()">{{ t('collection.reset') }}</AppButton>
      </AppCollectionState>
      <template v-else>
        <article
          v-for="row in rows"
          :key="row.key"
          class="modern-health-row modern-health-record"
          :class="{ 'is-selected': state.detail === row.key }"
          :aria-label="row.name + ' · ' + t('health.kinds.' + row.kind)"
        >
          <div class="modern-health-object">
            <span class="modern-health-object-mark"
              ><AppIcon
                :icon="
                  row.identityType === 'group'
                    ? Layers2
                    : row.identityType === 'access_key'
                      ? KeyRound
                      : UserRound
                "
                size="sm"
            /></span>
            <div class="modern-health-object-text">
              <AppButton variant="text" class="modern-health-name" @click="open(row.key)"
                ><AppOverflowText :text="row.name" /></AppButton
              ><span>{{
                t(
                  row.identityType === 'group'
                    ? 'health.group'
                    : row.identityType === 'access_key'
                      ? 'health.accessKey'
                      : 'health.account',
                )
              }}</span>
            </div>
          </div>
          <div class="modern-health-group">
            <AppChannelIcon
              v-if="row.groupID && groupMap.get(row.groupID)"
              :icon="groupMap.get(row.groupID)!.channelIcon"
              :mark="groupMap.get(row.groupID)!.channelMark"
              :name="groupMap.get(row.groupID)!.channelName"
              :tooltip="false"
              size="sm"
            /><AppButton
              v-if="row.groupID"
              variant="text"
              @click="change({ group: String(row.groupID) })"
              ><AppOverflowText :text="row.groupName ?? t('logs.deleted')" /></AppButton
            ><span v-else class="modern-health-muted">—</span>
          </div>
          <div class="modern-health-stack">
            <AppBadge :tone="row.severity" size="xs" dot>{{
              t('health.kinds.' + row.kind)
            }}</AppBadge
            ><AppOverflowText :text="row.reason" class="modern-health-secondary" />
          </div>
          <AppOverflowText :text="row.impact" class="modern-health-impact" />
          <div class="modern-health-stack">
            <AppOverflowText :text="row.recovery" /><AppOverflowText
              v-if="row.recoveryAt"
              :text="healthTime(row.recoveryAt, locale)"
              :full-text="healthTime(row.recoveryAt, locale, true)"
              class="modern-health-secondary modern-health-time"
            />
          </div>
          <div class="modern-health-actions">
            <AppIconButton
              :icon="Eye"
              :label="t('health.details')"
              :tooltip="true"
              size="xs"
              variant="text"
              @click="open(row.key)"
            /><AppIconButton
              :icon="ScrollText"
              :label="t('health.logs')"
              :tooltip="true"
              size="xs"
              variant="text"
              @click="logs(row)"
            /><AppIconButton
              :icon="ArrowUpRight"
              :label="t('health.manage')"
              :tooltip="true"
              size="xs"
              variant="text"
              @click="manage(row)"
            />
          </div>
        </article>
      </template>
      <template #footer
        ><AppPagination
          mode="total"
          :page="page"
          :page-size="state.pageSize"
          :total="report ? filtered.length : undefined"
          :pending="pending"
          @update:page="change({ page: $event })"
          @update:page-size="change({ pageSize: $event })"
      /></template>
    </AppListFrame>
    <HealthDetailPanel
      v-if="state.detail && report"
      :key="state.detail"
      :issue="selected"
      :report="report"
      :groups="groupMap"
      @close="open('')"
    />
  </div>
</template>

<style scoped>
.modern-health-workspace {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
}
.modern-health-filters {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3);
  padding-block: var(--modern-space-5) var(--modern-space-3);
}
.modern-health-search {
  flex: 2 1 320px;
  min-width: 0;
}
.modern-health-choice {
  flex: 1 1 180px;
  min-width: 0;
}
.modern-health-statusbar {
  display: flex;
  flex: none;
  align-items: center;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--modern-space-3);
  padding-bottom: var(--modern-space-3);
}
.modern-health-summary {
  display: flex;
  min-width: 0;
  margin-left: auto;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
}
.modern-health-list {
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-health-row {
  display: grid;
  grid-template-columns:
    minmax(210px, 1.5fr) minmax(130px, 0.85fr) minmax(190px, 1.2fr) minmax(180px, 1.1fr)
    minmax(170px, 1fr) 100px;
  align-items: center;
  gap: var(--modern-space-4);
  min-width: 1070px;
  padding: var(--modern-space-3) var(--modern-space-3);
}
.modern-health-heading {
  background: var(--modern-canvas);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-health-record {
  min-height: 70px;
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  font-size: var(--modern-font-size-secondary);
}
.modern-health-record:hover {
  background: var(--modern-control-hover);
}
.modern-health-record.is-selected {
  background: var(--modern-accent-soft);
}
.modern-health-record > * {
  min-width: 0;
}
.modern-health-object {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-3);
}
.modern-health-object-mark {
  display: grid;
  place-items: center;
  flex: none;
  width: var(--modern-control-sm);
  height: var(--modern-control-sm);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  color: var(--modern-muted);
  background: var(--modern-surface);
}
.modern-health-object-text {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-0-5);
}
.modern-health-name {
  min-width: 0;
  max-width: 100%;
  font-weight: var(--modern-weight-medium);
}
.modern-health-object-text > span,
.modern-health-secondary {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-health-group {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1-5);
}
.modern-health-group > :last-child {
  min-width: 0;
}
.modern-health-stack {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-1);
  justify-items: start;
}
.modern-health-impact,
.modern-health-muted {
  color: var(--modern-muted);
}
.modern-health-time {
  font-variant-numeric: tabular-nums;
}
.modern-health-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
}
@media (max-width: 760px) {
  .modern-health-filters {
    padding-top: var(--modern-space-3);
    gap: var(--modern-space-2);
  }
  .modern-health-search {
    flex-basis: 100%;
  }
  .modern-health-choice {
    flex-basis: 120px;
  }
  .modern-health-row {
    grid-template-columns:
      minmax(210px, 1.5fr) minmax(130px, 0.85fr) minmax(190px, 1.2fr) minmax(180px, 1.1fr)
      minmax(170px, 1fr) 148px;
    min-width: 1118px;
  }
}
</style>
