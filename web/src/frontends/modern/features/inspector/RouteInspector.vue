<script setup lang="ts">
import { ArrowRight, ChevronDown, ChevronRight, Eye, Route, Search } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRouter } from 'vue-router'
import { useApiClient } from '@shared/http/client-context'
import type { GroupWorkspace } from '@modern/api/groups'
import type { LogAccessKeyOption } from '@modern/api/logs'
import { inspectRoute, type InspectionGroup, type InspectionRequest } from '@modern/api/inspector'
import { useURLState, positivePage } from '@modern/app/url-state'
import { protocolOrder } from '@modern/i18n/protocols'
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
  AppProgressBar,
  AppProtocolTag,
  AppSearchSelect,
  AppSegmentedControl,
  AppSelect,
  AppTextField,
  AppTooltip,
} from '@modern/components/ui'
import { credentialTime } from '@modern/features/groups/credential-presentation'
import { percentage } from '@modern/features/usage/usage-display'
import { activeGroups, groupWeight, reasonLabel } from './inspection-display'
import InspectionCredentials from './InspectionCredentials.vue'

const props = defineProps<{
  groups?: GroupWorkspace
  groupsLoading: boolean
  groupsFailed: boolean
  accessKeys?: LogAccessKeyOption[]
  keysLoading: boolean
  keysFailed: boolean
}>()
const emit = defineEmits<{ retryOptions: [] }>()
const client = useApiClient()
const router = useRouter()
const { t, te, n, locale } = useI18n()
const state = useURLState(
  [
    'inspect_protocol',
    'inspect_external_model',
    'inspect_access_key_id',
    'inspect_run',
    'inspect_view',
    'inspect_q',
    'inspect_page',
    'inspect_page_size',
  ],
  (query) => ({
    protocol: protocolOrder.find((value) => value === query.inspect_protocol) ?? protocolOrder[0],
    model: typeof query.inspect_external_model === 'string' ? query.inspect_external_model : '',
    key: positivePage(query.inspect_access_key_id, 0),
    run: query.inspect_run === '1',
    view: ['candidates', 'excluded'].includes(String(query.inspect_view))
      ? String(query.inspect_view)
      : 'all',
    q: typeof query.inspect_q === 'string' ? query.inspect_q : '',
    page: positivePage(query.inspect_page),
    pageSize: [20, 50, 100].includes(positivePage(query.inspect_page_size))
      ? positivePage(query.inspect_page_size)
      : 20,
  }),
  (value) => ({
    inspect_protocol: value.protocol,
    inspect_external_model: value.model || undefined,
    inspect_access_key_id: value.key ? String(value.key) : undefined,
    inspect_run: value.run ? '1' : undefined,
    inspect_view: value.view === 'all' ? undefined : value.view,
    inspect_q: value.q || undefined,
    inspect_page: value.page > 1 ? String(value.page) : undefined,
    inspect_page_size: value.pageSize === 20 ? undefined : String(value.pageSize),
  }),
)
const draft = ref({
  protocol: state.value.protocol,
  model: state.value.model,
  key: state.value.key ? String(state.value.key) : '',
})
const touched = ref(false)
watch([() => state.value.protocol, () => state.value.model, () => state.value.key], () => {
  draft.value = {
    protocol: state.value.protocol,
    model: state.value.model,
    key: state.value.key ? String(state.value.key) : '',
  }
  touched.value = false
})
const keyOptions = computed(() => [
  ...(draft.value.key && !props.accessKeys?.some((key) => String(key.id) === draft.value.key)
    ? [
        {
          value: draft.value.key,
          label: props.accessKeys ? t('logs.deleted') : t('ui.loading'),
          disabled: true,
        },
      ]
    : []),
  ...(props.accessKeys ?? []).map((key) => ({
    value: String(key.id),
    label: key.name,
    description: key.suffix,
  })),
])
const protocolOptions = computed(() =>
  protocolOrder.map((protocol) => ({ value: protocol, label: t('protocols.' + protocol) })),
)
const modelOptions = computed(() =>
  [...new Set(props.groups?.items.flatMap((group) => group.modelNames))]
    .sort()
    .map((model) => ({ value: model, label: model })),
)
const validModel = (model: string) =>
  model.trim().length > 0 &&
  new TextEncoder().encode(model.trim()).length <= 255 &&
  !/[\u0000-\u001f\u007f-\u009f]/u.test(model)
