<script setup lang="ts">
import {
  Plus,
  Upload,
  Play,
  Pause,
  Trash2,
  Pencil,
  FlaskConical,
  RefreshCw,
  Search,
  Network,
} from '@lucide/vue'
import { useQuery, useQueryClient, keepPreviousData } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useApiClient } from '@shared/http/client-context'
import {
  batchProxies,
  importProxies,
  listProxies,
  proxyImpact,
  proxyListKey,
  proxyTestSucceeded,
  runProxyTests,
  saveProxyTestURL,
  validTestURL,
  type ProxyFilters,
  type ProxyImpact,
  type ProxyItem,
  type ProxyList,
} from '@shared/proxies/api'
import { useRoute, useRouter, type LocationQuery } from 'vue-router'
import ProxyEditor from './ProxyEditor.vue'
import CollectionFilterBar from '@/components/collection/CollectionFilterBar.vue'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import IconButton from '@/components/ui/IconButton.vue'
import PaginationBar from '@/components/ui/PaginationBar.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import DataTable from '@/components/ui/DataTable.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'

const { t, n, locale } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const route = useRoute()
const router = useRouter()
const positivePage = (value: unknown) =>
  Number.isSafeInteger(Number(value)) && Number(value) > 0 ? Number(value) : 1
const parseFilters = (query: LocationQuery): ProxyFilters => ({
  q: String(query.q ?? ''),
  state: String(query.state ?? ''),
  scheme: String(query.scheme ?? ''),
  used: String(query.used ?? ''),
  test: String(query.test ?? ''),
  sort: query.sort === 'latency' ? 'latency' : 'name',
  page: positivePage(query.page),
  page_size: [20, 50, 100].includes(Number(query.page_size)) ? Number(query.page_size) : 20,
})
const filters = ref(parseFilters(route.query))
watch(
  () => route.query,
  (query) => {
    const value = parseFilters(query)
    if (JSON.stringify(value) !== JSON.stringify(filters.value)) {
      filters.value = value
      selected.value = new Set()
    }
  },
)
watch(filters, (value) => {
  void router
    .replace({
      query: Object.fromEntries(
        Object.entries(value)
          .filter(([, v]) => v !== '')
          .map(([key, value]) => [key, String(value)]),
      ),
    })
    .catch(() => {
      error.value = t('proxies.operationFailed')
    })
})

