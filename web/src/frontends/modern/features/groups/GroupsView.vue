<script setup lang="ts">
import { Layers2, Plus, Search, TriangleAlert, X } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import {
  getGroupWorkspace,
  groupQueryKey,
  groupSorts,
  groupViews,
  isPaused,
  isServing,
  needsAttention,
  updateGroupBasics,
  type GroupBasics,
  type GroupFilters,
  type GroupRow,
  type GroupWorkspace,
} from '@modern/api/groups'
import { usePageRefresh } from '@modern/app/page-refresh'
import AppButton from '@modern/components/ui/AppButton.vue'
import AppCollectionState from '@modern/components/ui/AppCollectionState.vue'
import AppIcon from '@modern/components/ui/AppIcon.vue'
import AppIconButton from '@modern/components/ui/AppIconButton.vue'
import AppNotice from '@modern/components/ui/AppNotice.vue'
import AppSelect from '@modern/components/ui/AppSelect.vue'
import AppTextField from '@modern/components/ui/AppTextField.vue'
import { useApiClient } from '@shared/http/client-context'
import GroupCard from './GroupCard.vue'
import GroupEditPanel from './GroupEditPanel.vue'
import { groupFilterQuery, parseGroupFilters } from './group-route'

const { t, n, locale } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()
const route = useRoute()
const router = useRouter()
const filters = computed(() => parseGroupFilters(route.query))
const search = ref(filters.value.q)
const composing = ref(false)
const visibleCount = ref(60)
const expanded = ref(new Set<number>())
const editing = ref<GroupRow>()
const pending = ref(new Set<number>())
const enabledOverrides = ref(new Map<number, boolean>())
const notice = ref<{ text: string; tone: 'success' | 'danger' }>()
let editTrigger: HTMLElement | undefined
let searchTimer: ReturnType<typeof setTimeout> | undefined
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
    staleTime: 30_000,
    refetchInterval: editing.value || expanded.value.size ? false : 30_000,
    refetchIntervalInBackground: false,
  })),
)
const data = query.data
const counts = computed(() => {
  const groups = data.value?.items ?? []
  return {
    all: groups.length,
    serving: groups.filter(isServing).length,
    attention: groups.filter(needsAttention).length,
    paused: groups.filter(isPaused).length,
  }
})
const channels = computed(() => [
  { value: '', label: t('groups.board.allChannels') },
  ...Array.from(
    new Map((data.value?.items ?? []).map((group) => [group.channelID, group.channelName])),
  )
    .map(([value, label]) => ({ value, label }))
    .sort((a, b) => a.label.localeCompare(b.label, locale.value)),
])
const sortOptions = computed(() =>
  groupSorts.map((value) => ({ value, label: t(`groups.board.sort.${value}`) })),
)
const filtered = computed(() => {
  const f = filters.value
  const words = f.q.toLocaleLowerCase().split(/\s+/u).filter(Boolean)
  const rank = (group: GroupRow) => (isPaused(group) ? 2 : needsAttention(group) ? 0 : 1)
  return (data.value?.items ?? [])
    .filter((group) => {
      if (f.view === 'serving' && !isServing(group)) return false
      if (f.view === 'attention' && !needsAttention(group)) return false
      if (f.view === 'paused' && !isPaused(group)) return false
      if (f.channel && f.channel !== group.channelID) return false
      const text =
        `${group.name} ${group.channelName} ${group.channelID} ${group.endpoint}`.toLocaleLowerCase()
      return words.every((word) => text.includes(word))
    })
    .sort((a, b) => {
      if (f.sort === 'priority' && rank(a) !== rank(b)) return rank(a) - rank(b)
      if (f.sort !== 'name' && a.lastActiveHour !== b.lastActiveHour)
        return (b.lastActiveHour ?? -1) - (a.lastActiveHour ?? -1)
      return a.name.localeCompare(b.name, locale.value) || a.id - b.id
    })
})
const visible = computed(() => filtered.value.slice(0, visibleCount.value))
const changedFilters = computed(
  () => filters.value.q || filters.value.channel || filters.value.view !== 'all',
)
usePageRefresh({
  refresh: () => query.refetch(),
  pending: query.isFetching,
  updatedAt: () => data.value?.observedAt,
})

