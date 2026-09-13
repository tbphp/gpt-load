<script setup lang="ts">
import {
  ChevronDown,
  Download,
  Layers,
  Pause,
  Play,
  Plus,
  RotateCcw,
  Search,
  Trash2,
} from '@lucide/vue'
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  batchGroupCredentials,
  batchAllGroupCredentials,
  credentialStates,
  credentialSorts,
  getGroupCredentials,
  groupCredentialsKey,
  setCredentialEnabled,
  type CredentialCollection,
  type CredentialFilters,
  type CredentialRow,
} from '@modern/api/group-detail'
import type { GroupRow } from '@modern/api/groups'
import type { GroupChannel } from '@modern/api/group-create'
import {
  credentialDetailKey,
  exportCredential,
  exportAllCredentials,
  refreshCredentialQuota,
  resetCredentialQuota,
  revealCredential,
  runCredentialAction,
} from '@modern/api/credential-actions'
import { ApiError } from '@shared/http/errors'
import { createOperationKey } from './group-create-operation'
import APIKeyCredentialCard from './APIKeyCredentialCard.vue'
import SubscriptionCredentialCard from './SubscriptionCredentialCard.vue'
import CredentialDetailPanel from './CredentialDetailPanel.vue'
import CredentialTestDialog from './CredentialTestDialog.vue'
import {
  AppButton,
  AppActionMenu,
  AppIcon,
  AppCheckbox,
  AppCollectionState,
  AppConfirmDialog,
  AppFilterSummary,
  AppIconButton,
  AppListFrame,
  AppNotice,
  AppPagination,
  AppSegmentedControl,
  AppSortMenu,
  AppSelect,
  AppTextField,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import GroupDraftGuard from './GroupDraftGuard.vue'

const props = defineProps<{ group: GroupRow; channel?: GroupChannel }>()
const emit = defineEmits<{
  add: []
  changed: []
  pending: [value: boolean]
  updatedAt: [value: number]
}>()
const { t, n } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const filters = ref<CredentialFilters>({
  q: '',
  status: '',
  page: 1,
  pageSize: 20,
  sort: 'priority',
  proxy: '',
  reset: '',
})
const search = ref('')
const composing = ref(false)
const selected = ref(new Set<number>())
const detail = ref<CredentialRow>()
const testing = ref<CredentialRow>()
const resetTarget = ref<CredentialRow>()
const resetKeys = new Map<number, string>()
const cardErrors = ref(new Map<number, string>())
const copyResolvers = new Map<number, () => Promise<string>>()
const downloads = new Set<string>()
const mutating = ref<number | 'batch'>()
const pendingAction = ref('')
const deleting = ref<number[]>([])
type FullAction = 'download' | 'enable' | 'disable' | 'restore'
const fullTarget = ref<FullAction>()
const error = ref('')
const notice = ref('')
const list = ref<InstanceType<typeof AppListFrame>>()
const controller = new AbortController()
let searchTimer: ReturnType<typeof setTimeout> | undefined
const query = useQuery(
  computed(() => ({
    queryKey: [...groupCredentialsKey(props.group.id), filters.value],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getGroupCredentials(client, props.group.id, filters.value, signal),
    placeholderData: keepPreviousData,
  })),
)
const rows = computed(() => query.data.value?.items ?? [])
const busy = computed(() => query.isFetching.value || mutating.value !== undefined)
const stale = computed(() => query.isError.value && Boolean(query.data.value))
const summary = computed(() => query.data.value?.counts)
const fullActions = computed(() => [
  { id: 'enable', label: t('groupDetail.full.enable'), icon: Play },
  { id: 'disable', label: t('groupDetail.full.disable'), icon: Pause },
  { id: 'restore', label: t('groupDetail.full.restore'), icon: RotateCcw },
  { id: 'download', label: t('groupDetail.full.download'), icon: Download },
])
const fullIcon = computed(
  () => fullActions.value.find((item) => item.id === fullTarget.value)?.icon,
)
const segments = computed(() => [
  { value: '', label: t('groupDetail.allCredentials'), count: summary.value?.total },
  ...credentialStates.map((value) => ({
    value,
    label: t('groups.credentials.' + value),
    count: summary.value?.[value],
  })),
])
const sortOptions = computed(() =>
  credentialSorts.map((value) => ({
    value,
    label: t('groupDetail.filters.sorts.' + value),
  })),
)
const proxyOptions = computed(() => [
  { value: '', label: t('groupDetail.filters.allProxies') },
  ...['inherit', 'direct', 'custom'].map((value) => ({
    value,
    label: t('credentialCards.proxyMode.' + value),
  })),
])
const resetOptions = computed(() => [
  { value: '', label: t('groupDetail.filters.allResets') },
  ...['available', 'none', 'unknown'].map((value) => ({
    value,
    label: t('groupDetail.filters.resets.' + value),
  })),
])
const filterSummary = computed(() => [
  ...(filters.value.sort !== 'priority'
    ? [
        {
          key: 'sort',
          label: t('groups.sort.label'),
          value: t('groupDetail.filters.sorts.' + filters.value.sort),
        },
      ]
    : []),
  ...(filters.value.proxy
    ? [
        {
          key: 'proxy',
          label: t('groupDetail.filters.proxy'),
          value: t('credentialCards.proxyMode.' + filters.value.proxy),
        },
      ]
    : []),
  ...(filters.value.reset
    ? [
        {
          key: 'reset',
          label: t('groupDetail.filters.reset'),
          value: t('groupDetail.filters.resets.' + filters.value.reset),
        },
      ]
    : []),
  ...(filters.value.q
    ? [{ key: 'q', label: t('groupDetail.searchLabel'), value: filters.value.q }]
    : []),
  ...(filters.value.status
    ? [
        {
          key: 'status',
          label: t('groupDetail.status'),
          value: t('groups.credentials.' + filters.value.status),
        },
      ]
    : []),
])
const allSelected = computed(
  () => rows.value.length > 0 && rows.value.every((row) => selected.value.has(row.id)),
)
watch(busy, (value) => emit('pending', value), { immediate: true })
watch(query.dataUpdatedAt, (value) => emit('updatedAt', value), { immediate: true })
watch(query.data, (data) => {
  if (!data || query.isPlaceholderData.value) return
  const max = Math.max(1, Math.ceil(data.total / filters.value.pageSize))
  if (filters.value.page > max) filters.value = { ...filters.value, page: max }
  selected.value = new Set(
    [...selected.value].filter((id) => data.items.some((row) => row.id === id)),
  )
})
function change(value: Partial<typeof filters.value>): void {
  if (mutating.value !== undefined) return
  filters.value = { ...filters.value, ...value }
  selected.value = new Set()
  list.value?.scrollToTop()
}
function scheduleSearch(): void {
  clearTimeout(searchTimer)
  if (!composing.value)
    searchTimer = setTimeout(() => change({ q: search.value.trim(), page: 1 }), 200)
}
function compositionEnd(): void {
  composing.value = false
  scheduleSearch()
}
function resetFilter(key?: string): void {
  clearTimeout(searchTimer)
  if (!key || key === 'q') search.value = ''
  change({
    page: 1,
    ...(!key || key === 'q' ? { q: '' } : {}),
    ...(!key || key === 'status' ? { status: '' } : {}),
    ...(!key || key === 'sort' ? { sort: 'priority' as const } : {}),
    ...(!key || key === 'proxy' ? { proxy: '' as const } : {}),
    ...(!key || key === 'reset' ? { reset: '' as const } : {}),
  })
}
function select(id: number, value: boolean): void {
  const next = new Set(selected.value)
  if (value) next.add(id)
  else next.delete(id)
  selected.value = next
}
async function refresh(): Promise<void> {
  await query.refetch()
}
async function changed(): Promise<void> {
  await cache.invalidateQueries({ queryKey: groupCredentialsKey(props.group.id) })
  emit('changed')
}
async function toggle(row: CredentialRow, value: boolean): Promise<void> {
  if (busy.value) return
  mutating.value = row.id
  pendingAction.value = 'toggle'
  error.value = ''
  notice.value = ''
  cardErrors.value.delete(row.id)
  try {
    await setCredentialEnabled(client, props.group.id, row.id, value, controller.signal)
    await changed()
  } catch {
    if (!controller.signal.aborted) cardErrors.value.set(row.id, t('groups.edit.saveFailed'))
  } finally {
    mutating.value = undefined
  }
}
async function batch(
  action: 'enable' | 'disable' | 'delete',
  ids = [...selected.value],
): Promise<void> {
  if (busy.value || !ids.length) return
  mutating.value = 'batch'
  error.value = ''
  notice.value = ''
  try {
    const affected = await batchGroupCredentials(
      client,
      props.group.id,
      ids,
      action,
      controller.signal,
    )
    selected.value = new Set()
    deleting.value = []
    notice.value = t('groupDetail.updatedCredentials', { count: n(affected.length) })
    await changed()
  } catch {
    if (!controller.signal.aborted) error.value = t('groups.edit.saveFailed')
  } finally {
    mutating.value = undefined
  }
}
function copySecret(id: number): () => Promise<string> {
  let resolver = copyResolvers.get(id)
  if (!resolver) {
    resolver = () => revealCredential(client, props.group.id, id, controller.signal)
    copyResolvers.set(id, resolver)
  }
  return resolver
}
function cacheRow(row: CredentialRow): void {
  cache.setQueriesData<CredentialCollection>(
    { queryKey: groupCredentialsKey(props.group.id) },
    (data) =>
      data && {
        ...data,
        items: data.items.map((item) =>
          item.id === row.id
            ? {
                ...row,
                weightManual: row.weightManual === undefined ? item.weightManual : row.weightManual,
                daily: row.daily ?? item.daily,
                lastUsed: row.lastUsed ?? item.lastUsed,
              }
            : item,
        ),
      },
  )
}
function downloadFile(file: { filename: string; content: string; type?: string }): void {
  const url = URL.createObjectURL(
    new Blob([file.content], { type: file.type ?? 'application/json;charset=utf-8' }),
  )
  downloads.add(url)
  const link = document.createElement('a')
  link.href = url
  link.download = file.filename
  document.body.append(link)
  link.click()
  link.remove()
  setTimeout(() => {
    URL.revokeObjectURL(url)
    downloads.delete(url)
  }, 1000)
}
function openFullAction(value: string): void {
  if (busy.value || !summary.value?.total || !fullActions.value.some((item) => item.id === value))
    return
  error.value = ''
  fullTarget.value = value as FullAction
}
async function applyFullAction(): Promise<void> {
  const value = fullTarget.value
  if (!value || busy.value) return
  mutating.value = 'batch'
  error.value = ''
  notice.value = ''
  try {
    let count = 0
    if (value === 'download') {
      const result = await exportAllCredentials(client, props.group.id, controller.signal)
      if (controller.signal.aborted) return
      result.files.forEach(downloadFile)
      count = result.count
    } else {
      const affected = await batchAllGroupCredentials(
        client,
        props.group.id,
        value,
        controller.signal,
      )
      if (controller.signal.aborted) return
      count = affected.length
      selected.value = new Set()
      await changed()
      void cache.invalidateQueries({ queryKey: ['modern', 'credential-detail', props.group.id] })
    }
    fullTarget.value = undefined
    notice.value = t('groupDetail.full.succeeded.' + value, { count: n(count) })
  } catch {
    if (!controller.signal.aborted) error.value = t('credentialCards.actionFailed')
  } finally {
    mutating.value = undefined
  }
}
function saved(row: CredentialRow): void {
  cacheRow(row)
  void changed()
  void cache.invalidateQueries({ queryKey: credentialDetailKey(props.group.id, row.id) })
}
async function action(row: CredentialRow, value: string): Promise<void> {
  if (busy.value) return
  if (value === 'details') {
    detail.value = row
    return
  }
  if (value === 'test') {
    testing.value = row
    return
  }
  if (value === 'delete') {
    deleting.value = [row.id]
    return
  }
  if (value === 'reset') {
    if (!props.channel?.resetCredit || !row.observation?.resetCredits) return
    if (!resetKeys.has(row.id)) resetKeys.set(row.id, createOperationKey())
    cardErrors.value.delete(row.id)
    resetTarget.value = row
    return
  }
  if (!['quota', 'restore', 'refresh', 'download'].includes(value)) return
  mutating.value = row.id
  pendingAction.value = value
  cardErrors.value.delete(row.id)
  try {
    if (value === 'download') {
      const file = await exportCredential(client, props.group.id, row.id, controller.signal)
      if (controller.signal.aborted) return
      downloadFile(file)
      return
    }
    if (value === 'quota') {
      const observation = await refreshCredentialQuota(
        client,
        props.group.id,
        row.id,
        controller.signal,
      )
      if (observation) cacheRow({ ...row, observation })
    } else
      cacheRow(
        await runCredentialAction(
          client,
          props.group.id,
          row.id,
          value as 'restore' | 'refresh',
          controller.signal,
        ),
      )
    await changed()
    void cache.invalidateQueries({ queryKey: credentialDetailKey(props.group.id, row.id) })
  } catch {
    if (!controller.signal.aborted) cardErrors.value.set(row.id, t('credentialCards.actionFailed'))
  } finally {
    mutating.value = undefined
  }
}
async function resetQuota(): Promise<void> {
  const row = resetTarget.value
  if (!row || busy.value) return
  const key = resetKeys.get(row.id)
  if (!key) return
  mutating.value = row.id
  pendingAction.value = 'reset'
  cardErrors.value.delete(row.id)
  try {
    const result = await resetCredentialQuota(
      client,
      props.group.id,
      row.id,
      key,
      controller.signal,
    )
    if (controller.signal.aborted) return
    if (result.observation) cacheRow({ ...row, observation: result.observation })
    resetKeys.delete(row.id)
    resetTarget.value = undefined
    notice.value = t(
      result.pending ? 'credentialCards.resetPending' : 'credentialCards.resetSucceeded',
    )
    await changed()
    void cache.invalidateQueries({ queryKey: credentialDetailKey(props.group.id, row.id) })
  } catch (cause) {
    if (controller.signal.aborted) return
    const unknown =
      !(cause instanceof ApiError) ||
      cause.code === 'RESET_CREDIT_OUTCOME_UNKNOWN' ||
      cause.status >= 500
    if (!unknown) {
      resetKeys.delete(row.id)
      resetTarget.value = undefined
    }
    cardErrors.value.set(
      row.id,
      t(unknown ? 'credentialCards.resetUnknown' : 'credentialCards.actionFailed'),
    )
  } finally {
    mutating.value = undefined
  }
}
onScopeDispose(() => {
  controller.abort()
  clearTimeout(searchTimer)
  downloads.forEach((url) => URL.revokeObjectURL(url))
})
defineExpose({ refresh })
</script>

<template>
  <section class="modern-credentials">
    <div class="modern-credentials-toolbar">
      <AppTextField
        v-model="search"
        :label="t('groupDetail.searchCredentials')"
        label-hidden
        :placeholder="t('groupDetail.searchCredentials')"
        :icon="Search"
        :loading="query.isFetching.value"
        :disabled="mutating !== undefined"
        type="search"
        @input="scheduleSearch"
        @compositionstart="composing = true"
        @compositionend="compositionEnd"
      />
      <AppSelect
        v-if="group.connectionType === 'subscription'"
        class="modern-credentials-filter-control"
        :model-value="filters.reset"
        :label="t('groupDetail.filters.reset')"
        label-hidden
        :options="resetOptions"
        :disabled="mutating !== undefined"
        @update:model-value="change({ reset: $event as CredentialFilters['reset'], page: 1 })"
      />
      <AppSelect
        class="modern-credentials-filter-control"
        :model-value="filters.proxy"
        :label="t('groupDetail.filters.proxy')"
        label-hidden
        :options="proxyOptions"
        :disabled="mutating !== undefined"
        @update:model-value="change({ proxy: $event as CredentialFilters['proxy'], page: 1 })"
      />
      <div class="modern-credentials-toolbar-actions">
        <AppSortMenu
          :model-value="filters.sort"
          :label="t('groups.sort.label')"
          :options="sortOptions"
          :disabled="mutating !== undefined"
          @update:model-value="change({ sort: $event as CredentialFilters['sort'], page: 1 })"
        />
        <AppButton :icon="Plus" variant="primary" :disabled="!channel" @click="emit('add')">{{
          t(
            group.connectionType === 'subscription'
              ? 'groupDetail.connectAccount'
              : 'groupDetail.addCredentials',
          )
        }}</AppButton>
      </div>
    </div>
    <div class="modern-credentials-filters">
      <AppSegmentedControl
        :model-value="filters.status"
        :label="t('groupDetail.status')"
        :options="segments"
        :disabled="mutating !== undefined"
        @update:model-value="change({ status: $event, page: 1 })"
      /><AppFilterSummary
        class="modern-credentials-filter-summary"
        :items="filterSummary"
        :disabled="mutating !== undefined"
        @remove="resetFilter"
        @reset="resetFilter()"
      />
    </div>
    <AppNotice v-if="error" tone="danger">{{ error }}</AppNotice>
    <AppNotice v-else-if="stale" tone="warning"
      >{{ t('groupDetail.refreshFailed')
      }}<template #actions
        ><AppButton size="sm" @click="refresh">{{ t('ui.retry') }}</AppButton></template
      ></AppNotice
    >
    <AppNotice v-if="notice" tone="success">{{ notice }}</AppNotice>
    <AppListFrame
      ref="list"
      :label="t('groupDetail.credentials')"
      :loading="query.isFetching.value"
    >
      <template #header>
        <div class="modern-credentials-selection">
          <div class="modern-credentials-selected-actions">
            <AppCheckbox
              :model-value="allSelected"
              :indeterminate="selected.size > 0 && !allSelected"
              :label="t('groupDetail.selectPage')"
              :disabled="busy || !rows.length"
              @update:model-value="
                selected = $event ? new Set(rows.map((row) => row.id)) : new Set()
              "
            />
            <template v-if="selected.size"
              ><span>{{ t('groupDetail.selected', { count: n(selected.size) }) }}</span
              ><AppIconButton
                :icon="Play"
                :label="t('groupDetail.enableSelected')"
                size="sm"
                :disabled="busy"
                @click="batch('enable')" /><AppIconButton
                :icon="Pause"
                :label="t('groupDetail.disableSelected')"
                size="sm"
                :disabled="busy"
                @click="batch('disable')" /><AppIconButton
                :icon="Trash2"
                :label="t('groupDetail.deleteSelected')"
                size="sm"
                :disabled="busy"
                @click="deleting = [...selected]"
            /></template>
          </div>
          <AppActionMenu
            :label="t('groupDetail.full.actions')"
            :items="fullActions"
            :disabled="busy || !summary?.total"
            @select="openFullAction"
          >
            <template #trigger>
              <AppButton
                :icon="Layers"
                variant="ghost"
                size="sm"
                :disabled="busy || !summary?.total"
              >
                {{ t('groupDetail.full.actions') }}<AppIcon :icon="ChevronDown" size="xs" />
              </AppButton>
            </template>
          </AppActionMenu>
        </div>
      </template>
      <AppCollectionState
        v-if="query.isError.value && !query.data.value"
        :title="t('groups.edit.loadFailed')"
        error
        ><AppButton @click="refresh">{{ t('ui.retry') }}</AppButton></AppCollectionState
      >
      <AppCollectionState
        v-else-if="!rows.length && !query.isFetching.value"
        :title="t(filterSummary.length ? 'groupDetail.noMatches' : 'groupDetail.noCredentials')"
      />
      <div
        v-else
        class="modern-credential-cards"
        :class="{
          'modern-credential-cards--subscriptions': group.connectionType === 'subscription',
        }"
      >
        <template v-for="row in rows" :key="row.id">
          <SubscriptionCredentialCard
            v-if="group.connectionType === 'subscription'"
            :row="row"
            :channel="channel"
            :selected="selected.has(row.id)"
            :pending="mutating === row.id"
            :pending-action="mutating === row.id ? pendingAction : undefined"
            :disabled="busy"
            :error="cardErrors.get(row.id)"
            @select="select(row.id, $event)"
            @toggle="toggle(row, $event)"
            @action="action(row, $event)"
          />
          <APIKeyCredentialCard
            v-else
            :row="row"
            :selected="selected.has(row.id)"
            :pending="mutating === row.id"
            :disabled="busy"
            :error="cardErrors.get(row.id)"
            :resolve-secret="copySecret(row.id)"
            @select="select(row.id, $event)"
            @toggle="toggle(row, $event)"
            @action="action(row, $event)"
          />
        </template>
      </div>
      <template #footer
        ><AppPagination
          :page="filters.page"
          :page-size="filters.pageSize"
          mode="total"
          :total="query.data.value?.total"
          :pending="query.isFetching.value"
          :disabled="mutating !== undefined"
          @update:page="change({ page: $event })"
          @update:page-size="change({ pageSize: $event, page: 1 })"
      /></template>
    </AppListFrame>
  </section>
  <AppConfirmDialog
    :open="Boolean(fullTarget)"
    :icon="fullIcon"
    :title="fullTarget ? t('groupDetail.full.' + fullTarget) : ''"
    :subject="group.name"
    :description="
      t(
        fullTarget === 'restore'
          ? 'groupDetail.full.restoreDescription'
          : 'groupDetail.full.description',
      )
    "
    :confirm-label="fullTarget ? t('groupDetail.full.' + fullTarget) : ''"
    :pending="mutating !== undefined"
    :disabled="busy"
    :error="error"
    @cancel="fullTarget = undefined"
    @confirm="applyFullAction"
  />
  <AppConfirmDialog
    :open="deleting.length > 0"
    :icon="Trash2"
    tone="danger"
    :title="t('groupDetail.deleteCredential')"
    :description="t('groupDetail.deleteConfirmation', { count: n(deleting.length) })"
    :confirm-label="t('groupDetail.deleteCredential')"
    :pending="mutating !== undefined"
    :disabled="busy"
    :error="error"
    @cancel="deleting = []"
    @confirm="batch('delete', deleting)"
  />
  <AppConfirmDialog
    :open="Boolean(resetTarget)"
    :icon="RotateCcw"
    :title="t('credentialCards.useReset')"
    :subject="resetTarget?.account || resetTarget?.mask"
    :description="t('credentialCards.confirmReset')"
    :confirm-label="t('credentialCards.useReset')"
    :pending="mutating !== undefined"
    :disabled="busy"
    :error="resetTarget ? cardErrors.get(resetTarget.id) : undefined"
    @cancel="resetTarget = undefined"
    @confirm="resetQuota"
  />
  <CredentialDetailPanel
    v-if="detail"
    :key="detail.id"
    :group="group"
    :row="detail"
    :channel="channel"
    @close="detail = undefined"
    @saved="saved"
  />
  <CredentialTestDialog
    v-if="testing"
    :key="testing.id"
    :group-id="group.id"
    :row="testing"
    @close="testing = undefined"
    @changed="changed"
  />
  <GroupDraftGuard :dirty="false" :pending="mutating !== undefined" />