const query = useQuery({
  queryKey: computed(() => [...proxyListKey, filters.value]),
  queryFn: ({ signal }) => listProxies(client, filters.value, signal),
  placeholderData: keepPreviousData,
})
const selected = ref(new Set<number>())
const search = ref(filters.value.q)
watch(
  () => filters.value.q,
  (value) => {
    search.value = value
  },
)
const target = ref('')
const savedTarget = ref('')
const targetSaveFailed = ref(false)
const savingTarget = ref(false)
const targetState = computed(() => {
  const value = target.value.trim()
  if (!savedTarget.value || !validTestURL(value)) return ''
  if (savingTarget.value) return 'saving'
  if (value === savedTarget.value) return 'saved'
  return targetSaveFailed.value ? 'saveFailed' : 'saving'
})
let initialized = false
let targetTimer: ReturnType<typeof setTimeout> | undefined
let searchTimer: ReturnType<typeof setTimeout> | undefined
let alive = true
watch(
  query.data,
  (value) => {
    if (!value) return
    if (!initialized || (!savingTarget.value && target.value.trim() === savedTarget.value)) {
      target.value = value.test_url
      savedTarget.value = value.test_url
      initialized = true
    }
    const maxPage = Math.max(1, Math.ceil(value.total / filters.value.page_size))
    if (filters.value.page > maxPage) filters.value = { ...filters.value, page: maxPage }
  },
  { immediate: true },
)
watch(search, (q) => {
  clearTimeout(searchTimer)
  if (q === filters.value.q) return
  searchTimer = setTimeout(() => change({ q }), 250)
})
watch(target, () => {
  clearTimeout(targetTimer)
  targetSaveFailed.value = false
  if (
    initialized &&
    target.value.trim() !== savedTarget.value &&
    validTestURL(target.value.trim())
  ) {
    targetTimer = setTimeout(() => void saveTarget(), 500)
  }
})
async function saveTarget() {
  if (savingTarget.value || !initialized || !validTestURL(target.value.trim())) return
  savingTarget.value = true
  targetSaveFailed.value = false
  try {
    while (validTestURL(target.value.trim()) && target.value.trim() !== savedTarget.value) {
      const value = target.value.trim()
      await saveProxyTestURL(client, value)
      savedTarget.value = value
      cache.setQueriesData<ProxyList>({ queryKey: proxyListKey }, (previous) =>
        previous ? { ...previous, test_url: value } : previous,
      )
    }
  } catch {
    targetSaveFailed.value = true
  } finally {
    savingTarget.value = false
  }
}
function change(value: Partial<ProxyFilters>) {
  filters.value = { ...filters.value, ...value, page: value.page ?? 1 }
  selected.value = new Set()
}
const hasFilters = computed(() =>
  Boolean(
    filters.value.q ||
    filters.value.state ||
    filters.value.scheme ||
    filters.value.used ||
    filters.value.test,
  ),
)
const hasConditions = computed(() => hasFilters.value || filters.value.sort !== 'name')
function resetFilters() {
  clearTimeout(searchTimer)
  search.value = ''
  change({ q: '', state: '', scheme: '', used: '', test: '', sort: 'name' })
}
const updates = ref(new Map<number, ProxyItem>())
const rows = computed(() =>
  (query.data.value?.items ?? []).map((row) => {
    const result = updates.value.get(row.id)
    return result && (result.last_test_at_ms ?? 0) >= (row.last_test_at_ms ?? 0)
      ? {
          ...row,
          last_test_url: result.last_test_url,
          last_test_at_ms: result.last_test_at_ms,
          last_test_duration_ms: result.last_test_duration_ms,
          last_test_status_code: result.last_test_status_code,
          last_test_error: result.last_test_error,
        }
      : row
  }),
)
const allSelected = computed(
  () => rows.value.length > 0 && rows.value.every((row) => selected.value.has(row.id)),
)
function toggle(id: number, checked: boolean) {
  const next = new Set(selected.value)
  if (checked) next.add(id)
  else next.delete(id)
  selected.value = next
}
const states = computed(() =>
  ['', 'enabled', 'disabled'].map((value) => ({ value, label: t('proxies.' + (value || 'all')) })),
)
const schemes = computed(() => [
  { value: '', label: t('proxies.protocol') },
  { value: 'http', label: 'HTTP' },
  { value: 'socks5', label: 'SOCKS5' },
])
const usages = computed(() =>
  ['', 'used', 'unused'].map((value) => ({
    value,
    label: t('proxies.' + (value || 'references')),
  })),
)
const tests = computed(() =>
  ['', 'untested', 'success', 'failed'].map((value) => ({
    value,
    label: t('proxies.' + (value || 'latestTest')),
  })),
)
const sorts = computed(() => [
  { value: 'name', label: t('proxies.sortName') },
  { value: 'latency', label: t('proxies.sortLatency') },
])
const editor = ref<ProxyItem | null | undefined>()
const importing = ref(false)
const importText = ref('')
const notice = ref('')
const error = ref('')
const pending = ref(false)
const impact = ref<ProxyImpact>()
const hasReferences = computed(() =>
  Boolean(impact.value?.global || impact.value?.groups.length || impact.value?.credentials.length),
)
const confirmation = ref<{ ids: number[]; action: 'disable' | 'delete' }>()
const running = ref(false)
const preparing = ref(false)
const total = ref(0)
const done = ref(0)
const successful = ref(0)
const failed = ref<number[]>([])
const testing = ref(new Set<number>())
let testController: AbortController | undefined
const progress = computed(() =>
  t('proxies.progress', {
    done: n(done.value),
    total: n(total.value),
    success: n(successful.value),
    failed: n(failed.value.length),
    cancelled: n(running.value ? 0 : total.value - done.value),
  }),
)
async function refresh() {
  await cache.invalidateQueries({ queryKey: proxyListKey })
}
async function run(ids: number[]) {
  if (running.value || !ids.length || !validTestURL(target.value.trim())) return
  const controller = new AbortController()
  testController = controller
  running.value = true
  total.value = ids.length
  done.value = successful.value = 0
  failed.value = []
  testing.value = new Set(ids)
  await runProxyTests(client, ids, target.value.trim(), controller.signal, (id, result) => {
    testing.value.delete(id)
    done.value++
    if (result) updates.value.set(id, result)
    if (result && proxyTestSucceeded(result)) successful.value++
    else failed.value.push(id)
  })
  running.value = false
  testing.value = new Set()
  if (alive) await refresh()
}
async function runFiltered() {
  if (running.value || preparing.value) return
  preparing.value = true
  error.value = ''
  try {
    const list = await listProxies(client, filters.value, undefined, true)
    if (alive) await run(list.items.map((row) => row.id))
  } catch {
    error.value = t('proxies.operationFailed')
  } finally {
    preparing.value = false
  }
}
async function act(action: 'enable' | 'disable' | 'delete', ids: number[]) {
  if (!ids.length || pending.value) return
  pending.value = true
  error.value = ''
  try {
    if (action !== 'enable') {
      impact.value = await proxyImpact(client, ids)
      if (action === 'delete' || hasReferences.value) {
        confirmation.value = { ids, action }
        return
      }
    }
    await batchProxies(client, ids, action)
    selected.value = new Set()
    await refresh()
  } catch {
    error.value = t('proxies.operationFailed')
  } finally {
    pending.value = false
  }
}
async function confirm() {
  if (!confirmation.value || pending.value) return
  pending.value = true
  error.value = ''
  try {
    await batchProxies(client, confirmation.value.ids, confirmation.value.action)
    confirmation.value = undefined
    selected.value = new Set()
    await refresh()
  } catch {
    error.value = t('proxies.operationFailed')
  } finally {
    pending.value = false
  }
}
async function importRows() {
  if (pending.value || !importText.value.trim()) return
  pending.value = true
  error.value = ''
  try {
    const result = await importProxies(client, importText.value)
    notice.value = t('proxies.importResult', {
      imported: n(result.imported),
      duplicates: n(result.duplicates),
      lines: result.invalid_lines.length ? result.invalid_lines.join(', ') : t('proxies.noInvalid'),
    })
    importText.value = ''
    importing.value = false
    await refresh()
  } catch {
    error.value = t('proxies.operationFailed')
  } finally {
    pending.value = false
  }
}
const confirmationDescription = computed(() => {
  if (!confirmation.value) return ''
  if (!hasReferences.value) {
    return t('proxies.deleteWarning', { count: n(confirmation.value.ids.length) })
  }
  return t('proxies.confirmWarning', { action: t('proxies.' + confirmation.value.action) })
})
function testDetails(row: ProxyItem) {
  const result = row.last_test_error
    ? t('proxies.' + row.last_test_error)
    : `HTTP ${row.last_test_status_code}`
  return [result, row.last_test_url].filter(Boolean).join(' · ')
}
function time(value: number | null) {
  return value === null ? '' : new Date(value).toLocaleString(locale.value)
}
onScopeDispose(() => {
  alive = false
  clearTimeout(targetTimer)
  clearTimeout(searchTimer)
  testController?.abort()
  void saveTarget()
})
</script>

