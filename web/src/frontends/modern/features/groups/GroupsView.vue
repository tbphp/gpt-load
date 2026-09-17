<script setup lang="ts">
import { Layers2, Plus, Search, SlidersHorizontal, TriangleAlert } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  isNavigationFailure,
  onBeforeRouteLeave,
  onBeforeRouteUpdate,
  useRoute,
  useRouter,
} from 'vue-router'
import {
  getGroupUsage,
  getCredentialOptions,
  credentialOptionsKey,
  getGroupWorkspace,
  groupQueryKey,
  groupSorts,
  groupViews,
  isPaused,
  isServing,
  needsAttention,
  updateGroupBasics,
  type GroupBasics,
  type GroupBasicsPatch,
  type GroupFilters,
  type GroupRow,
  type GroupWorkspace,
} from '@modern/api/groups'
import { useMessageSource } from '@modern/app/messages'
import { useURLState } from '@modern/app/url-state'
import { usePageRefresh } from '@modern/app/page-refresh'
import { useLoadingFeedback } from '@modern/components/ui/loading'
import {
  AppAdvancedFilters,
  AppAdvancedFilterSection,
  AppButton,
  AppChannelIcon,
  AppCollectionState,
  AppConfirmDialog,
  AppFilterSummary,
  AppListFrame,
  AppModelSelect,
  AppIconButton,
  AppPagination,
  AppProtocolTag,
  AppOverflowText,
  AppSearchSelect,
  AppSegmentedControl,
  AppSortMenu,
  AppSelect,
  AppTextField,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import GroupListRow from './GroupListRow.vue'
import GroupCreatePanel from './GroupCreatePanel.vue'
import { getGroupChannels, type GroupCreateResult } from '@modern/api/group-create'
import { channelSearchOption } from '@modern/components/channel-options'
import { protocolLabel, protocolOrder } from '@modern/i18n/protocols'
import { groupFilterQuery as serializeGroupFilters, parseGroupFilters } from './group-route'

const { t, n, locale } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()
const route = useRoute()
const router = useRouter()
const filters = computed(() => parseGroupFilters(route.query))
const search = ref(filters.value.q)
const moreFilterState = useURLState(
  ['filters'],
  (query) => ({ more: query.filters === '1' }),
  (value) => (value.more ? { filters: '1' } : {}),
)
const moreFilters = computed({
  get: () => moreFilterState.value.more,
  set: (more: boolean) => {
    moreFilterState.value = { more }
  },
})
const filtering = ref(false)
const refreshing = ref(false)
const filterLoading = useLoadingFeedback(filtering)
const listLoading = computed(() => filtering.value || refreshing.value)
const composing = ref(false)
const expansion = useURLState(
  ['expanded'],
  (query) => ({
    ids:
      typeof query.expanded === 'string'
        ? [
            ...new Set(
              query.expanded
                .split(',')
                .map(Number)
                .filter((id) => Number.isSafeInteger(id) && id > 0),
            ),
          ]
        : [],
  }),
  (value) => (value.ids.length ? { expanded: value.ids.join(',') } : {}),
)
const expanded = computed(() => new Set(expansion.value.ids))
const creating = computed({
  get: () => route.query.panel === 'create',
  set: (value) => {
    void setCreating(value)
  },
})
function setCreating(value: boolean) {
  const query = { ...route.query }
  if (value) query.panel = 'create'
  else {
    delete query.panel
    delete query.pick_models
  }
  return router.replace({ query })
}
function workspaceQuery(value: GroupFilters) {
  const query = { ...route.query }
  for (const key of [
    'page',
    'page_size',
    'q',
    'view',
    'channel',
    'sort',
    'connection',
    'model',
    'credential_id',
    'credential_key',
    'protocol',
  ])
    delete query[key]
  return { ...query, ...serializeGroupFilters(value) }
}
const pending = ref(new Map<number, 'toggle' | 'weight'>())
const enabledOverrides = ref(new Map<number, boolean>())
const weightErrors = ref(new Map<number, string>())
const weightEditors = ref(new Set<number>())
const dirtyWeights = ref(new Set<number>())
const frozenGroups = ref<GroupRow[]>()
const rowRevision = ref(0)
const notice = ref<{ text: string; tone: 'success' | 'warning' | 'danger' }>()
const listFrame = ref<InstanceType<typeof AppListFrame>>()
const discardRequested = ref(false)
let resolveLeave: ((allow: boolean) => void) | undefined
let discardTrigger: HTMLElement | null = null
let createTrigger: HTMLElement | undefined
let searchTimer: ReturnType<typeof setTimeout> | undefined
let filterSequence = 0
const controller = new AbortController()
const query = useQuery(
  computed(() => ({
    queryKey: groupQueryKey,
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const result = await getGroupWorkspace(client, signal)
      if (!signal.aborted)
        for (const id of enabledOverrides.value.keys()) {
          if (!pending.value.has(id)) enabledOverrides.value.delete(id)
        }
      return result
    },
  })),
)
const data = query.data
const channelCatalog = useQuery({
  queryKey: ['modern', 'group-channels'],
  queryFn: ({ signal }) => getGroupChannels(client, signal),
})
const channelKeywords = computed(
  () => new Map(channelCatalog.data.value?.map((channel) => [channel.id, channel.keywords])),
)
const groups = computed(() => data.value?.items ?? [])
const credentialCatalog = useQuery({
  queryKey: credentialOptionsKey,
  queryFn: ({ signal }) => getCredentialOptions(client, signal),
  enabled: computed(() => moreFilters.value || Boolean(filters.value.credential)),
})
const selectedCredential = computed(() =>
  credentialCatalog.data.value?.find((option) => option.key === filters.value.credential),
)
const credentialOptions = computed(() => [
  { value: '', label: t('groups.board.allCredentials') },
  ...(credentialCatalog.data.value ?? []).map((option) => ({
    value: option.key,
    label: option.label,
    keywords: [
      channelInfo.value.get(option.channelID)?.name ?? option.channelID,
      ...option.groupIDs.map((id) => groups.value.find((group) => group.id === id)?.name ?? ''),
    ],
  })),
  ...(filters.value.credential && !selectedCredential.value
    ? [{ value: filters.value.credential, label: t('groups.board.selectedCredential') }]
    : []),
])
const protocolOptions = computed(() => [
  { value: '', label: t('groups.board.allProtocols') },
  ...protocolOrder.map((value) => ({ value, label: protocolLabel(value, t) })),
])
const credentialFiltering = computed(() => Boolean(filters.value.credential))
const credentialFilterLoading = computed(
  () => credentialFiltering.value && credentialCatalog.isPending.value,
)
const filterDataReady = computed(
  () =>
    (!filters.value.credential || Boolean(credentialCatalog.data.value)) &&
    (!filters.value.protocol || Boolean(channelCatalog.data.value)),
)
const counts = computed(() => ({
  all: groups.value.length,
  serving: groups.value.filter(isServing).length,
  attention: groups.value.filter(needsAttention).length,
  paused: groups.value.filter(isPaused).length,
}))
const channelInfo = computed(() => {
  const info = new Map(
    groups.value.map((group) => [
      group.channelID,
      { icon: group.channelIcon, mark: group.channelMark, name: group.channelName },
    ]),
  )
  for (const channel of channelCatalog.data.value ?? [])
    info.set(channel.id, { icon: channel.icon, mark: channel.mark, name: channel.name })
  return info
})
const channels = computed(() => [
  { value: '', label: t('groups.board.allChannels') },
  ...Array.from(channelInfo.value, ([id, info]) =>
    channelSearchOption({ id, name: info.name, keywords: channelKeywords.value.get(id) }),
  ).sort((a, b) => a.label.localeCompare(b.label, locale.value)),
])
const sortOptions = computed(() =>
  groupSorts.map((value) => ({ value, label: t('groups.board.sort.' + value) })),
)
const connectionOptions = computed(() => [
  { value: '', label: t('groupDetail.filters.allChannelTypes') },
  ...['api_key', 'subscription'].map((value) => ({
    value,
    label: t('groups.connection.' + value),
  })),
])
const modelNames = computed(() => [...new Set(groups.value.flatMap((group) => group.modelNames))])
const viewOptions = computed(() =>
  groupViews.map((value) => ({
    value,
    label: t('groups.board.views.' + value),
    count: data.value ? n(counts.value[value]) : undefined,
  })),
)
const filtered = computed(() => {
  if (frozenGroups.value) {
    const latest = new Map(groups.value.map((group) => [group.id, group]))
    return frozenGroups.value.map((group) => latest.get(group.id) ?? group)
  }
  const f = filters.value
  const words = f.q.toLocaleLowerCase().split(/\s+/u).filter(Boolean)
  const rank = (group: GroupRow) => (isPaused(group) ? 2 : needsAttention(group) ? 0 : 1)
  return groups.value
    .filter((group) => {
      if (f.view === 'serving' && !isServing(group)) return false
      if (f.view === 'attention' && !needsAttention(group)) return false
      if (f.view === 'paused' && !isPaused(group)) return false
      if (f.channel && f.channel !== group.channelID) return false
      if (f.connection && f.connection !== group.connectionType) return false
      if (
        f.protocol &&
        !channelCatalog.data.value
          ?.find((channel) => channel.id === group.channelID)
          ?.nativeProtocols.includes(f.protocol)
      )
        return false
      if (f.credential && !selectedCredential.value?.groupIDs.includes(group.id)) return false
      if (
        f.model &&
        !group.modelNames.some((name) =>
          name.toLocaleLowerCase().includes(f.model.toLocaleLowerCase()),
        )
      )
        return false
      const text = [group.name, group.channelName, group.channelID, group.endpoint]
        .join(' ')
        .toLocaleLowerCase()
      return words.every((word) => text.includes(word))
    })
    .sort((a, b) => {
      if (f.sort === 'priority' && rank(a) !== rank(b)) return rank(a) - rank(b)
      if (f.sort !== 'name' && a.lastActiveHour !== b.lastActiveHour)
        return (b.lastActiveHour ?? -1) - (a.lastActiveHour ?? -1)
      return a.name.localeCompare(b.name, locale.value) || a.id - b.id
    })
})
const maxPage = computed(() =>
  Math.max(1, Math.ceil(filtered.value.length / filters.value.pageSize)),
)
const page = computed(() =>
  data.value && filterDataReady.value
    ? Math.min(filters.value.page, maxPage.value)
    : filters.value.page,
)
const visible = computed(() =>
  filtered.value.slice(
    (page.value - 1) * filters.value.pageSize,
    page.value * filters.value.pageSize,
  ),
)
const usageIDs = computed(() => visible.value.map((group) => group.id).sort((a, b) => a - b))
const usage = useQuery(
  computed(() => {
    const ids = usageIDs.value
    return {
      queryKey: ['modern', 'group-usage', ...ids],
      queryFn: ({ signal }: { signal: AbortSignal }) => getGroupUsage(client, ids, signal),
      enabled: ids.length > 0,
    }
  }),
)
const usageByID = computed(
  () => new Map(usage.data.value?.items.map((item) => [item.id, item]) ?? []),
)
const activeFilters = computed(() => [
  ...(filters.value.credential
    ? [
        {
          key: 'credential',
          label: t('groups.board.credentialFilter'),
          value: selectedCredential.value?.label ?? t('groups.board.selectedCredential'),
        },
      ]
    : []),
  ...(filters.value.protocol
    ? [
        {
          key: 'protocol',
          label: t('groups.board.protocolFilter'),
          value: protocolLabel(filters.value.protocol, t),
        },
      ]
    : []),
  ...(filters.value.connection
    ? [
        {
          key: 'connection',
          label: t('groupDetail.filters.channelType'),
          value: t('groups.connection.' + filters.value.connection),
        },
      ]
    : []),
  ...(filters.value.model
    ? [{ key: 'model', label: t('groupDetail.models'), value: filters.value.model }]
    : []),
  ...(filters.value.q
    ? [{ key: 'q', label: t('ui.filters.keyword'), value: filters.value.q }]
    : []),
  ...(filters.value.view !== 'all'
    ? [
        {
          key: 'view',
          label: t('ui.filters.status'),
          value: t('groups.board.views.' + filters.value.view),
        },
      ]
    : []),
  ...(filters.value.channel
    ? [
        {
          key: 'channel',
          label: t('groups.board.channel'),
          value: channelInfo.value.get(filters.value.channel)?.name ?? filters.value.channel,
        },
      ]
    : []),
  ...(filters.value.sort !== 'recent'
    ? [
        {
          key: 'sort',
          label: t('groups.sort.label'),
          value: t('groups.board.sort.' + filters.value.sort),
        },
      ]
    : []),
])
const changedFilters = computed(() => activeFilters.value.length > 0)
function removeFilter(key: string): void {
  if (key === 'credential') void updateFilters({ credential: '' })
  else if (key === 'protocol') void updateFilters({ protocol: '' })
  else if (key === 'connection') void updateFilters({ connection: '' })
  else if (key === 'model') void updateFilters({ model: '' })
  else if (key === 'q') {
    search.value = ''
    void updateFilters({ q: '' })
  } else if (key === 'view') void updateFilters({ view: 'all' })
  else if (key === 'channel') void updateFilters({ channel: '' })
  else if (key === 'sort') void updateFilters({ sort: 'recent' })
}