const keyError = computed(() =>
  (touched.value || state.value.run) &&
  props.accessKeys !== undefined &&
  !props.accessKeys.some((key) => String(key.id) === draft.value.key)
    ? t('inspector.requiredKey')
    : undefined,
)
const modelError = computed(() =>
  (touched.value || state.value.run) && !validModel(draft.value.model)
    ? t(draft.value.model.trim() ? 'inspector.invalidModel' : 'inspector.requiredModel')
    : undefined,
)
const request = computed<InspectionRequest>(() => ({
  protocol: state.value.protocol,
  external_model: state.value.model,
  access_key_id: state.value.key,
}))
const canQuery = computed(
  () =>
    state.value.run &&
    validModel(state.value.model) &&
    Boolean(props.accessKeys?.some((key) => key.id === state.value.key)),
)
const query = useQuery(
  computed(() => {
    const body = { ...request.value }
    return {
      queryKey: ['modern', 'inspection', body],
      queryFn: ({ signal }: { signal: AbortSignal }) => inspectRoute(client, body, signal),
      enabled: canQuery.value,
    }
  }),
)
const result = computed(() => (canQuery.value ? query.data.value : undefined))
const changed = computed(
  () =>
    result.value &&
    (draft.value.protocol !== state.value.protocol ||
      draft.value.model.trim() !== state.value.model ||
      Number(draft.value.key) !== state.value.key),
)
const candidates = computed(() => (result.value ? activeGroups(result.value) : []))
const totalWeight = computed(() =>
  candidates.value.reduce((sum, group) => sum + groupWeight(group), 0),
)
const active = (group: InspectionGroup) => candidates.value.includes(group)
const expanded = ref(new Set<string>())
const rowKey = (group: InspectionGroup) => `${group.id}:${group.mode}`
watch(result, (value) => {
  const available = new Set(value?.groups.map(rowKey))
  expanded.value = new Set([...expanded.value].filter((key) => available.has(key)))
})
const groupMap = computed(() => new Map(props.groups?.items.map((group) => [group.id, group])))
const filtered = computed(() =>
  (result.value?.groups ?? [])
    .filter((group) => {
      if (state.value.view === 'candidates' && !group.routable) return false
      if (state.value.view === 'excluded' && group.included) return false
      const target = [
        group.name,
        groupMap.value.get(group.id)?.channelName,
        group.channelID,
        group.model,
        reasonLabel(group.reason, t),
      ]
        .join(' ')
        .toLocaleLowerCase()
      return target.includes(state.value.q.trim().toLocaleLowerCase())
    })
    .sort(
      (a, b) =>
        Number(active(b)) - Number(active(a)) ||
        Number(b.routable) - Number(a.routable) ||
        Number(b.included) - Number(a.included) ||
        groupWeight(b) - groupWeight(a) ||
        a.name.localeCompare(b.name),
    ),
)
const page = computed(() =>
  Math.min(state.value.page, Math.max(1, Math.ceil(filtered.value.length / state.value.pageSize))),
)
const visible = computed(() =>
  filtered.value.slice((page.value - 1) * state.value.pageSize, page.value * state.value.pageSize),
)
const views = computed(() => [
  { value: 'all', label: t('inspector.all'), count: result.value?.groups.length ?? 0 },
  {
    value: 'candidates',
    label: t('inspector.candidates'),
    count: result.value?.groups.filter((group) => group.routable).length ?? 0,
  },
  {
    value: 'excluded',
    label: t('inspector.excluded'),
    count: result.value?.groups.filter((group) => !group.included).length ?? 0,
  },
])
const filters = computed(() => [
  ...(state.value.view === 'all'
    ? []
    : [
        {
          key: 'view',
          label: t('inspector.status'),
          value: views.value.find((view) => view.value === state.value.view)!.label,
        },
      ]),
  ...(state.value.q ? [{ key: 'q', label: t('ui.select.search'), value: state.value.q }] : []),
])
function reset(key?: string): void {
  state.value = {
    ...state.value,
    view: !key || key === 'view' ? 'all' : state.value.view,
    q: !key || key === 'q' ? '' : state.value.q,
    page: 1,
  }
}
function share(group: InspectionGroup): number {
  return active(group) && totalWeight.value ? (groupWeight(group) / totalWeight.value) * 100 : 0
}
function status(group: InspectionGroup): string {
  return !group.included
    ? 'excluded'
    : !group.routable
      ? 'blocked'
      : active(group)
        ? 'active'
        : 'fallback'
}
function toggle(group: InspectionGroup): void {
  const key = rowKey(group)
  const next = new Set(expanded.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expanded.value = next
}
async function run(): Promise<void> {
  touched.value = true
  if (
    !validModel(draft.value.model) ||
    !props.accessKeys?.some((key) => String(key.id) === draft.value.key) ||
    query.isFetching.value
  )
    return
  const same =
    state.value.run &&
    state.value.protocol === draft.value.protocol &&
    state.value.model === draft.value.model.trim() &&
    state.value.key === Number(draft.value.key)
  state.value = {
    ...state.value,
    protocol: draft.value.protocol,
    model: draft.value.model.trim(),
    key: Number(draft.value.key),
    run: true,
    page: 1,
  }
  if (same) await query.refetch()
}
async function refresh(): Promise<void> {
  if (canQuery.value) await query.refetch()
}
function retryOptions(): void {
  emit('retryOptions')
}
const pending = computed(() => query.isFetching.value)
const updatedAt = computed(() => result.value?.observedAt)
defineExpose({ refresh, pending, updatedAt })
</script>

<template>
  <div class="modern-inspector-workspace">
    <form class="modern-inspector-form" @submit.prevent="run">
      <AppSearchSelect
        v-model="draft.key"
        :label="t('inspector.accessKey')"
        :placeholder="t('inspector.chooseKey')"
        :options="keyOptions"
        :error="keyError"
        :loading="keysLoading"
        size="xs"
        label-hidden
        required
      />
      <AppSelect
        v-model="draft.protocol"
        :label="t('inspector.protocol')"
        :options="protocolOptions"
        size="xs"
        label-hidden
      />
      <AppSearchSelect
        v-model="draft.model"
        :label="t('inspector.model')"
        :placeholder="t('inspector.chooseModel')"
        :options="modelOptions"
        :error="modelError"
        :loading="groupsLoading"
        size="xs"
        allow-custom
        label-hidden
        required
      />
      <AppButton
        type="submit"
        variant="primary"
        size="xs"
        :icon="Route"
        :loading="query.isFetching.value"
        :disabled="!accessKeys || keysFailed || !accessKeys.length"
        >{{ t('inspector.run') }}</AppButton
      >
    </form>
    <div v-if="groupsFailed || keysFailed" class="modern-inspector-feedback" role="alert">
      <span>{{ t('inspector.optionsFailed') }}</span
      ><AppButton size="xs" @click="retryOptions">{{ t('ui.retry') }}</AppButton>
    </div>
    <AppCollectionState
      v-if="!result && (query.isFetching.value || query.isError.value)"
      :loading="query.isFetching.value"
      :error="query.isError.value"
      :title="t(query.isError.value ? 'inspector.failed' : 'ui.loading')"
    >
      <AppButton v-if="query.isError.value" @click="run">{{ t('ui.retry') }}</AppButton>
    </AppCollectionState>
    <template v-if="result">
      <section class="modern-inspector-summary" :aria-label="t('inspector.result')">
        <div class="modern-inspector-verdict">
          <AppBadge :tone="result.routable ? 'success' : 'danger'" size="xs" dot>{{
            t(result.routable ? 'inspector.routable' : 'inspector.blocked')
          }}</AppBadge>
          <strong>{{
            result.routable
              ? t('inspector.availableCount', { count: n(candidates.length) })
              : reasonLabel(result.reason, t)
          }}</strong>
          <span class="modern-inspector-observed">{{
            t('inspector.observed', { time: credentialTime(result.observedAt, locale) })
          }}</span>
        </div>
        <div class="modern-inspector-request">
          <span>{{ result.accessKey.name }}</span
          ><AppIcon :icon="ArrowRight" size="sm" />
          <AppProtocolTag :protocol="result.protocol" /><AppOverflowText
            :text="result.model ?? '—'"
          />
          <AppBadge size="xs">{{ t('inspector.' + result.strategy) }}</AppBadge>
          <AppBadge size="xs">{{
            t(result.requirement === 'native' ? 'inspector.nativeOnly' : 'inspector.any')
          }}</AppBadge>
          <span>{{
            te('logs.values.' + result.operation)
              ? t('logs.values.' + result.operation)
              : result.operation
          }}</span>
        </div>
        <p v-if="changed || query.isError.value" class="modern-inspector-stale" role="status">
          {{ t(query.isError.value ? 'inspector.failed' : 'inspector.changed') }}
        </p>
      </section>
      <div class="modern-inspector-result-tools">
        <AppTextField
          v-model="state.q"
          class="modern-inspector-result-search"
          :label="t('inspector.search')"
          :placeholder="t('inspector.search')"
          :icon="Search"
          type="search"
          size="xs"
          label-hidden
          @update:model-value="state.page = 1"
        />
        <div class="modern-inspector-filterbar">
          <AppSegmentedControl
            :model-value="state.view"
            :label="t('inspector.result')"
            :options="views"
            @update:model-value="state = { ...state, view: $event, page: 1 }"
          />
          <AppFilterSummary :items="filters" @remove="reset" @reset="reset()" />
        </div>
      </div>
      <AppListFrame :label="t('inspector.result')" :loading="query.isFetching.value" flow>
        <div
          class="modern-inspector-table"
          role="table"
          :aria-label="t('inspector.result')"
          tabindex="0"
        >
          <div class="modern-inspector-columns modern-inspector-head" role="row">
            <span role="columnheader">{{ t('inspector.group') }}</span
            ><span role="columnheader">{{ t('inspector.upstream') }}</span>
            <span role="columnheader">{{ t('inspector.mode') }}</span
            ><span role="columnheader">{{ t('inspector.status') }}</span>
            <span role="columnheader">{{ t('inspector.credentials') }}</span>
            <AppTooltip :label="t('inspector.shareHint')"
              ><span role="columnheader" tabindex="0">{{ t('inspector.share') }}</span></AppTooltip
            >
            <span class="modern-sr-only" role="columnheader">{{ t('inspector.openGroup') }}</span>
          </div>
          <AppCollectionState
            v-if="!visible.length"
            :title="t(result.groups.length ? 'inspector.emptyFiltered' : 'inspector.noGroups')"
          />
          <div v-for="group in visible" :key="rowKey(group)" class="modern-inspector-row">
            <div class="modern-inspector-columns" role="row">
              <div class="modern-inspector-identity" role="cell">
                <AppIconButton
                  :icon="expanded.has(rowKey(group)) ? ChevronDown : ChevronRight"
                  :label="
                    t(expanded.has(rowKey(group)) ? 'inspector.collapse' : 'inspector.details')
                  "
                  :aria-expanded="expanded.has(rowKey(group))"
                  size="xs"
                  variant="ghost"
                  @click="toggle(group)"
                />
                <AppChannelIcon
                  :icon="groupMap.get(group.id)?.channelIcon"
                  :name="groupMap.get(group.id)?.channelName || group.channelID"
                  :tooltip="false"
                  size="sm"
                />
                <div class="modern-inspector-name">
                  <AppButton as-child variant="text" class="modern-inspector-name-link"
                    ><RouterLink :to="{ name: 'modern-group-detail', params: { id: group.id } }"
                      ><AppOverflowText :text="group.name" /></RouterLink></AppButton
                  ><AppOverflowText
                    :text="groupMap.get(group.id)?.channelName || group.channelID"
                  />
                </div>
              </div>
              <div role="cell"><AppOverflowText :text="group.model ?? '—'" /></div>
              <div role="cell">
                <AppBadge size="xs" :tone="group.mode === 'native' ? 'info' : 'neutral'">{{
                  t('inspector.' + group.mode)
                }}</AppBadge>
              </div>
              <div class="modern-inspector-status" role="cell">
                <AppBadge
                  size="xs"
                  variant="plain"
                  :tone="
                    active(group)
                      ? 'success'
                      : group.included && !group.routable
                        ? 'warning'
                        : 'neutral'
                  "
                  dot
                  >{{ t('inspector.' + status(group)) }}</AppBadge
                ><AppOverflowText v-if="group.reason" :text="reasonLabel(group.reason, t)" />
              </div>
              <span role="cell"
                >{{ n(group.credentials.filter((row) => row.available).length) }} /
                {{ n(group.credentials.length) }}</span
              >
              <div class="modern-inspector-share" role="cell">
                <span>{{ active(group) ? percentage(share(group), locale) : '—' }}</span
                ><AppProgressBar
                  :label="t('inspector.share')"
                  :value="share(group)"
                  :tone="active(group) ? 'info' : 'neutral'"
                  size="sm"
                />
              </div>
              <div role="cell">
                <AppIconButton
                  :icon="Eye"
                  :label="t('inspector.openGroup')"
                  size="xs"
                  variant="ghost"
                  @click="router.push({ name: 'modern-group-detail', params: { id: group.id } })"
                />
              </div>
            </div>
            <InspectionCredentials
              v-if="expanded.has(rowKey(group))"
              :group="group"
              :observed-at="result.observedAt"
            />
          </div>
        </div>
        <template #footer
          ><AppPagination
            :page="page"
            :page-size="state.pageSize"
            :total="filtered.length"
            mode="total"
            :pending="query.isFetching.value"
            @update:page="state.page = $event"
            @update:page-size="state = { ...state, pageSize: $event, page: 1 }"
        /></template>
      </AppListFrame>
    </template>
  </div>
</template>

<style scoped>
.modern-inspector-workspace {
  container: modern-inspector / inline-size;
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}
.modern-inspector-form {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr)) auto;
  align-items: start;
  gap: var(--modern-space-3);
}
.modern-inspector-form > :not(:last-child) {
  min-width: 0;
}
.modern-inspector-feedback {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-danger);
  margin-top: var(--modern-space-2);
}
.modern-inspector-summary {
  display: grid;
  gap: var(--modern-space-2);
  background: var(--modern-subtle);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-3);
  margin-top: var(--modern-space-3);
}
.modern-inspector-table {
  min-width: 0;
  overflow-x: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  overscroll-behavior-x: contain;
}
.modern-inspector-verdict,
.modern-inspector-request {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2) var(--modern-space-3);
  min-width: 0;
}
.modern-inspector-verdict strong {
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-inspector-observed {
  margin-inline-start: auto;
}
.modern-inspector-observed,
.modern-inspector-request {
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
.modern-inspector-request > * {
  max-width: 100%;
}
.modern-inspector-stale {
  font-size: var(--modern-font-size-small);
  color: var(--modern-warning);
}
.modern-inspector-result-tools {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2) var(--modern-space-3);
  padding-block: var(--modern-space-3);
}
.modern-inspector-result-search {
  flex: 1 1 240px;
  min-width: 0;
}
.modern-inspector-filterbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
}
.modern-inspector-filterbar > :last-child:not(:first-child) {
  margin-inline-start: auto;
}
.modern-inspector-columns {
  display: grid;
  grid-template-columns:
    minmax(170px, 1.2fr) minmax(140px, 1fr) 64px minmax(120px, 1fr)
    76px 80px 28px;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 760px;
  padding: var(--modern-space-2) var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
  font-variant-numeric: tabular-nums;
}
.modern-inspector-columns > * {
  min-width: 0;
}
.modern-inspector-head {
  color: var(--modern-muted);
  background: var(--modern-surface);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-inspector-row {
  min-width: 760px;
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-inspector-row:last-child {
  border-bottom: 0;
}
.modern-inspector-name-link {
  min-width: 0;
  max-width: 100%;
}
.modern-inspector-identity {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-inspector-name,
.modern-inspector-status,
.modern-inspector-share {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-1);
}
.modern-inspector-name > span,
.modern-inspector-status > :last-child:not(:first-child) {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
@container modern-inspector (max-width: 620px) {
  .modern-inspector-form {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .modern-inspector-form > :last-child {
    justify-self: end;
  }
}
</style>
