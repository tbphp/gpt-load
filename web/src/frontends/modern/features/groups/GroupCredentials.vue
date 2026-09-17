<script setup lang="ts">
import {
  ChevronDown,
  Download,
  Layers,
  Pause,
  Play,
  Plus,
  RotateCcw,
  RefreshCw,
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
  getCredentialDetail,
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
  AppPagination,
  AppSegmentedControl,
  AppSortMenu,
  AppSelect,
  AppTextField,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import { useURLState, positivePage } from '@modern/app/url-state'
import { useMessageSource } from '@modern/app/messages'
import AppDraftGuard from '@modern/components/AppDraftGuard.vue'

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
const filters = useURLState<CredentialFilters>(
  ['q', 'status', 'page', 'page_size', 'sort', 'proxy', 'reset', 'credential_key'],
  (query) => ({
    credential:
      typeof query.credential_key === 'string' && /^[a-f0-9]{64}$/u.test(query.credential_key)
        ? query.credential_key
        : '',
    q: typeof query.q === 'string' ? query.q : '',
    status: credentialStates.find((value) => value === query.status) ?? '',
    page: positivePage(query.page),
    pageSize: [20, 50, 100].includes(Number(query.page_size)) ? Number(query.page_size) : 20,
    sort: credentialSorts.find((value) => value === query.sort) ?? 'priority',
    proxy:
      query.proxy === 'inherit' || query.proxy === 'direct' || query.proxy === 'custom'
        ? query.proxy
        : '',
    reset:
      props.group.connectionType === 'subscription' &&
      (query.reset === 'available' || query.reset === 'none' || query.reset === 'unknown')
        ? query.reset
        : '',
  }),
  (value) => ({
    ...(value.credential ? { credential_key: value.credential } : {}),
    ...(value.q ? { q: value.q } : {}),
    ...(value.status ? { status: value.status } : {}),
    ...(value.page > 1 ? { page: String(value.page) } : {}),
    ...(value.pageSize !== 20 ? { page_size: String(value.pageSize) } : {}),
    ...(value.sort !== 'priority' ? { sort: value.sort } : {}),
    ...(value.proxy ? { proxy: value.proxy } : {}),
    ...(value.reset ? { reset: value.reset } : {}),
  }),
)
const search = ref(filters.value.q)
watch(
  () => filters.value.q,
  (value) => {
    search.value = value
  },
)
const composing = ref(false)
const selected = ref(new Set<number>())
const credentialView = useURLState(
  ['credential', 'credential_view'],
  (query) => ({
    id: positivePage(query.credential, 0),
    mode: query.credential_view === 'test' ? 'test' : 'details',
  }),
  (value) =>
    value.id
      ? {
          credential: String(value.id),
          ...(value.mode === 'test' ? { credential_view: 'test' } : {}),
        }
      : {},
)
const credentialQuery = useQuery(
  computed(() => ({
    queryKey: credentialDetailKey(props.group.id, credentialView.value.id),
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getCredentialDetail(client, props.group.id, credentialView.value.id, signal),
    enabled: credentialView.value.id > 0,
  })),
)
const detail = computed({
  get: () =>
    credentialView.value.id && credentialView.value.mode === 'details'
      ? credentialQuery.data.value
      : undefined,
  set: (row: CredentialRow | undefined) => {
    credentialView.value = { id: row?.id ?? 0, mode: 'details' }
  },
})
const testing = computed({
  get: () =>
    credentialView.value.id && credentialView.value.mode === 'test'
      ? credentialQuery.data.value
      : undefined,
  set: (row: CredentialRow | undefined) => {
    credentialView.value = { id: row?.id ?? 0, mode: 'test' }
  },
})
const resetTarget = ref<CredentialRow>()
const resetKeys = new Map<number, string>()
const cardErrors = ref(new Map<number, string>())
const copyResolvers = new Map<number, () => Promise<string>>()
const downloads = new Set<string>()
const mutating = ref<number | 'batch'>()
const syncing = ref(new Set<number>())
const queuedSync = ref(new Set<number>())
const syncSucceeded = ref(new Set<number>())
const accountBatchPending = ref(false)
const pendingAction = ref('')
const deleting = ref<number[]>([])
type FullAction = 'download' | 'enable' | 'disable' | 'restore'
const fullTarget = ref<FullAction>()
const error = ref('')
const notice = ref('')
type AccountBatchAction = 'sync' | 'download'
const accountBatch = ref<{
  action: AccountBatchAction
  completed: number
  total: number
  failed: CredentialRow[]
}>()
const list = ref<InstanceType<typeof AppListFrame>>()
const controller = new AbortController()
const syncSuccessTimers = new Map<number, ReturnType<typeof setTimeout>>()
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
const filteredCredential = computed(() => (filters.value.credential ? rows.value[0] : undefined))
const busy = computed(() => query.isFetching.value || mutating.value !== undefined)
const syncPending = (id: number) => syncing.value.has(id) || queuedSync.value.has(id)
const bulkBusy = computed(() => busy.value || accountBatchPending.value || syncing.value.size > 0)
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
  ...(filters.value.credential
    ? [
        {
          key: 'credential',
          label: t('groups.board.credentialFilter'),
          value: filteredCredential.value
            ? filteredCredential.value.account ||
              (props.group.connectionType === 'api_key'
                ? filteredCredential.value.mask
                : props.group.channelName)
            : t('groups.board.selectedCredential'),
        },
      ]
    : []),
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
const canSyncSelected = computed(
  () =>
    Boolean(props.channel?.quotaObservation) &&
    rows.value
      .filter((row) => selected.value.has(row.id))
      .every((row) => row.authState === 'ready') &&
    rows.value.some((row) => selected.value.has(row.id) && !syncPending(row.id)),
)
watch(bulkBusy, (value) => emit('pending', value), { immediate: true })
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
  if (mutating.value !== undefined || accountBatchPending.value) return
  filters.value = { ...filters.value, ...value }
  selected.value = new Set()
  accountBatch.value = undefined
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
    ...(!key || key === 'credential' ? { credential: '' } : {}),
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
async function runAccountBatch(
  action: AccountBatchAction,
  targets = rows.value.filter((row) => selected.value.has(row.id)),
): Promise<void> {
  if (busy.value || accountBatchPending.value || props.group.connectionType !== 'subscription')
    return
  if (action === 'sync') targets = targets.filter((row) => !syncPending(row.id))
  if (!targets.length) return
  if (
    action === 'sync' &&
    (!props.channel?.quotaObservation || targets.some((row) => row.authState !== 'ready'))
  )
    return
  accountBatchPending.value = true
  if (action === 'download') mutating.value = 'batch'
  else queuedSync.value = new Set(targets.map((row) => row.id))
  error.value = ''
  notice.value = ''
  const report = { action, completed: 0, total: targets.length, failed: [] as CredentialRow[] }
  accountBatch.value = report
  const exports: unknown[] = []
  let cursor = 0
  async function worker(): Promise<void> {
    while (cursor < targets.length && !controller.signal.aborted) {
      const row = targets[cursor++]!
      cardErrors.value.delete(row.id)
      try {
        if (action === 'download') {
          const file = await exportCredential(client, props.group.id, row.id, controller.signal)
          if (!controller.signal.aborted) exports.push(JSON.parse(file.content))
        } else {
          queuedSync.value.delete(row.id)
          await syncQuota(row)
        }
      } catch {
        if (controller.signal.aborted) return
        report.failed.push(row)
        cardErrors.value.set(row.id, t('credentialCards.actionFailed'))
      }
      if (controller.signal.aborted) return
      report.completed++
      accountBatch.value = { ...report }
    }
  }
  try {
    await Promise.all(Array.from({ length: Math.min(2, targets.length) }, worker))
    if (controller.signal.aborted) return
    if (exports.length)
      downloadFile({
        filename: `gpt-load-accounts-${Date.now()}.json`,
        content: JSON.stringify(exports, null, 2),
      })
    if (action === 'sync') {
      emit('changed')
    }
    const visibleIDs = new Set(rows.value.map((row) => row.id))
    const targetIDs = new Set(targets.map((row) => row.id))
    selected.value = new Set(
      [
        ...[...selected.value].filter((id) => !targetIDs.has(id)),
        ...report.failed.map((row) => row.id),
      ].filter((id) => visibleIDs.has(id)),
    )
  } finally {
    queuedSync.value.clear()
    accountBatchPending.value = false
    if (action === 'download') mutating.value = undefined
  }
}
async function syncQuota(row: CredentialRow): Promise<void> {
  const previous = syncSuccessTimers.get(row.id)
  if (previous) clearTimeout(previous)
  syncSuccessTimers.delete(row.id)
  syncSucceeded.value.delete(row.id)
  syncing.value.add(row.id)
  cardErrors.value.delete(row.id)
  try {
    const observation = await refreshCredentialQuota(
      client,
      props.group.id,
      row.id,
      controller.signal,
    )
    if (controller.signal.aborted) return
    if (!observation) throw new Error('Missing observation')
    // 同步只更新该账号的观测数据，不覆盖其他并行操作，也不刷新整个列表。
    cache.setQueriesData<CredentialCollection>(
      { queryKey: groupCredentialsKey(props.group.id) },
      (data) =>
        data && {
          ...data,
          items: data.items.map((item) => (item.id === row.id ? { ...item, observation } : item)),
        },
    )
    cache.setQueryData<CredentialRow>(credentialDetailKey(props.group.id, row.id), (data) =>
      data ? { ...data, observation } : undefined,
    )
    if (observation.state !== 'fresh') throw new Error('Observation failed')
    syncSucceeded.value.add(row.id)
    syncSuccessTimers.set(
      row.id,
      setTimeout(() => {
        syncSucceeded.value.delete(row.id)
        syncSuccessTimers.delete(row.id)
      }, 3000),
    )
  } finally {
    syncing.value.delete(row.id)
  }
}
async function changed(): Promise<void> {
  await cache.invalidateQueries({ queryKey: groupCredentialsKey(props.group.id) })
  emit('changed')
}
async function toggle(row: CredentialRow, value: boolean): Promise<void> {
  if (busy.value || syncPending(row.id)) return
  if (!accountBatchPending.value) accountBatch.value = undefined
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
  if (bulkBusy.value || !ids.length) return
  accountBatch.value = undefined
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
  if (
    bulkBusy.value ||
    !summary.value?.total ||
    !fullActions.value.some((item) => item.id === value)
  )
    return
  error.value = ''
  fullTarget.value = value as FullAction
}
async function applyFullAction(): Promise<void> {
  const value = fullTarget.value
  if (!value || bulkBusy.value) return
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
  if (busy.value || syncPending(row.id)) return
  if (value === 'quota') {
    if (!props.channel?.quotaObservation || row.authState !== 'ready') return
    if (!accountBatchPending.value) accountBatch.value = undefined
    try {
      await syncQuota(row)
      if (!controller.signal.aborted) emit('changed')
    } catch {
      if (!controller.signal.aborted)
        cardErrors.value.set(row.id, t('credentialCards.actionFailed'))
    }
    return
  }
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
  if (!['restore', 'refresh', 'download'].includes(value)) return
  if (!accountBatchPending.value) accountBatch.value = undefined
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
  if (!row || busy.value || syncPending(row.id)) return
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
  syncSuccessTimers.forEach((timer) => clearTimeout(timer))
  downloads.forEach((url) => URL.revokeObjectURL(url))
})
useMessageSource(() => (notice.value ? { text: notice.value, tone: 'success' } : undefined))
useMessageSource(() =>
  credentialQuery.isError.value
    ? {
        text: t('groups.edit.loadFailed'),
        tone: 'danger',
        action: { label: t('ui.retry'), run: () => credentialQuery.refetch() },
      }
    : undefined,
)
useMessageSource(() =>
  error.value && !fullTarget.value && !deleting.value.length
    ? { text: error.value, tone: 'danger' }
    : undefined,
)
useMessageSource(() =>
  stale.value
    ? {
        text: t('groupDetail.refreshFailed'),
        tone: 'warning',
        action: { label: t('ui.retry'), run: refresh },
      }
    : undefined,
)
useMessageSource(() =>
  accountBatch.value
    ? {
        text: accountBatchPending.value
          ? t('groupWorkflows.batchProgress', {
              done: n(accountBatch.value.completed),
              total: n(accountBatch.value.total),
            })
          : t('groupWorkflows.batchResult', {
              success: n(accountBatch.value.total - accountBatch.value.failed.length),
              failed: n(accountBatch.value.failed.length),
            }),
        tone: accountBatch.value.failed.length
          ? 'warning'
          : accountBatchPending.value
            ? 'info'
            : 'success',
        action:
          accountBatch.value.failed.length && !bulkBusy.value
            ? {
                label: t('groupWorkflows.retryFailed'),
                run: () => runAccountBatch(accountBatch.value!.action, accountBatch.value!.failed),
              }
            : undefined,
      }
    : undefined,
)
useMessageSource(() =>
  !accountBatch.value && !resetTarget.value && cardErrors.value.size
    ? {
        text: [...cardErrors.value.values()].at(-1)!,
        tone: 'danger',
      }
    : undefined,
)
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
        :disabled="mutating !== undefined || accountBatchPending"
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
        :disabled="mutating !== undefined || accountBatchPending"
        @update:model-value="change({ reset: $event as CredentialFilters['reset'], page: 1 })"
      />
      <AppSelect
        class="modern-credentials-filter-control"
        :model-value="filters.proxy"
        :label="t('groupDetail.filters.proxy')"
        label-hidden
        :options="proxyOptions"
        :disabled="mutating !== undefined || accountBatchPending"
        @update:model-value="change({ proxy: $event as CredentialFilters['proxy'], page: 1 })"
      />
      <div class="modern-credentials-toolbar-actions">
        <AppSortMenu
          :model-value="filters.sort"
          :label="t('groups.sort.label')"
          :options="sortOptions"
          :disabled="mutating !== undefined || accountBatchPending"
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
        :disabled="mutating !== undefined || accountBatchPending"
        @update:model-value="change({ status: $event, page: 1 })"
      /><AppFilterSummary
        class="modern-credentials-filter-summary"
        :items="filterSummary"
        :disabled="mutating !== undefined || accountBatchPending"
        @remove="resetFilter"
        @reset="resetFilter()"
      />
    </div>
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
              :disabled="bulkBusy || !rows.length"
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
                :disabled="bulkBusy"
                @click="batch('enable')"
              /><AppIconButton
                :icon="Pause"
                :label="t('groupDetail.disableSelected')"
                size="sm"
                :disabled="bulkBusy"
                @click="batch('disable')"
              /><AppIconButton
                :icon="Trash2"
                :label="t('groupDetail.deleteSelected')"
                size="sm"
                :disabled="bulkBusy"
                @click="deleting = [...selected]"
              />
              <AppIconButton
                v-if="group.connectionType === 'subscription' && channel?.quotaObservation"
                :icon="RefreshCw"
                :label="t('groupWorkflows.syncSelected')"
                size="sm"
                :loading="accountBatchPending && accountBatch?.action === 'sync'"
                :disabled="busy || accountBatchPending || !canSyncSelected"
                @click="runAccountBatch('sync')"
              />
              <AppIconButton
                v-if="group.connectionType === 'subscription'"
                :icon="Download"
                :label="t('groupWorkflows.downloadSelected')"
                size="sm"
                :disabled="bulkBusy"
                @click="runAccountBatch('download')"
              />
            </template>
          </div>
          <AppActionMenu
            :label="t('groupDetail.full.actions')"
            :items="fullActions"
            :disabled="bulkBusy || !summary?.total"
            @select="openFullAction"
          >
            <template #trigger>
              <AppButton
                :icon="Layers"
                variant="ghost"
                size="sm"
                :disabled="bulkBusy || !summary?.total"
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
            :pending="mutating === row.id || syncPending(row.id)"
            :pending-action="
              syncPending(row.id) ? 'quota' : mutating === row.id ? pendingAction : undefined
            "
            :sync-succeeded="syncSucceeded.has(row.id)"
            :disabled="busy || syncPending(row.id)"
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
          :disabled="mutating !== undefined || accountBatchPending"
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
    :disabled="bulkBusy"
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
    :disabled="bulkBusy"
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
  <AppDraftGuard
    :dirty="false"
    :pending="mutating !== undefined || accountBatchPending || syncing.size > 0"
  />
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