usePageRefresh({
  refresh: refresh,
  pending: () =>
    query.isFetching.value ||
    usage.isFetching.value ||
    channelCatalog.isFetching.value ||
    credentialCatalog.isFetching.value ||
    filtering.value ||
    pending.value.size > 0,
  updatedAt: () => data.value?.observedAt,
})
async function refresh(): Promise<void> {
  if (weightEditors.value.size) {
    notice.value = { tone: 'warning', text: t('groups.row.finishEditing') }
    return
  }
  refreshing.value = true
  try {
    await query.refetch()
    await nextTick()
    if (!controller.signal.aborted) {
      await Promise.all([
        channelCatalog.refetch(),
        moreFilters.value || credentialFiltering.value ? credentialCatalog.refetch() : undefined,
        usageIDs.value.length ? usage.refetch() : undefined,
        queryClient.invalidateQueries({
          queryKey: ['modern', 'group-model-names'],
          refetchType: 'active',
        }),
      ])
    }
  } finally {
    refreshing.value = false
  }
}
async function updateFilters(patch: Partial<GroupFilters>, replace = false): Promise<void> {
  clearTimeout(searchTimer)
  const next = {
    ...filters.value,
    q: Array.from(search.value.trim()).slice(0, 200).join(''),
    page: 1,
    ...patch,
  }
  const location = { name: 'modern-groups', query: workspaceQuery(next) }
  const operation = ++filterSequence
  filtering.value = true
  try {
    const result = await (replace ? router.replace(location) : router.push(location))
    if (isNavigationFailure(result) && operation === filterSequence) search.value = filters.value.q
    await nextTick()
  } finally {
    if (operation === filterSequence) filtering.value = false
  }
}
function applySearch(): void {
  if (!composing.value) void updateFilters({}, true)
}
function scheduleSearch(): void {
  clearTimeout(searchTimer)
  if (!composing.value) searchTimer = setTimeout(applySearch, 200)
}
function endComposition(): void {
  composing.value = false
  scheduleSearch()
}
function resetFilters(): void {
  search.value = ''
  void updateFilters({
    q: '',
    channel: '',
    connection: '',
    model: '',
    credential: '',
    protocol: '',
    view: 'all',
    sort: 'recent',
  })
}
function toggleExpanded(id: number): void {
  expansion.value = {
    ids: expanded.value.has(id)
      ? expansion.value.ids.filter((value) => value !== id)
      : [...expansion.value.ids, id],
  }
}
function weightEditing(id: number, active: boolean): void {
  if (active) {
    if (!weightEditors.value.size) frozenGroups.value = [...filtered.value]
    weightEditors.value.add(id)
  } else {
    weightEditors.value.delete(id)
    dirtyWeights.value.delete(id)
    if (!weightEditors.value.size) frozenGroups.value = undefined
  }
}
function weightDirty(id: number, dirty: boolean): void {
  if (dirty) dirtyWeights.value.add(id)
  else dirtyWeights.value.delete(id)
}
function guardNavigation(): boolean | Promise<boolean> {
  if (pending.value.size) return false
  if (!dirtyWeights.value.size) return true
  resolveLeave?.(false)
  discardTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  discardRequested.value = true
  return new Promise((resolve) => {
    resolveLeave = resolve
  })
}
function finishDiscard(allow: boolean): void {
  if (allow) {
    dirtyWeights.value.clear()
    weightEditors.value.clear()
    weightErrors.value.clear()
    frozenGroups.value = undefined
    rowRevision.value++
  }
  discardRequested.value = false
  resolveLeave?.(allow)
  resolveLeave = undefined
}
function restoreDiscardFocus(event: Event): void {
  event.preventDefault()
  if (discardTrigger?.isConnected) discardTrigger.focus({ preventScroll: true })
}
onBeforeRouteLeave(guardNavigation)
onBeforeRouteUpdate(guardNavigation)
function beforeUnload(event: BeforeUnloadEvent): void {
  if (!dirtyWeights.value.size && !pending.value.size) return
  event.preventDefault()
  event.returnValue = ''
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
watch(
  () => route.fullPath,
  (_path, previousPath) => {
    if (route.name !== 'modern-groups') return
    // 仅重置仍在编辑的行内控件；普通筛选复用已有列表行，避免图标重复挂载。
    if (weightEditors.value.size) rowRevision.value++
    clearTimeout(searchTimer)
    search.value = filters.value.q
    weightEditors.value.clear()
    dirtyWeights.value.clear()
    weightErrors.value.clear()
    frozenGroups.value = undefined
    if (previousPath !== undefined) void nextTick(() => listFrame.value?.scrollToTop())
    const canonical = workspaceQuery(filters.value)
    if (
      Object.keys(route.query).length !== Object.keys(canonical).length ||
      Object.keys(canonical).some((key) => canonical[key] !== route.query[key])
    ) {
      void router.replace({ name: 'modern-groups', query: canonical })
    }
  },
  { immediate: true },
)
watch([page, () => data.value !== undefined, filterDataReady], () => {
  if (
    data.value &&
    filterDataReady.value &&
    page.value !== filters.value.page &&
    !dirtyWeights.value.size &&
    !pending.value.size
  )
    void router.replace({
      name: 'modern-groups',
      query: workspaceQuery({ ...filters.value, page: page.value }),
    })
})
onScopeDispose(() => {
  controller.abort()
  clearTimeout(searchTimer)
  resolveLeave?.(false)
  window.removeEventListener('beforeunload', beforeUnload)
})

async function refreshGroup(id: number, settings: GroupBasics): Promise<void> {
  enabledOverrides.value.set(id, settings.enabled)
  queryClient.setQueryData<GroupWorkspace>(
    groupQueryKey,
    (previous) =>
      previous && {
        ...previous,
        items: previous.items.map((group) =>
          group.id === id
            ? {
                ...group,
                name: settings.name,
                enabled: settings.enabled,
                weight: settings.weight ?? 50,
                priceMultiplier: settings.priceMultiplier,
              }
            : group,
        ),
      },
  )
  const result = await query.refetch()
  if (!controller.signal.aborted && !result.isError) enabledOverrides.value.delete(id)
}
async function mutate(
  group: GroupRow,
  patch: GroupBasicsPatch,
  kind: 'toggle' | 'weight',
): Promise<void> {
  if (pending.value.has(group.id)) return
  pending.value.set(group.id, kind)
  weightErrors.value.delete(group.id)
  notice.value = undefined
  if (patch.enabled !== undefined) enabledOverrides.value.set(group.id, patch.enabled)
  try {
    await queryClient.cancelQueries({ queryKey: groupQueryKey })
    if (controller.signal.aborted) return
    const settings = await updateGroupBasics(client, group.id, patch, controller.signal)
    if (controller.signal.aborted) return
    await refreshGroup(group.id, settings)
  } catch {
    if (!controller.signal.aborted) {
      enabledOverrides.value.delete(group.id)
      if (kind === 'weight') weightErrors.value.set(group.id, t('groups.row.weightFailed'))
      else notice.value = { tone: 'danger', text: t('groups.operationFailed') }
    }
  } finally {
    pending.value.delete(group.id)
  }
}
async function openCreate(event: MouseEvent): Promise<void> {
  const trigger = event.currentTarget as HTMLElement
  if (!(await guardNavigation()) || controller.signal.aborted) return
  await setCreating(true)
  createTrigger = trigger
}
async function closeCreate(): Promise<void> {
  await setCreating(false)
  await nextTick()
  if (!controller.signal.aborted && createTrigger?.isConnected)
    createTrigger.focus({ preventScroll: true })
}
async function locateCreated(id: number): Promise<void> {
  await closeCreate()
  const result = await query.refetch()
  if (moreFilters.value || credentialFiltering.value) await credentialCatalog.refetch()
  if (controller.signal.aborted) return
  if (result.isError) {
    notice.value = {
      tone: 'warning',
      text: [notice.value?.text, t('groupCreate.resultReloadFailed')].filter(Boolean).join(' '),
    }
    return
  }
  if (creating.value) return
  const group = result.data?.items.find((item) => item.id === id)
  if (group) {
    search.value = group.name
    await updateFilters({
      q: group.name,
      channel: '',
      connection: '',
      model: '',
      credential: '',
      protocol: '',
      view: 'all',
      page: 1,
    })
  }
}
async function onCreated(result: GroupCreateResult, appended: boolean): Promise<void> {
  const completedNotice = {
    tone: 'success' as const,
    text: t(appended ? 'groupCreate.resultAppended' : 'groupCreate.resultCreated', {
      name: result.name,
      added: n(result.added),
      duplicated: n(result.duplicated),
    }),
  }
  notice.value = undefined
  await locateCreated(result.id)
  if (!notice.value) notice.value = completedNotice
}
async function onLocated(id: number): Promise<void> {
  notice.value = { tone: 'warning', text: t('groupCreate.resultKnown') }
  await locateCreated(id)
}
useMessageSource(() =>
  notice.value ? { text: notice.value.text, tone: notice.value.tone } : undefined,
)
useMessageSource(() =>
  query.isError.value && data.value
    ? {
        text: t('collection.stale'),
        tone: 'warning',
        action: { label: t('collection.retry'), run: refresh },
      }
    : undefined,
)
useMessageSource(() =>
  credentialCatalog.isError.value
    ? {
        text: t('groups.board.credentialsFailed'),
        tone: 'warning',
        action: { label: t('collection.retry'), run: () => credentialCatalog.refetch() },
      }
    : undefined,
)
useMessageSource(() =>
  channelCatalog.isError.value && filters.value.protocol
    ? {
        text: t('groupDetail.channelFailed'),
        tone: 'warning',
        action: { label: t('collection.retry'), run: () => channelCatalog.refetch() },
      }
    : undefined,
)
useMessageSource(() =>
  usage.isError.value
    ? {
        text: t(usage.data.value ? 'groups.row.usageStale' : 'groups.row.usageFailed'),
        tone: 'warning',
        action: { label: t('collection.retry'), run: () => usage.refetch() },
      }
    : undefined,
)
</script>

<template>
  <div class="modern-groups-workspace">
    <form
      class="modern-groups-toolbar"
      role="search"
      :aria-label="t('groups.search')"
      @submit.prevent="applySearch"
    >
      <div class="modern-groups-search">
        <AppTextField
          v-model="search"
          :label="t('groups.search')"
          label-hidden
          :icon="Search"
          :loading="filterLoading"
          type="search"
          :placeholder="t('groups.board.search')"
          @input="scheduleSearch"
          @compositionstart="composing = true"
          @compositionend="endComposition"
        />
      </div>
      <AppSearchSelect
        class="modern-groups-channel"
        :model-value="filters.channel"
        :label="t('groups.board.channel')"
        label-hidden
        :options="channels"
        @update:model-value="updateFilters({ channel: $event })"
      >
        <template #option="{ option }">
          <AppChannelIcon
            v-if="channelInfo.has(option.value)"
            :icon="channelInfo.get(option.value)?.icon"
            :mark="channelInfo.get(option.value)?.mark"
            :name="channelInfo.get(option.value)?.name"
          />
          <span>{{ option.label }}</span>
        </template>
      </AppSearchSelect>
      <AppModelSelect
        class="modern-groups-model"
        :model-value="filters.model"
        :label="t('groupDetail.models')"
        label-hidden
        :models="modelNames"
        fuzzy
        @update:model-value="updateFilters({ model: $event }, true)"
      />
      <div class="modern-groups-toolbar-actions">
        <AppIconButton
          :icon="SlidersHorizontal"
          :label="t('groups.board.moreFilters')"
          :aria-expanded="moreFilters"
          aria-controls="modern-groups-more-filters"
          @click="moreFilters = !moreFilters"
        />
        <AppSortMenu
          :model-value="filters.sort"
          :label="t('groups.sort.label')"
          :options="sortOptions"
          :disabled="pending.size > 0"
          @update:model-value="updateFilters({ sort: $event as GroupFilters['sort'] })"
        />
        <AppButton
          variant="primary"
          :icon="Plus"
          :disabled="pending.size > 0"
          @click="openCreate"
          >{{ t('groups.create') }}</AppButton
        >
      </div>
    </form>
    <AppAdvancedFilters id="modern-groups-more-filters" :open="moreFilters">
      <AppAdvancedFilterSection :title="t('groups.board.filterSection')">
        <AppSelect
          :model-value="filters.connection"
          :label="t('groupDetail.filters.channelType')"
          :options="connectionOptions"
          size="xs"
          @update:model-value="updateFilters({ connection: $event as GroupFilters['connection'] })"
        />
        <AppSearchSelect
          :model-value="filters.credential"
          :label="t('groups.board.credentials')"
          :placeholder="t('groups.board.searchCredentials')"
          :options="credentialOptions"
          :loading="credentialCatalog.isFetching.value"
          size="xs"
          @update:model-value="updateFilters({ credential: $event })"
        />
        <AppSelect
          :model-value="filters.protocol"
          :label="t('groups.board.protocolFilter')"
          :options="protocolOptions"
          size="xs"
          @update:model-value="updateFilters({ protocol: $event })"
        >
          <template #value="{ value, label }">
            <AppProtocolTag v-if="value" :protocol="value" />
            <AppOverflowText v-else :text="label" />
          </template>
          <template #option="{ option }">
            <AppProtocolTag v-if="option.value" :protocol="option.value" />
            <span v-else>{{ option.label }}</span>
          </template>
        </AppSelect>
      </AppAdvancedFilterSection>
    </AppAdvancedFilters>
    <div class="modern-groups-filterbar">
      <AppSegmentedControl
        :label="t('groups.statusFilter')"
        :model-value="filters.view"
        :options="viewOptions"
        @update:model-value="updateFilters({ view: $event as GroupFilters['view'] })"
      />
      <div class="modern-groups-filter-actions">
        <AppButton
          v-if="expanded.size"
          variant="ghost"
          size="sm"
          @click="expansion = { ids: [] }"
          >{{ t('groups.board.collapseAll') }}</AppButton
        >
        <AppFilterSummary
          :items="activeFilters"
          :disabled="pending.size > 0"
          @remove="removeFilter"
          @reset="resetFilters"
        />
      </div>
    </div>
    <AppListFrame
      ref="listFrame"
      :label="t('groups.list')"
      :scroll-key="route.fullPath"
      :loading="
        Boolean(data) &&
        (listLoading ||
          credentialFilterLoading ||
          (Boolean(filters.protocol) && channelCatalog.isFetching.value))
      "
    >
      <template #header>
        <div class="modern-group-list-head" aria-hidden="true">
          <span>{{ t('groups.row.group') }}</span>
          <span>{{ t('groups.row.credentialsHeader') }}</span>
          <span>{{ t('groups.columns.models') }}</span>
          <span>{{ t('groups.row.requests24h') }}</span>
          <span>{{ t('groups.row.usage24h') }}</span>
          <span class="modern-group-list-actions-head"
            ><span>{{ t('groups.row.enabled') }}</span
            ><span>{{ t('groups.row.weight') }}</span></span
          >
        </div>
      </template>
      <AppCollectionState
        v-if="
          query.isPending.value ||
          credentialFilterLoading ||
          (filters.protocol && channelCatalog.isPending.value)
        "
        :title="t('collection.loading')"
        loading
      />
      <AppCollectionState
        v-else-if="!data"
        :title="t('groups.loadFailed')"
        :description="t('collection.loadFailedHelp')"
        :icon="TriangleAlert"
        error
        ><AppButton @click="query.refetch()">{{
          t('collection.retry')
        }}</AppButton></AppCollectionState
      >
      <AppCollectionState
        v-else-if="!filterDataReady"
        :title="
          t(
            filters.credential && !credentialCatalog.data.value
              ? 'groups.board.credentialsFailed'
              : 'groupDetail.channelFailed',
          )
        "
        :icon="TriangleAlert"
        error
        ><AppButton @click="refresh">{{ t('collection.retry') }}</AppButton></AppCollectionState
      >
      <AppCollectionState
        v-else-if="!filtered.length"
        :title="t(data.items.length ? 'groups.noResults' : 'groups.empty')"
        :description="t(data.items.length ? 'groups.noResultsHelp' : 'groups.emptyHelp')"
        :icon="data.items.length ? Search : Layers2"
      >
        <AppButton v-if="changedFilters" @click="resetFilters">{{
          t('collection.reset')
        }}</AppButton>
      </AppCollectionState>
      <template v-else>
        <GroupListRow
          v-for="group in visible"
          :key="group.id + ':' + rowRevision"
          :group="group"
          :expanded="expanded.has(group.id)"
          :pending="pending.get(group.id)"
          :enabled-override="enabledOverrides.get(group.id)"
          :usage="usageByID.get(group.id)"
          :usage-loading="usage.isFetching.value"
          :usage-incomplete="usage.data.value?.incomplete ?? false"
          :weight-error="weightErrors.get(group.id)"
          @expand="toggleExpanded(group.id)"
          @toggle="mutate(group, { enabled: $event }, 'toggle')"
          @weight="mutate(group, { weight_manual: $event }, 'weight')"
          @weight-editing="weightEditing(group.id, $event)"
          @weight-dirty="weightDirty(group.id, $event)"
          @clear-weight-error="weightErrors.delete(group.id)"
        />
      </template>
      <template #footer>
        <AppPagination
          mode="total"
          :page="page"
          :page-size="filters.pageSize"
          :total="data && filterDataReady ? filtered.length : undefined"
          :pending="query.isPending.value || !filterDataReady || filtering || pending.size > 0"
          @update:page="updateFilters({ page: $event })"
          @update:page-size="updateFilters({ pageSize: $event })"
        />
      </template>
    </AppListFrame>
    <GroupCreatePanel
      v-if="creating"
      :initial-channel="filters.channel"
      @close="closeCreate"
      @created="onCreated"
      @located="onLocated"
    />
    <AppConfirmDialog
      :open="discardRequested"
      :title="t('groups.edit.unsaved')"
      :description="t('groups.row.finishEditing')"
      :cancel-label="t('groups.edit.keepEditing')"
      :confirm-label="t('groups.edit.discard')"
      tone="danger"
      @cancel="finishDiscard(false)"
      @confirm="finishDiscard(true)"
      @close-auto-focus="restoreDiscardFocus"
    />
  </div>
</template>

<style scoped>
.modern-groups-workspace {
  --modern-group-list-width: 1024px;
  --modern-group-action-columns: 36px var(--modern-inline-number-width);
  --modern-group-columns: minmax(260px, 2fr) minmax(168px, 1fr) 90px 114px 140px 138px;
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
}
.modern-groups-toolbar {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-3);
  padding: var(--modern-space-5) 0 var(--modern-space-3);
}
.modern-groups-search {
  flex: 2 1 240px;
  min-width: 0;
}
.modern-groups-channel {
  flex: 1 1 180px;
  min-width: 0;
}
.modern-groups-model {
  flex: 1.3 1 200px;
  min-width: 0;
}
.modern-groups-toolbar > :last-child {
  flex: none;
  margin-left: auto;
}
.modern-groups-toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  max-width: 100%;
  gap: var(--modern-space-2);
}
.modern-groups-filterbar {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  padding-bottom: var(--modern-space-4);
}
.modern-groups-filter-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}

.modern-groups-notices {
  display: grid;
  flex: none;
  gap: var(--modern-space-2);
  padding-bottom: var(--modern-space-3);
}
.modern-group-list-head {
  display: grid;
  text-align: left;
  grid-template-columns: var(--modern-group-columns);
  min-width: var(--modern-group-list-width);
  align-items: center;
  gap: var(--modern-space-4);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-surface);
  padding: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-list-actions-head {
  display: grid;
  grid-template-columns: var(--modern-group-action-columns);
  justify-items: start;
  gap: var(--modern-space-4);
  text-align: left;
}
@media (max-width: 1150px) {
  .modern-groups-search {
    flex-basis: 100%;
  }
}
@media (max-width: 760px) {
  .modern-groups-workspace {
    --modern-group-action-columns: 44px 112px;
  }
  .modern-groups-toolbar {
    gap: var(--modern-space-2);
  }
  .modern-group-list-head {
    display: none;
  }
}
@media (max-width: 420px) {
  .modern-groups-toolbar > :last-child {
    margin-left: auto;
  }
}
</style>
