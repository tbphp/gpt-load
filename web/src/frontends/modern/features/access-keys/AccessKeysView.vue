<script setup lang="ts">
import { useMobileLayout } from '@modern/app/use-mobile-layout'
import { SlidersHorizontal, ChevronRight } from '@lucide/vue'
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import {
  ChartNoAxesCombined,
  CopyPlus,
  KeyRound,
  Pencil,
  Plus,
  RotateCcw,
  ScrollText,
  Search,
  Share2,
  Trash2,
} from '@lucide/vue'
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import {
  accessDetailKey,
  accessKeysKey,
  accessKeySorts,
  deleteAccessKey,
  getAccessKeys,
  revealAccessKey,
  updateAccessKey,
  type AccessFilters,
  type AccessKey,
  type AccessKeyRow,
} from '@modern/api/access-keys'
import { getGroupWorkspace, groupQueryKey } from '@modern/api/groups'
import { useMessages, useMessageSource } from '@modern/app/messages'
import { usePageRefresh } from '@modern/app/page-refresh'
import { positivePage, useURLState } from '@modern/app/url-state'
import {
  AppActionMenu,
  AppChannelIcon,
  AppSearchSelect,
  AppSelect,
  AppBadge,
  AppButton,
  AppFilterSheet,
  AppIcon,
  AppCollectionState,
  AppConfirmDialog,
  AppCopyValue,
  AppFilterSummary,
  AppIconButton,
  AppListFrame,
  AppOverflowText,
  AppPagination,
  AppSegmentedControl,
  AppSortMenu,
  AppSwitch,
  AppTextField,
  AppTooltip,
} from '@modern/components/ui'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import AccessKeyRestrictions from './AccessKeyRestrictions.vue'
import AccessKeyHandoff from './AccessKeyHandoff.vue'
import AccessKeyPanel from './AccessKeyPanel.vue'
import AccessKeyQuotaResetDialog from './AccessKeyQuotaResetDialog.vue'
import { accessState, accessTime } from './access-key-display'