<template>
  <PageFrame aria-labelledby="proxies-title">
    <LedgerSheet class="proxy-page" :aria-busy="query.isFetching.value || undefined">
      <PageHeader id="proxies-title" class="proxy-header" :title="t('proxies.title')">
        <template #actions>
          <AppButton
            variant="secondary"
            size="compact"
            :busy="query.isFetching.value"
            @click="query.refetch()"
          >
            <RefreshCw :size="15" aria-hidden="true" />{{ t('proxies.refresh') }}
          </AppButton>
          <AppButton variant="secondary" size="compact" @click="importing = true">
            <Upload :size="15" aria-hidden="true" />{{ t('proxies.import') }}
          </AppButton>
          <AppButton size="compact" @click="editor = null">
            <Plus :size="15" aria-hidden="true" />{{ t('proxies.new') }}
          </AppButton>
        </template>
      </PageHeader>
      <AsyncRefreshIndicator
        :active="Boolean(query.data.value) && query.isFetching.value"
        :label="t('common.asyncLoading')"
      />

      <CollectionFilterBar class="proxy-toolbar" :label="t('proxies.search')">
        <label class="collection-filter-field collection-filter-field--search">
          <span class="collection-filter-label">{{ t('proxies.search') }}</span>
          <AppSearchInput
            v-model="search"
            :label="t('proxies.search')"
            :placeholder="t('proxies.search')"
            :clear-label="t('proxies.clearSearch')"
          />
        </label>
        <label class="collection-filter-field">
          <span class="collection-filter-label">{{ t('proxies.protocol') }}</span>
          <AppSelect
            :model-value="filters.scheme"
            :options="schemes"
            :label="t('proxies.protocol')"
            size="compact"
            @update:model-value="change({ scheme: $event })"
          />
        </label>
        <label class="collection-filter-field">
          <span class="collection-filter-label">{{ t('proxies.references') }}</span>
          <AppSelect
            :model-value="filters.used"
            :options="usages"
            :label="t('proxies.references')"
            size="compact"
            @update:model-value="change({ used: $event })"
          />
        </label>
        <label class="collection-filter-field">
          <span class="collection-filter-label">{{ t('proxies.latestTest') }}</span>
          <AppSelect
            :model-value="filters.test"
            :options="tests"
            :label="t('proxies.latestTest')"
            size="compact"
            @update:model-value="change({ test: $event })"
          />
        </label>
        <label class="collection-filter-field">
          <span class="collection-filter-label">{{ t('proxies.sort') }}</span>
          <AppSelect
            :model-value="filters.sort"
            :options="sorts"
            :label="t('proxies.sort')"
            size="compact"
            @update:model-value="change({ sort: $event })"
          />
        </label>
      </CollectionFilterBar>

      <div class="proxy-testbar">
        <div class="proxy-test-target">
          <span class="proxy-test-label" aria-hidden="true">{{ t('proxies.testURL') }}</span>
          <div class="proxy-test-input">
            <AppTextInput
              v-model="target"
              :label="t('proxies.testURL')"
              type="text"
              size="compact"
              :disabled="!initialized"
              :invalid="Boolean(target) && !validTestURL(target.trim())"
              @blur="saveTarget"
            />
          </div>
        </div>
        <span v-if="targetState" class="proxy-save-state" role="status">
          {{ t('proxies.' + targetState) }}
        </span>
        <AppButton
          v-if="targetState === 'saveFailed'"
          variant="link"
          size="inline"
          @click="saveTarget"
        >
          {{ t('proxies.save') }}
        </AppButton>
        <AppTooltip :content="t('proxies.testHelp')">
          <AppButton
            variant="secondary"
            size="compact"
            :disabled="
              running || preparing || !query.data.value?.total || !validTestURL(target.trim())
            "
            :busy="preparing"
            @click="runFiltered"
          >
            <FlaskConical :size="14" aria-hidden="true" />
            {{ t('proxies.testFiltered') }}
            <span v-if="query.data.value">({{ n(query.data.value.total) }})</span>
          </AppButton>
        </AppTooltip>
      </div>
      <div class="proxy-filterbar">
        <SegmentedControl
          :model-value="filters.state"
          :options="states"
          :label="t('proxies.status')"
          size="compact"
          @update:model-value="change({ state: $event })"
        />
        <AppButton v-if="hasConditions" variant="link" size="inline" @click="resetFilters">
          {{ t('proxies.resetFilters') }}
        </AppButton>
      </div>
      <InlineFeedback v-if="error" tone="danger" appearance="ledger">{{ error }}</InlineFeedback>
      <InlineFeedback v-if="notice" tone="success" appearance="ledger">{{ notice }}</InlineFeedback>
      <InlineFeedback
        v-if="query.isError.value && query.data.value"
        tone="danger"
        appearance="ledger"
      >
        {{ t('proxies.loadFailed') }}
        <template #action>
          <AppButton variant="link" size="inline" @click="query.refetch()">{{
            t('common.retry')
          }}</AppButton>
        </template>
      </InlineFeedback>
      <div v-if="total" class="proxy-progress" role="status" aria-live="polite">
        <span>{{ progress }}</span>
        <AppButton
          v-if="running"
          variant="secondary"
          size="compact"
          @click="testController?.abort()"
        >
          {{ t('proxies.stop') }}
        </AppButton>
        <AppButton
          v-else-if="failed.length"
          variant="secondary"
          size="compact"
          @click="run([...failed])"
        >
          {{ t('proxies.retryFailed') }}
        </AppButton>
      </div>
      <div v-if="selected.size" class="proxy-batch">
        <span>{{ t('proxies.selected', { count: n(selected.size) }) }}</span>
        <AppButton
          size="compact"
          variant="secondary"
          :disabled="running || !validTestURL(target.trim())"
          @click="run([...selected])"
        >
          <FlaskConical :size="14" aria-hidden="true" />{{ t('proxies.testSelected') }}
        </AppButton>
        <AppTooltip :content="t('proxies.enable')">
          <IconButton
            :label="t('proxies.enable')"
            size="compact"
            variant="ghost"
            :disabled="pending"
            @click="act('enable', [...selected])"
          >
            <Play :size="15" aria-hidden="true" />
          </IconButton>
        </AppTooltip>
        <AppTooltip :content="t('proxies.disable')">
          <IconButton
            :label="t('proxies.disable')"
            size="compact"
            variant="ghost"
            :disabled="pending"
            @click="act('disable', [...selected])"
          >
            <Pause :size="15" aria-hidden="true" />
          </IconButton>
        </AppTooltip>
        <AppTooltip :content="t('proxies.delete')">
          <IconButton
            :label="t('proxies.delete')"
            size="compact"
            variant="ghost"
            tone="danger"
            :disabled="pending"
            @click="act('delete', [...selected])"
          >
            <Trash2 :size="15" aria-hidden="true" />
          </IconButton>
        </AppTooltip>
      </div>

      <SkeletonSurface
        v-if="!query.data.value && query.isPending.value"
        variant="collection"
        :rows="6"
        :columns="7"
        row-height="64px"
        :label="t('common.asyncLoading')"
      />
      <EmptyState
        v-else-if="!query.data.value"
        :title="t('proxies.loadFailed')"
        description=""
        variant="ledger"
      >
        <template #actions>
          <AppButton variant="secondary" size="compact" @click="query.refetch()">{{
            t('common.retry')
          }}</AppButton>
        </template>
      </EmptyState>
      <EmptyState
        v-else-if="!rows.length"
        :title="t(hasFilters ? 'proxies.noResults' : 'proxies.empty')"
        description=""
        variant="ledger"
      >
        <template #icon
          ><Search v-if="hasFilters" :size="20" /><Network v-else :size="20"
        /></template>
        <template v-if="hasFilters" #actions>
          <AppButton variant="secondary" size="compact" @click="resetFilters">{{
            t('proxies.resetFilters')
          }}</AppButton>
        </template>
      </EmptyState>
      <DataTable
        v-else
        class="proxy-table"
        :caption="t('proxies.title')"
        appearance="editorial"
        dense
      >
        <colgroup>
          <col class="proxy-column-select" />
          <col class="proxy-column-name" />
          <col class="proxy-column-protocol" />
          <col class="proxy-column-status" />
          <col class="proxy-column-references" />
          <col class="proxy-column-result" />
          <col class="proxy-column-actions" />
        </colgroup>
        <thead>
          <tr>
            <th scope="col">
              <AppTooltip :content="t('proxies.selectPage')">
                <input
                  class="proxy-checkbox"
                  type="checkbox"
                  :checked="allSelected"
                  :indeterminate="selected.size > 0 && !allSelected"
                  :aria-label="t('proxies.selectPage')"
                  @change="
                    selected = ($event.target as HTMLInputElement).checked
                      ? new Set(rows.map((row) => row.id))
                      : new Set()
                  "
                />
              </AppTooltip>
            </th>
            <th scope="col">{{ t('proxies.name') }}</th>
            <th scope="col">{{ t('proxies.protocol') }}</th>
            <th scope="col">{{ t('proxies.status') }}</th>
            <th scope="col">{{ t('proxies.references') }}</th>
            <th scope="col">{{ t('proxies.latestTest') }}</th>
            <th scope="col">{{ t('proxies.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in rows"
            :key="row.id"
            :class="{ 'proxy-row-selected': selected.has(row.id) }"
          >
            <td>
              <input
                class="proxy-checkbox"
                type="checkbox"
                :checked="selected.has(row.id)"
                :aria-label="row.name || row.display_url"
                @change="toggle(row.id, ($event.target as HTMLInputElement).checked)"
              />
            </td>
            <td>
              <div class="proxy-identity">
                <span class="proxy-avatar"><Network :size="16" aria-hidden="true" /></span>
                <div class="proxy-description">
                  <OverflowTooltip class="proxy-name" :content="row.name || row.display_url">{{
                    row.name || row.display_url
                  }}</OverflowTooltip>
                  <OverflowTooltip
                    v-if="row.name"
                    class="proxy-address"
                    :content="row.display_url"
                    >{{ row.display_url }}</OverflowTooltip
                  >
                </div>
              </div>
            </td>
            <td>
              <span class="proxy-protocol">{{ row.scheme.toUpperCase() }}</span>
            </td>
            <td>
              <StatusBadge
                :tone="row.enabled ? 'success' : 'neutral'"
                :icon="row.enabled ? 'check' : 'off'"
                size="compact"
              >
                {{ t(row.enabled ? 'proxies.enabled' : 'proxies.disabled') }}
              </StatusBadge>
            </td>
            <td>
              <div class="proxy-references">
                <span v-if="row.group_count">{{
                  t('proxies.groups', { count: n(row.group_count) })
                }}</span>
                <span v-if="row.credential_count">{{
                  t('proxies.credentials', { count: n(row.credential_count) })
                }}</span>
                <StatusBadge v-if="row.global" tone="info" size="compact">{{
                  t('proxies.global')
                }}</StatusBadge>
                <span v-if="!row.group_count && !row.credential_count && !row.global">—</span>
              </div>
            </td>
            <td>
              <StatusBadge v-if="row.last_test_at_ms === null" size="compact">{{
                t('proxies.untested')
              }}</StatusBadge>
              <div v-else class="proxy-result">
                <div class="proxy-result-summary">
                  <AppTooltip :content="testDetails(row)">
                    <StatusBadge
                      :tone="proxyTestSucceeded(row) ? 'success' : 'danger'"
                      size="compact"
                    >
                      {{ t(proxyTestSucceeded(row) ? 'proxies.success' : 'proxies.failed') }}
                    </StatusBadge>
                  </AppTooltip>
                  <span v-if="row.last_test_duration_ms !== null" class="proxy-latency"
                    >{{ n(row.last_test_duration_ms) }} ms</span
                  >
                </div>
                <small>{{ time(row.last_test_at_ms) }}</small>
              </div>
            </td>
            <td>
              <div class="proxy-actions">
                <AppTooltip :content="t('proxies.test')">
                  <IconButton
                    :label="t('proxies.test')"
                    size="compact"
                    variant="ghost"
                    :busy="testing.has(row.id)"
                    :disabled="running || !validTestURL(target.trim())"
                    @click="run([row.id])"
                  >
                    <FlaskConical :size="15" aria-hidden="true" />
                  </IconButton>
                </AppTooltip>
                <AppTooltip :content="t('proxies.edit')">
                  <IconButton
                    :label="t('proxies.edit')"
                    size="compact"
                    variant="ghost"
                    @click="editor = row"
                  >
                    <Pencil :size="15" aria-hidden="true" />
                  </IconButton>
                </AppTooltip>
                <AppTooltip :content="t(row.enabled ? 'proxies.disable' : 'proxies.enable')">
                  <IconButton
                    :label="t(row.enabled ? 'proxies.disable' : 'proxies.enable')"
                    size="compact"
                    variant="ghost"
                    :disabled="pending"
                    @click="act(row.enabled ? 'disable' : 'enable', [row.id])"
                  >
                    <Pause v-if="row.enabled" :size="15" aria-hidden="true" /><Play
                      v-else
                      :size="15"
                      aria-hidden="true"
                    />
                  </IconButton>
                </AppTooltip>
                <AppTooltip :content="t('proxies.delete')">
                  <IconButton
                    :label="t('proxies.delete')"
                    size="compact"
                    variant="ghost"
                    tone="danger"
                    :disabled="pending"
                    @click="act('delete', [row.id])"
                  >
                    <Trash2 :size="15" aria-hidden="true" />
                  </IconButton>
                </AppTooltip>
              </div>
            </td>
          </tr>
        </tbody>
      </DataTable>
      <PaginationBar
        :page="filters.page"
        :page-size="filters.page_size"
        :total-items="query.data.value?.total ?? 0"
        :total-pages="Math.max(1, Math.ceil((query.data.value?.total ?? 0) / filters.page_size))"
        :pending="query.isFetching.value"
        show-page-size
        @previous="change({ page: filters.page - 1 })"
        @next="change({ page: filters.page + 1 })"
        @update:page-size="change({ page_size: $event })"
      />
    </LedgerSheet>

    <ProxyEditor
      v-if="editor !== undefined"
      :proxy="editor ?? undefined"
      @close="editor = undefined"
      @saved="editor = undefined"
    />
    <AppConfirmDialog
      :open="Boolean(confirmation)"
      :title="
        t('proxies.confirmTitle', { action: t('proxies.' + (confirmation?.action ?? 'delete')) })
      "
      :description="confirmationDescription"
      :close-label="t('proxies.cancel')"
      :cancel-label="t('proxies.cancel')"
      :confirm-label="t('proxies.' + (confirmation?.action ?? 'delete'))"
      tone="danger"
      appearance="ledger"
      :pending="pending"
      @update:open="!$event && (confirmation = undefined)"
      @confirm="confirm"
    >
      <div
        v-if="impact && hasReferences"
        class="proxy-impact"
        :aria-label="t('proxies.references')"
      >
        <StatusBadge v-if="impact.groups.length" size="compact">{{
          t('proxies.groups', { count: n(impact.groups.length) })
        }}</StatusBadge>
        <StatusBadge v-if="impact.credentials.length" size="compact">{{
          t('proxies.credentials', { count: n(impact.credentials.length) })
        }}</StatusBadge>
        <StatusBadge v-if="impact.global" tone="info" size="compact">{{
          t('proxies.global')
        }}</StatusBadge>
      </div>
      <InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback>
    </AppConfirmDialog>
    <AppDrawer
      :open="importing"
      :title="t('proxies.import')"
      :description="t('proxies.importHelp')"
      :close-label="t('proxies.cancel')"
      :dismissible="!pending"
      @update:open="importing = $event"
    >
      <form class="proxy-panel" @submit.prevent="importRows">
        <label for="proxy-import">{{ t('proxies.importHelp') }}</label>
        <textarea
          id="proxy-import"
          v-model="importText"
          :disabled="pending"
          :aria-label="t('proxies.address')"
          autocomplete="off"
          spellcheck="false"
          rows="12"
        />
        <InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback>
        <AppButton type="submit" :busy="pending" :disabled="!importText.trim()">{{
          t('proxies.import')
        }}</AppButton>
      </form>
    </AppDrawer>
  </PageFrame>
</template>

<style scoped>
.proxy-page {
  display: flex;
  min-width: 0;
  flex-direction: column;
}
.proxy-header {
  flex-wrap: wrap;
}
.proxy-toolbar {
  grid-template-columns: minmax(240px, 2fr) repeat(4, minmax(110px, 1fr));
}
.proxy-testbar,
.proxy-test-target,
.proxy-filterbar,
.proxy-batch,
.proxy-progress,
.proxy-actions,
.proxy-references,
.proxy-result-summary,
.proxy-impact {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.proxy-testbar {
  flex-wrap: wrap;
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: var(--space-3);
  margin-bottom: var(--space-4);
}
.proxy-test-target {
  min-width: 0;
  max-width: 100%;
}
.proxy-test-label {
  flex: none;
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}
.proxy-test-input {
  width: 360px;
  min-width: 0;
}
.proxy-save-state {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.proxy-filterbar {
  flex-wrap: wrap;
  justify-content: space-between;
  padding-bottom: var(--space-5);
}
.proxy-progress {
  flex-wrap: wrap;
  justify-content: space-between;
  padding-bottom: var(--space-3);
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}
.proxy-batch {
  flex-wrap: wrap;
  padding-bottom: var(--space-3);
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}
.proxy-table {
  --table-editorial-min-width: 1000px;
  min-width: 0;
}
.proxy-table :deep(.data-table) {
  table-layout: fixed;
}
.proxy-column-select {
  width: 32px;
}
.proxy-column-name {
  width: 26%;
}
.proxy-column-protocol {
  width: 78px;
}
.proxy-column-status {
  width: 88px;
}
.proxy-column-references {
  width: 140px;
}
.proxy-column-result {
  width: 185px;
}
.proxy-column-actions {
  width: 136px;
}
.proxy-checkbox {
  width: 15px;
  height: 15px;
  margin: 0;
  vertical-align: middle;
  accent-color: var(--color-action);
  cursor: pointer;
}
.proxy-checkbox:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
.proxy-table :deep(.proxy-row-selected td) {
  background: var(--color-action-soft);
}
.proxy-identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}
.proxy-avatar {
  display: grid;
  width: var(--control-compact);
  height: var(--control-compact);
  flex: none;
  place-items: center;
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text-faint);
}
.proxy-description,
.proxy-result {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: var(--space-1);
}
.proxy-name {
  font-size: var(--text-body);
  font-weight: 600;
}
.proxy-address {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.proxy-protocol {
  display: inline-flex;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-tag);
  padding: var(--space-1) var(--space-2);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.proxy-references,
.proxy-impact {
  flex-wrap: wrap;
  color: var(--color-text-muted);
  font-size: var(--text-meta);
  white-space: normal;
}
.proxy-result small {
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.proxy-latency {
  color: var(--color-text-muted);
  font-size: var(--text-meta);
  font-variant-numeric: tabular-nums;
}
.proxy-actions {
  gap: var(--space-1);
}
.proxy-panel {
  display: grid;
  gap: var(--space-4);
}
.proxy-panel textarea {
  width: 100%;
  resize: vertical;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  padding: var(--space-3);
  color: var(--color-text);
  background: var(--color-surface);
  font-family: var(--font-mono);
}
@media (max-width: 860px) {
  .proxy-toolbar {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 560px) {
  .proxy-toolbar {
    grid-template-columns: minmax(0, 1fr);
  }
  .proxy-test-target {
    width: 100%;
  }
  .proxy-test-input {
    flex: 1;
    width: auto;
  }
}
</style>