</template>

<style scoped>
.modern-credentials {
  container: modern-credentials / inline-size;
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: var(--modern-space-3);
  min-width: 0;
  min-height: 0;
}
.modern-credentials-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: var(--modern-space-3);
  padding: var(--modern-space-1) 0;
}
.modern-credentials-toolbar > :first-child {
  flex: 2 1 220px;
  min-width: 0;
}
.modern-credentials-filter-control {
  flex: 1 1 150px;
  min-width: 0;
}
.modern-credentials-toolbar > :last-child {
  flex: none;
  margin-left: auto;
}
.modern-credentials-toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  max-width: 100%;
  gap: var(--modern-space-2);
}
.modern-credentials-filters {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2) var(--modern-space-3);
}
.modern-credentials-filter-summary {
  margin-left: auto;
  justify-content: flex-end;
}
.modern-credentials-selection {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--modern-space-2);
  min-height: var(--modern-control-nav);
  padding: 0 var(--modern-space-1) var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-credentials-selected-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.modern-credentials-selected-actions > :first-child {
  margin-right: var(--modern-space-1);
}
.modern-credential-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, var(--modern-key-card-min)), 1fr));
  align-content: start;
  align-items: start;
  gap: var(--modern-credential-grid-gap);
  padding: var(--modern-space-1) var(--modern-space-1) var(--modern-space-4);
}
.modern-credential-cards--subscriptions {
  grid-template-columns: repeat(auto-fill, minmax(min(100%, var(--modern-account-card-min)), 1fr));
}
@container modern-credentials (max-width: 420px) {
  .modern-credentials-toolbar {
    flex-wrap: wrap;
  }
  .modern-credentials-toolbar > :first-child {
    flex-basis: 100%;
  }
}
</style>