function updateFilters(patch: Partial<GroupFilters>, replace = false): void {
  clearTimeout(searchTimer)
  const next = {
    ...filters.value,
    q: Array.from(search.value.trim()).slice(0, 200).join(''),
    ...patch,
  }
  const location = { name: 'modern-groups', query: groupFilterQuery(next) }
  void (replace ? router.replace(location) : router.push(location))
}
function applySearch(): void {
  if (!composing.value) updateFilters({}, true)
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
  updateFilters({ q: '', view: 'all', channel: '' })
}
function toggleExpanded(id: number): void {
  if (expanded.value.has(id)) expanded.value.delete(id)
  else expanded.value.add(id)
}
watch(
  () => route.query,
  (value) => {
    if (route.name !== 'modern-groups') return
    clearTimeout(searchTimer)
    search.value = filters.value.q
    visibleCount.value = 60
    const canonical = groupFilterQuery(filters.value)
    if (
      Object.keys(value).length !== Object.keys(canonical).length ||
      Object.keys(canonical).some((key) => canonical[key] !== value[key])
    )
      void router.replace({ name: 'modern-groups', query: canonical })
  },
  { immediate: true },
)
onScopeDispose(() => {
  controller.abort()
  clearTimeout(searchTimer)
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
async function toggle(group: GroupRow, enabled: boolean): Promise<void> {
  if (pending.value.has(group.id)) return
  pending.value.add(group.id)
  enabledOverrides.value.set(group.id, enabled)
  notice.value = undefined
  try {
    const settings = await updateGroupBasics(client, group.id, { enabled }, controller.signal)
    if (controller.signal.aborted) return
    await refreshGroup(group.id, settings)
    if (!controller.signal.aborted)
      notice.value = {
        tone: 'success',
        text: t(enabled ? 'groups.enabled' : 'groups.disabled', { name: group.name }),
      }
  } catch {
    if (!controller.signal.aborted) {
      enabledOverrides.value.delete(group.id)
      notice.value = { tone: 'danger', text: t('groups.operationFailed') }
    }
  } finally {
    pending.value.delete(group.id)
  }
}
function openEditor(group: GroupRow, event: MouseEvent): void {
  editing.value = group
  editTrigger = event.currentTarget as HTMLElement
}
async function closeEditor(): Promise<void> {
  editing.value = undefined
  await nextTick()
  if (controller.signal.aborted) return
  const target =
    editTrigger?.isConnected && !editTrigger.matches(':disabled, [aria-disabled="true"]')
      ? editTrigger
      : document.getElementById('modern-content')
  target?.focus({ preventScroll: true })
}
async function onSaved(id: number, settings: GroupBasics): Promise<void> {
  pending.value.add(id)
  notice.value = { tone: 'success', text: t('groups.saved', { name: settings.name }) }
  try {
    await refreshGroup(id, settings)
  } finally {
    pending.value.delete(id)
  }
}
async function copyURL(value: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(value)
    if (!controller.signal.aborted) notice.value = { tone: 'success', text: t('groups.copied') }
  } catch {
    if (!controller.signal.aborted) notice.value = { tone: 'danger', text: t('groups.copyFailed') }
  }
}
</script>

<template>
  <div class="modern-page modern-groups-workspace">
    <AppNotice v-if="notice" :tone="notice.tone"
      >{{ notice.text
      }}<template #actions
        ><AppIconButton
          :icon="X"
          :label="t('shell.close')"
          size="xs"
          @click="notice = undefined" /></template
    ></AppNotice>
    <div class="modern-groups-tools">
      <div class="modern-groups-views" role="group" :aria-label="t('groups.statusFilter')">
        <button
          v-for="view in groupViews"
          :key="view"
          type="button"
          :class="{ 'is-selected': filters.view === view }"
          :aria-pressed="filters.view === view"
          @click="updateFilters({ view })"
        >
          {{ t(`groups.board.views.${view}`) }}<span v-if="data">{{ n(counts[view]) }}</span>
        </button>
      </div>
      <form
        class="modern-groups-filters"
        role="search"
        :aria-label="t('groups.search')"
        @submit.prevent="applySearch"
      >
        <div class="modern-groups-search">
          <AppTextField
            v-model="search"
            :label="t('groups.search')"
            label-hidden
            type="search"
            :placeholder="t('groups.board.search')"
            autocomplete="off"
            @update:model-value="scheduleSearch"
            @compositionstart="composing = true"
            @compositionend="endComposition"
            ><template #suffix><AppIcon :icon="Search" size="sm" /></template
          ></AppTextField>
        </div>
        <AppSelect
          :model-value="filters.channel"
          :label="t('groups.board.channel')"
          :options="channels"
          label-hidden
          @update:model-value="updateFilters({ channel: $event })"
        />
        <AppSelect
          :model-value="filters.sort"
          :label="t('groups.sort.label')"
          :options="sortOptions"
          label-hidden
          @update:model-value="updateFilters({ sort: $event as GroupFilters['sort'] })"
        />
      </form>
      <AppButton class="modern-groups-create" variant="primary" as-child
        ><RouterLink :to="{ name: 'modern-import' }"
          ><AppIcon :icon="Plus" />{{ t('groups.create') }}</RouterLink
        ></AppButton
      >
    </div>
    <div v-if="changedFilters || expanded.size" class="modern-groups-selection-info">
      <span>{{ t('groups.board.matching', { count: n(filtered.length) }) }}</span>
      <div>
        <AppButton v-if="changedFilters" variant="ghost" size="sm" @click="resetFilters">{{
          t('collection.reset')
        }}</AppButton
        ><AppButton v-if="expanded.size" variant="ghost" size="sm" @click="expanded.clear()">{{
          t('groups.board.collapseAll')
        }}</AppButton>
      </div>
    </div>
    <AppNotice v-if="query.isError.value && data" tone="warning"
      >{{ t('collection.stale')
      }}<template #actions
        ><AppButton size="sm" @click="query.refetch()">{{
          t('collection.retry')
        }}</AppButton></template
      ></AppNotice
    >
    <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
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
      v-else-if="!filtered.length"
      :title="t(data.items.length ? 'groups.noResults' : 'groups.empty')"
      :description="t(data.items.length ? 'groups.noResultsHelp' : 'groups.emptyHelp')"
      :icon="data.items.length ? Search : Layers2"
      ><AppButton v-if="changedFilters" @click="resetFilters">{{
        t('collection.reset')
      }}</AppButton></AppCollectionState
    >
    <section v-else class="modern-groups-grid" :aria-label="t('groups.list')">
      <GroupCard
        v-for="group in visible"
        :key="group.id"
        :group="group"
        :expanded="expanded.has(group.id)"
        :pending="pending.has(group.id)"
        :enabled-override="enabledOverrides.get(group.id)"
        @expand="toggleExpanded(group.id)"
        @edit="openEditor(group, $event)"
        @toggle="toggle(group, $event)"
        @copy="copyURL"
      />
    </section>
    <footer v-if="data && filtered.length" class="modern-groups-end">
      <AppButton v-if="visible.length < filtered.length" @click="visibleCount += 60">{{
        t('groups.board.showMore', { count: n(Math.min(60, filtered.length - visible.length)) })
      }}</AppButton>
      <span>{{
        t('groups.board.showing', { shown: n(visible.length), total: n(filtered.length) })
      }}</span>
    </footer>
    <GroupEditPanel
      v-if="editing"
      :key="editing.id"
      :group="editing"
      @close="closeEditor"
      @saved="onSaved"
    />
  </div>
</template>

<style scoped>
.modern-groups-tools {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-4);
  position: sticky;
  top: var(--modern-topbar-height);
  z-index: var(--modern-layer-raised);
  background: var(--modern-canvas);
  padding-block: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-groups-views {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-1);
}
.modern-groups-views > button {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-height: var(--modern-control-md);
  border: 0;
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-1-5) var(--modern-space-3);
  background: transparent;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  white-space: nowrap;
}
.modern-groups-views > button:hover {
  background: var(--modern-subtle);
  color: var(--modern-text);
}
.modern-groups-views > button.is-selected {
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
  font-weight: var(--modern-weight-medium);
}
.modern-groups-views > button span {
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
.modern-groups-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  margin-left: auto;
}
/* 操作按钮统一贴最右，搜索与筛选排在它左侧。 */
.modern-groups-create {
  flex-shrink: 0;
}
.modern-groups-search {
  width: 260px;
  max-width: 100%;
}
.modern-groups-selection-info {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-groups-selection-info > div {
  display: flex;
  gap: var(--modern-space-2);
}
.modern-groups-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 380px), 1fr));
  align-items: start;
  gap: var(--modern-space-4);
}
.modern-groups-end {
  display: grid;
  justify-items: center;
  gap: var(--modern-space-3);
  padding-block: var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
@media (max-width: 760px) {
  .modern-groups-tools {
    position: static;
  }
  .modern-groups-filters {
    width: 100%;
  }
  .modern-groups-search {
    width: 100%;
  }
  .modern-groups-views > button {
    min-height: var(--modern-touch-target);
  }
}
</style>