const { t, n, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const isMobile = useMobileLayout()
const mobileFilters = ref(false)
const client = useApiClient()
const cache = useQueryClient()
const messages = useMessages()
const controller = new AbortController()
const filters = useURLState<AccessFilters>(
  ['q', 'status', 'sort', 'page', 'page_size', 'group_id', 'expiry'],
  (query) => ({
    pageSize: [20, 50, 100].includes(Number(query.page_size)) ? Number(query.page_size) : 20,
    group: positivePage(query.group_id, 0) ? String(query.group_id) : '',
    expiry:
      query.expiry === 'never' || query.expiry === 'active' || query.expiry === 'expired'
        ? query.expiry
        : '',
    q: typeof query.q === 'string' ? Array.from(query.q.trim()).slice(0, 200).join('') : '',
    status: query.status === 'active' || query.status === 'disabled' ? query.status : '',
    sort: accessKeySorts.find((value) => value === query.sort) ?? 'updated_desc',
    page: positivePage(query.page),
  }),
  (value) => ({
    ...(value.pageSize !== 20 ? { page_size: String(value.pageSize) } : {}),
    ...(value.group ? { group_id: value.group } : {}),
    ...(value.expiry ? { expiry: value.expiry } : {}),
    ...(value.q ? { q: value.q } : {}),
    ...(value.status ? { status: value.status } : {}),
    ...(value.sort !== 'updated_desc' ? { sort: value.sort } : {}),
    ...(value.page > 1 ? { page: String(value.page) } : {}),
  }),
)
const search = ref(filters.value.q)
const composing = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
const frame = ref<InstanceType<typeof AppListFrame>>()
const mutating = ref(new Set<number>())
const deleting = ref<AccessKeyRow>()
const resetting = ref<AccessKeyRow>()
const deleteError = ref('')
const query = useQuery(
  computed(() => ({
    queryKey: [...accessKeysKey, filters.value],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getAccessKeys(client, { ...filters.value }, signal),
    placeholderData: keepPreviousData,
  })),
)
const groups = useQuery({
  queryKey: groupQueryKey,
  queryFn: ({ signal }) => getGroupWorkspace(client, signal),
})
const groupMap = computed(
  () => new Map(groups.data.value?.items.map((row) => [String(row.id), row])),
)
const groupOptions = computed(() => [
  { value: '', label: t('accessKeys.allGroups') },
  ...(groups.data.value?.items ?? []).map((group) => ({
    value: String(group.id),
    label: group.name,
    keywords: [group.channelName, group.channelID],
  })),
])
const expiryOptions = computed(() =>
  ['', 'never', 'active', 'expired'].map((value) => ({
    value,
    label: t('accessKeys.expiryOptions.' + (value || 'all')),
  })),
)
const rows = computed(() => query.data.value?.items ?? [])
const busy = computed(() => mutating.value.size > 0)
const panel = computed(() => {
  const mode = route.query.panel
  return mode === 'create' || mode === 'detail' || mode === 'copy' || mode === 'share'
    ? mode
    : undefined
})
const selectedID = computed(() => positivePage(route.query.access_key, 0))
const sorts = computed(() =>
  accessKeySorts.map((value) => ({ value, label: t('accessKeys.sorts.' + value) })),
)
const states = computed(() => [
  { value: '', label: t('accessKeys.all'), count: query.data.value?.summary.total },
  { value: 'active', label: t('accessKeys.active'), count: query.data.value?.summary.active },
  { value: 'disabled', label: t('accessKeys.disabled'), count: query.data.value?.summary.disabled },
])
const summary = computed(() => [
  ...(filters.value.group
    ? [
        {
          key: 'group',
          label: t('accessKeys.groupFilter'),
          value: groupMap.value.get(filters.value.group)?.name ?? t('accessKeys.missingGroup'),
        },
      ]
    : []),
  ...(filters.value.expiry
    ? [
        {
          key: 'expiry',
          label: t('accessKeys.expires'),
          value: t('accessKeys.expiryOptions.' + filters.value.expiry),
        },
      ]
    : []),
  ...(filters.value.q ? [{ key: 'q', label: t('accessKeys.name'), value: filters.value.q }] : []),
  ...(filters.value.status
    ? [
        {
          key: 'status',
          label: t('accessKeys.state'),
          value: t('accessKeys.' + filters.value.status),
        },
      ]
    : []),
  ...(filters.value.sort !== 'updated_desc'
    ? [
        {
          key: 'sort',
          label: t('accessKeys.sort'),
          value: t('accessKeys.sorts.' + filters.value.sort),
        },
      ]
    : []),
])
function change(patch: Partial<AccessFilters>): void {
  if (busy.value) return
  filters.value = { ...filters.value, page: 1, ...patch }
  frame.value?.scrollToTop()
}
watch(
  () => filters.value.q,
  (value) => {
    clearTimeout(timer)
    search.value = value
  },
)
function scheduleSearch(): void {
  clearTimeout(timer)
  if (!composing.value) timer = setTimeout(() => change({ q: search.value.trim() }), 200)
}
function compositionEnd(): void {
  composing.value = false
  scheduleSearch()
}
function reset(key?: string): void {
  clearTimeout(timer)
  if (!key || key === 'q') search.value = ''
  change({
    ...(!key || key === 'group' ? { group: '' } : {}),
    ...(!key || key === 'expiry' ? { expiry: '' as const } : {}),
    ...(!key || key === 'q' ? { q: '' } : {}),
    ...(!key || key === 'status' ? { status: '' as const } : {}),
    ...(!key || key === 'sort' ? { sort: 'updated_desc' as const } : {}),
  })
}
async function setPanel(
  value?: 'create' | 'detail' | 'copy' | 'share',
  id?: number,
): Promise<void> {
  const query = { ...route.query }
  delete query.panel
  delete query.access_key
  if (value) query.panel = value
  if (value && id) query.access_key = String(id)
  await router.replace({ query })
}
function openCreate(): void {
  void setPanel('create')
}
function menus(row: AccessKeyRow) {
  return [
    { id: 'logs', label: t('accessKeys.viewLogs'), icon: ScrollText },
    { id: 'usage', label: t('accessKeys.viewUsage'), icon: ChartNoAxesCombined },
    { id: 'share', label: t('accessKeys.handoff'), icon: Share2 },
    { id: 'copy', label: t('accessKeys.copyConfig'), icon: CopyPlus },
    {
      id: 'reset',
      label: t('accessKeys.reset'),
      icon: RotateCcw,
      disabled: !row.cost_limit_rules.some((rule) => rule.id !== undefined),
    },
    { id: 'delete', label: t('accessKeys.delete'), icon: Trash2, danger: true },
  ]
}
function action(row: AccessKeyRow, value: string): void {
  if (busy.value) return
  if (value === 'delete') {
    deleteError.value = ''
    deleting.value = row
  } else if (value === 'reset' && row.cost_limit_rules.some((rule) => rule.id !== undefined))
    resetting.value = row
  else if (value === 'logs' || value === 'usage')
    void router.push({
      name: value === 'logs' ? 'modern-logs' : 'modern-usage',
      query: {
        access_key_id: String(row.id),
        preset: '7d',
      },
    })
  else if (value === 'copy' || value === 'share') void setPanel(value, row.id)
}
function resetPending(value: boolean): void {
  const id = resetting.value?.id
  if (id === undefined) return
  if (value) mutating.value.add(id)
  else mutating.value.delete(id)
}
async function refresh(): Promise<void> {
  await Promise.all([
    query.refetch(),
    cache.refetchQueries({ queryKey: ['modern', 'access-key-detail'], type: 'active' }),
    cache.refetchQueries({ queryKey: ['modern', 'rpm', 'access_key'], type: 'active' }),
    cache.refetchQueries({ queryKey: ['modern', 'groups', 'workspace'], type: 'active' }),
  ])
}
async function toggle(row: AccessKeyRow, enabled: boolean): Promise<void> {
  if (mutating.value.has(row.id)) return
  mutating.value.add(row.id)
  try {
    await updateAccessKey(
      client,
      row.id,
      { status: enabled ? 'active' : 'disabled' },
      controller.signal,
    )
    if (controller.signal.aborted) return
    await cache.invalidateQueries({ queryKey: accessKeysKey })
    await cache.invalidateQueries({ queryKey: accessDetailKey(row.id) })
    messages.show({ text: t('accessKeys.updated'), tone: 'success' })
  } catch {
    if (!controller.signal.aborted) messages.show({ text: t('accessKeys.failed'), tone: 'danger' })
  } finally {
    mutating.value.delete(row.id)
  }
}
async function remove(): Promise<void> {
  const row = deleting.value
  if (!row || busy.value) return
  mutating.value.add(row.id)
  deleteError.value = ''
  try {
    await deleteAccessKey(client, row.id, controller.signal)
    if (controller.signal.aborted) return
    deleting.value = undefined
    await deleted(row.id)
  } catch (cause) {
    if (!controller.signal.aborted && cause instanceof ApiError && cause.status === 404) {
      deleting.value = undefined
      await deleted(row.id)
    } else if (!controller.signal.aborted) deleteError.value = t('accessKeys.failed')
  } finally {
    mutating.value.delete(row.id)
  }
}
async function deleted(id = selectedID.value): Promise<void> {
  await setPanel(undefined)
  cache.removeQueries({ queryKey: accessDetailKey(id) })
  await cache.invalidateQueries({ queryKey: accessKeysKey })
  messages.show({ text: t('accessKeys.deleted'), tone: 'success' })
}
async function saved(row: AccessKey, created: boolean): Promise<void> {
  await setPanel(created ? 'share' : undefined, row.id)
  await cache.invalidateQueries({ queryKey: accessKeysKey })
  await cache.invalidateQueries({ queryKey: accessDetailKey(row.id) })
  messages.show({
    text: t(created ? 'accessKeys.created' : 'accessKeys.saved', { name: row.name }),
    tone: 'success',
  })
}
watch([query.data, busy], ([data]) => {
  if (!data || query.isPlaceholderData.value) return
  const last = Math.max(1, data.pagination.total_pages)
  if (filters.value.page > last) change({ page: last })
})
const resolvers = new Map<number, () => Promise<string>>()
function resolveKey(id: number): () => Promise<string> {
  let resolve = resolvers.get(id)
  if (!resolve) {
    resolve = () => revealAccessKey(client, id, controller.signal)
    resolvers.set(id, resolve)
  }
  return resolve
}
useMessageSource(() =>
  groups.isError.value
    ? {
        text: t('accessKeys.catalogFailed'),
        tone: 'warning',
        action: { label: t('ui.retry'), run: () => groups.refetch() },
      }
    : undefined,
)
useLoadingActivity(busy)
usePageRefresh({
  refresh,
  pending: () => query.isFetching.value || busy.value,
  updatedAt: () => query.dataUpdatedAt.value,
})
useMessageSource(() =>
  query.isError.value && query.data.value
    ? {
        text: t('accessKeys.stale'),
        tone: 'warning',
        action: { label: t('ui.retry'), run: refresh },
      }
    : undefined,
)
onScopeDispose(() => {
  controller.abort()
  clearTimeout(timer)
  resolvers.clear()
})
</script>
<template>
  <div class="modern-access-workspace">
    <form
      class="modern-access-toolbar"
      role="search"
      @submit.prevent="change({ q: search.trim() })"
    >
      <AppTextField
        v-model="search"
        class="modern-access-search"
        :label="t('accessKeys.search')"
        label-hidden
        :placeholder="t('accessKeys.search')"
        :icon="Search"
        type="search"
        :disabled="busy"
        @input="scheduleSearch"
        @compositionstart="composing = true"
        @compositionend="compositionEnd"
      />
      <AppFilterSheet
        v-model:open="mobileFilters"
        :mobile="isMobile"
        :title="t('ui.mobile.filters')"
      >
        <AppSearchSelect
          class="modern-access-filter"
          :model-value="filters.group"
          :label="t('accessKeys.groupFilter')"
          :label-hidden="!isMobile"
          :options="groupOptions"
          :disabled="busy || groups.isPending.value"
          @update:model-value="change({ group: $event })"
          ><template #option="{ option }"
            ><AppChannelIcon
              v-if="groupMap.get(option.value)"
              :icon="groupMap.get(option.value)!.channelIcon"
              :name="groupMap.get(option.value)!.channelName"
              :mark="groupMap.get(option.value)!.channelMark"
              size="sm"
            /><span>{{ option.label }}</span></template
          ></AppSearchSelect
        >
        <AppSelect
          class="modern-access-filter"
          :model-value="filters.expiry"
          :label="t('accessKeys.expires')"
          :label-hidden="!isMobile"
          :options="expiryOptions"
          :disabled="busy"
          @update:model-value="change({ expiry: $event as AccessFilters['expiry'] })" />
        <template v-if="isMobile">
          <AppSelect
            :model-value="filters.sort"
            :label="t('accessKeys.sort')"
            :options="sorts"
            :disabled="busy"
            @update:model-value="change({ sort: $event as AccessFilters['sort'] })" /></template
      ></AppFilterSheet>
      <AppIconButton
        v-if="isMobile"
        :icon="SlidersHorizontal"
        :label="t('ui.mobile.filters')"
        @click="mobileFilters = true"
      />
      <div class="modern-access-toolbar-actions">
        <AppSortMenu
          v-if="!isMobile"
          :model-value="filters.sort"
          :label="t('accessKeys.sort')"
          :options="sorts"
          :disabled="busy"
          @update:model-value="change({ sort: $event as AccessFilters['sort'] })"
        /><AppButton
          :icon="Plus"
          :icon-only="isMobile"
          :aria-label="t('accessKeys.create')"
          variant="primary"
          :disabled="busy"
          @click="openCreate"
          ><span v-if="!isMobile">{{ t('accessKeys.create') }}</span></AppButton
        >
      </div>
    </form>
    <div class="modern-access-filterbar">
      <AppSegmentedControl
        :model-value="filters.status"
        :label="t('accessKeys.state')"
        :options="states"
        :disabled="busy"
        @update:model-value="change({ status: $event as AccessFilters['status'] })"
      /><AppFilterSummary :items="summary" :disabled="busy" @remove="reset" @reset="reset()" />
    </div>
    <AppListFrame
      ref="frame"
      mobile-flow
      :label="t('accessKeys.title')"
      :loading="query.isFetching.value && Boolean(query.data.value)"
    >
      <template #header
        ><div class="modern-access-row modern-access-head" aria-hidden="true">
          <span>{{ t('accessKeys.name') }}</span
          ><span>{{ t('accessKeys.key') }}</span
          ><span>{{ t('accessKeys.restrictions') }}</span
          ><span>{{ t('rpm.hourPeakShort') }}</span
          ><span>{{ t('accessKeys.state') }}</span
          ><span>{{ t('accessKeys.recordRequestBody.column') }}</span
          ><span>{{ t('accessKeys.expires') }}</span
          ><span>{{ t('accessKeys.lastRequest') }}</span
          ><span>{{ t('accessKeys.actions') }}</span>
        </div></template
      >
      <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
      <AppCollectionState v-else-if="!query.data.value" :title="t('accessKeys.loadFailed')" error
        ><AppButton @click="query.refetch()">{{ t('ui.retry') }}</AppButton></AppCollectionState
      >
      <AppCollectionState
        v-else-if="!rows.length"
        :title="t(summary.length ? 'accessKeys.noResults' : 'accessKeys.empty')"
        :description="summary.length ? undefined : t('accessKeys.emptyHelp')"
        :icon="KeyRound"
        ><AppButton v-if="summary.length" @click="reset()">{{ t('collection.reset') }}</AppButton
        ><AppButton v-else :icon="Plus" variant="brand" @click="openCreate">{{
          t('accessKeys.create')
        }}</AppButton></AppCollectionState
      >
      <template v-else
        ><template v-for="row in rows" :key="row.id">
          <article v-if="isMobile" class="modern-access-mobile">
            <div class="modern-access-mobile-heading">
              <AppButton
                class="modern-access-mobile-open"
                variant="ghost"
                @click="setPanel('detail', row.id)"
              >
                <span class="modern-access-mobile-name">{{ row.name }}</span>
                <AppBadge :tone="accessState(row).tone" variant="plain" size="xs" dot>{{
                  t('accessKeys.' + accessState(row).key)
                }}</AppBadge>
                <AppIcon :icon="ChevronRight" size="sm" />
              </AppButton>
              <AppActionMenu
                :label="`${row.name} · ${t('accessKeys.actions')}`"
                :items="menus(row)"
                :disabled="mutating.has(row.id)"
                @select="action(row, $event)"
              />
            </div>
            <div class="modern-access-mobile-meta">
              <AppCopyValue
                :key="row.updated_at_ms"
                :value="row.masked_key"
                :resolve-value="resolveKey(row.id)"
                :label="t('accessKeys.copy')"
              /><span>{{
                row.expires_at_ms === null
                  ? t('accessKeys.never')
                  : accessTime(row.expires_at_ms, locale, false)
              }}</span>
            </div>
            <AccessKeyRestrictions :row="row" :groups="groupMap" />
            <AppBadge v-if="row.record_request_body" tone="warning" size="xs">{{
              t('accessKeys.recordRequestBody.enabled')
            }}</AppBadge>
          </article>
          <article v-else class="modern-access-row modern-access-item" :aria-label="row.name">
            <div class="modern-access-name">
              <AppButton variant="text" @click="setPanel('detail', row.id)"
                ><AppOverflowText :text="row.name"
              /></AppButton>
            </div>
            <div class="modern-access-key">
              <AppCopyValue
                :key="row.updated_at_ms"
                :value="row.masked_key"
                :resolve-value="resolveKey(row.id)"
                :label="t('accessKeys.copy')"
              />
            </div>
            <div class="modern-access-limits" :data-label="t('accessKeys.restrictions')">
              <AccessKeyRestrictions :row="row" :groups="groupMap" />
            </div>
            <div class="modern-access-rpm" :data-label="t('rpm.hourPeakShort')">
              <span v-if="row.rpmPeakHour !== undefined">{{ n(row.rpmPeakHour) }}</span>
            </div>
            <div :data-label="t('accessKeys.state')">
              <AppBadge :tone="accessState(row).tone" variant="plain" size="xs" dot>{{
                t('accessKeys.' + accessState(row).key)
              }}</AppBadge>
            </div>
            <div :data-label="t('accessKeys.recordRequestBody.column')">
              <span class="modern-sr-only">{{ t('accessKeys.recordRequestBody.column') }}</span>
              <AppBadge v-if="row.record_request_body" tone="warning" size="xs" dot>{{
                t('accessKeys.recordRequestBody.enabled')
              }}</AppBadge>
              <AppBadge v-else variant="plain" size="xs">{{
                t('accessKeys.recordRequestBody.disabled')
              }}</AppBadge>
            </div>
            <div class="modern-access-expiry" :data-label="t('accessKeys.expires')">
              <AppTooltip
                :label="
                  row.expires_at_ms === null ? undefined : accessTime(row.expires_at_ms, locale)
                "
                ><span :tabindex="row.expires_at_ms === null ? undefined : 0">{{
                  row.expires_at_ms === null
                    ? t('accessKeys.never')
                    : accessTime(row.expires_at_ms, locale, false)
                }}</span></AppTooltip
              >
            </div>
            <div class="modern-access-last-request" :data-label="t('accessKeys.lastRequest')">
              <AppOverflowText
                :text="accessTime(row.last_request_at_ms, locale)"
                :full-text="
                  row.last_request_at_ms
                    ? dateFormatter(locale, {
                        dateStyle: 'medium',
                        timeStyle: 'medium',
                      }).format(row.last_request_at_ms)
                    : undefined
                "
              />
            </div>
            <div class="modern-access-row-actions">
              <AppIconButton
                :icon="Pencil"
                :label="t('accessKeys.edit')"
                size="xs"
                :disabled="mutating.has(row.id)"
                @click="setPanel('detail', row.id)"
              /><AppActionMenu
                :label="t('accessKeys.actions')"
                :items="menus(row)"
                size="xs"
                :disabled="mutating.has(row.id)"
                @select="action(row, $event)"
              /><AppSwitch
                size="sm"
                :model-value="row.status === 'active'"
                :label="t('accessKeys.active')"
                :loading="mutating.has(row.id)"
                :disabled="mutating.has(row.id)"
                @update:model-value="toggle(row, $event)"
              />
            </div>
          </article> </template
      ></template>
      <template #footer
        ><AppPagination
          :page="filters.page"
          :page-size="filters.pageSize"
          mode="total"
          :total="query.data.value?.pagination.total_items"
          :pending="query.isFetching.value"
          :disabled="busy"
          @update:page="change({ page: $event })"
          @update:page-size="change({ pageSize: $event })"
      /></template>
    </AppListFrame>
  </div>
  <AccessKeyPanel
    v-if="panel === 'create' || ((panel === 'detail' || panel === 'copy') && selectedID)"
    :id="selectedID || undefined"
    :key="`${panel}-${selectedID}`"
    :mode="panel"
    :hint="filters"
    @close="setPanel(undefined)"
    @saved="saved"
    @deleted="deleted()"
  />
  <AccessKeyHandoff
    v-if="panel === 'share' && selectedID"
    :id="selectedID"
    :key="selectedID"
    :hint="filters"
    @close="setPanel(undefined)"
  />
  <AccessKeyQuotaResetDialog
    v-if="resetting"
    :key="resetting.id"
    :row="resetting"
    @pending="resetPending"
    @close="resetting = undefined"
    @reset="resetting = undefined"
  />
  <AppConfirmDialog
    :open="Boolean(deleting)"
    :title="t('accessKeys.delete')"
    :subject="deleting?.name"
    :description="t('accessKeys.deleteHelp')"
    :confirm-label="t('accessKeys.delete')"
    tone="danger"
    :pending="busy"
    :error="deleteError"
    @cancel="deleting = undefined"
    @confirm="remove"
  />
</template>
<style scoped>
.modern-access-mobile {
  display: grid;
  gap: var(--modern-space-2);
  padding: var(--modern-space-3) 0;
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-access-mobile-open {
  flex: 1;
  min-width: 0;
  justify-content: flex-start;
  padding: 0;
  color: var(--modern-text);
}
.modern-access-mobile-name {
  flex: 1;
  min-width: 0;
  text-align: left;
  white-space: normal;
  overflow-wrap: anywhere;
  font-weight: var(--modern-weight-semibold);
}
.modern-access-mobile-heading {
  display: flex;
  align-items: flex-start;
  gap: var(--modern-space-1);
}
.modern-access-mobile-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}

.modern-access-workspace {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
}
.modern-access-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  flex: none;
  gap: var(--modern-space-3);
  padding: var(--modern-space-5) 0 var(--modern-space-3);
}
.modern-access-search {
  flex: 2 1 240px;
  min-width: 0;
}
.modern-access-filter {
  flex: 1 1 160px;
  min-width: 0;
}
.modern-access-toolbar-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  margin-left: auto;
}
.modern-access-filterbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  flex: none;
  justify-content: space-between;
  gap: var(--modern-space-2);
  padding-bottom: var(--modern-space-4);
}
.modern-access-row {
  display: grid;
  grid-template-columns:
    minmax(150px, 1.5fr) minmax(150px, 1.1fr) minmax(380px, 2.4fr)
    112px 96px 96px 110px minmax(148px, 1fr) 116px;
  align-items: center;
  gap: var(--modern-space-3);
  min-width: 1472px;
  padding-inline: var(--modern-space-2);
  text-align: left;
}
.modern-access-head {
  min-height: var(--modern-control-nav);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-access-item {
  min-height: calc(var(--modern-space-12) + var(--modern-space-2));
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  padding-block: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-access-item:hover {
  background: var(--modern-control-hover);
}
.modern-access-row > div {
  min-width: 0;
}
.modern-access-name :deep(.modern-button) {
  max-width: 100%;
  font-weight: var(--modern-weight-medium);
}
.modern-access-key {
  font-family: var(--modern-font-mono);
  color: var(--modern-muted);
}
.modern-access-rpm {
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-access-last-request {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-access-expiry {
  color: var(--modern-muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.modern-access-row-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
}
@media (max-width: 760px) {
  .modern-access-workspace {
    flex: none;
  }
  .modern-access-toolbar {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--modern-space-2);
    padding-top: var(--modern-space-3);
  }
  .modern-access-search {
    flex-basis: 100%;
  }
  .modern-access-search,
  .modern-access-toolbar-actions {
    grid-column: 1 / -1;
  }
  .modern-access-filter {
    flex: 1 1 0;
  }
  .modern-access-row {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    min-width: 0;
    gap: var(--modern-space-3);
  }
  .modern-access-head {
    display: none;
  }
  .modern-access-item {
    padding: var(--modern-space-4) var(--modern-space-1);
  }
  .modern-access-name,
  .modern-access-key,
  .modern-access-limits,
  .modern-access-last-request,
  .modern-access-row-actions {
    grid-column: 1 / -1;
  }
  .modern-access-item > [data-label] {
    display: grid;
    justify-items: start;
    align-content: start;
    gap: var(--modern-space-1);
    white-space: normal;
  }
  .modern-access-item > [data-label]::before {
    content: attr(data-label);
    color: var(--modern-muted);
    font-size: var(--modern-font-size-small);
    font-weight: var(--modern-weight-regular);
  }
  .modern-access-row-actions {
    border-top: var(--modern-line-width) solid var(--modern-border);
    padding-top: var(--modern-space-2);
  }
  .modern-access-row-actions > :last-child {
    margin-left: auto;
  }
}
@media (max-width: 760px) {
  .modern-access-toolbar {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    gap: var(--modern-space-2);
  }
  .modern-access-toolbar-actions {
    display: contents;
  }
  .modern-access-search {
    grid-column: auto;
  }
}
</style>
